package handoff

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

type BindingView struct {
	DeviceID         string
	ProjectID        string
	Revision         string
	TargetClient     string
	TargetClientName string
	ActionActor      string
	Evidence         []BoundEvidence
}

type BoundEvidence struct {
	Evidence protocol.Evidence
	Files    []store.ResolvedFileRef
}

type DeliveryPackage struct {
	Handoff                 []byte
	Manifest                []byte
	CandidateResponse       []byte
	CandidateResponseSchema []byte
}

type ClientAdapter interface {
	Name() string
	Export(context.Context, store.TaskSnapshot, BindingView) (DeliveryPackage, error)
}

type ExportOptions struct {
	TaskID       string
	ClientID     string
	Adapter      string
	DeviceID     string
	AfterEventID int64
	OutputDir    string
}

type ExportReport struct {
	Adapter      string
	TargetClient string
	TaskID       string
	ProjectID    string
	ThroughEvent int64
	PackageID    string
}

type NextExportReport struct {
	ExportReport
	OutputDir          string `json:"output_dir"`
	FromEventExclusive int64  `json:"from_event_exclusive"`
	Reused             bool   `json:"reused"`
}

type HandoffHistoryRecord struct {
	HandoffHistoryEntry
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}

type Publisher interface {
	Publish(string, DeliveryPackage) error
}

type Service struct {
	Query         store.TaskQuery
	Bindings      store.BindingStore
	Resolver      store.BindingResolver
	Registry      store.RegistryStore
	Publisher     Publisher
	WorkspaceRoot string
	History       HandoffHistory
	Locker        store.Locker
	Clock         func() time.Time
}

func NewService(query store.TaskQuery, bindings store.BindingStore, resolver store.BindingResolver, registry store.RegistryStore, workspaceRoot ...string) *Service {
	root := ""
	if len(workspaceRoot) > 0 {
		root = workspaceRoot[0]
	}
	service := &Service{
		Query:         query,
		Bindings:      bindings,
		Resolver:      resolver,
		Registry:      registry,
		Publisher:     NewDirectoryPublisher(root),
		WorkspaceRoot: root,
		Locker:        store.FlockLocker{},
		Clock:         time.Now,
	}
	if root != "" {
		service.History = NewFileHandoffHistory(root, service.Locker)
	}
	return service
}

func (s *Service) Export(ctx context.Context, options ExportOptions) (ExportReport, error) {
	if s.Query == nil || s.Bindings == nil || s.Resolver == nil || s.Registry == nil || s.Publisher == nil {
		return ExportReport{}, fmt.Errorf("handoff service is not configured")
	}
	if !protocol.IsValidID(options.TaskID) || !protocol.IsValidID(options.ClientID) || !protocol.IsValidID(options.DeviceID) || options.AfterEventID < 0 || strings.TrimSpace(options.OutputDir) == "" {
		return ExportReport{}, fmt.Errorf("invalid handoff export options")
	}
	snapshot, err := s.Query.SnapshotForActor(ctx, options.TaskID, options.AfterEventID, options.ClientID)
	if err != nil {
		return ExportReport{}, err
	}
	switch snapshot.Health {
	case store.Healthy:
	case store.RecoverableTail:
		return ExportReport{}, store.ErrRecoveryRequired
	default:
		return ExportReport{}, store.ErrCorrupt
	}
	target, err := s.Registry.ReadClient(ctx, options.ClientID)
	if err != nil {
		return ExportReport{}, err
	}
	if !target.HasCapability("import_export") {
		return ExportReport{}, fmt.Errorf("handoff target %q lacks import_export capability", target.ID)
	}
	binding, err := s.Bindings.ReadBinding(ctx, options.DeviceID, snapshot.Project.ID)
	if err != nil {
		return ExportReport{}, err
	}
	if !s.Bindings.BindingAvailable(ctx, options.DeviceID, snapshot.Project.ID) {
		return ExportReport{}, fmt.Errorf("%w: project %q on device %q", store.ErrBindingUnavailable, snapshot.Project.ID, options.DeviceID)
	}
	if err := s.validatePortableSnapshot(ctx, snapshot, binding, target); err != nil {
		return ExportReport{}, err
	}
	view, err := s.bindingView(ctx, binding, target, snapshot.Evidence, snapshot.ActionActor)
	if err != nil {
		return ExportReport{}, err
	}
	adapter, err := adapterFor(options.Adapter)
	if err != nil {
		return ExportReport{}, err
	}
	packageData, err := adapter.Export(ctx, snapshot, view)
	if err != nil {
		return ExportReport{}, err
	}
	if err := s.Publisher.Publish(options.OutputDir, packageData); err != nil {
		return ExportReport{}, err
	}
	manifest, err := DecodeManifest(packageData.Manifest)
	if err != nil {
		return ExportReport{}, fmt.Errorf("generated handoff manifest is invalid: %w", err)
	}
	return ExportReport{Adapter: adapter.Name(), TargetClient: target.ID, TaskID: snapshot.Task.ID, ProjectID: snapshot.Project.ID, ThroughEvent: snapshot.ThroughEvent, PackageID: manifest.PackageID}, nil
}

func (s *Service) ExportNext(ctx context.Context, taskID string) (NextExportReport, error) {
	if s.Query == nil || s.Bindings == nil || s.History == nil || s.Locker == nil || strings.TrimSpace(s.WorkspaceRoot) == "" || !protocol.IsValidID(taskID) {
		return NextExportReport{}, fmt.Errorf("handoff next export is not configured")
	}
	lock, err := s.Locker.Lock(ctx, filepath.Join(s.WorkspaceRoot, "collaboration", ".runtime", "locks", "handoff-next", taskID+".lock"))
	if err != nil {
		return NextExportReport{}, err
	}
	defer lock.Unlock()
	snapshot, err := s.Query.Snapshot(ctx, taskID, 0)
	if err != nil {
		return NextExportReport{}, err
	}
	switch snapshot.Health {
	case store.Healthy:
	case store.RecoverableTail:
		return NextExportReport{}, store.ErrRecoveryRequired
	default:
		return NextExportReport{}, store.ErrCorrupt
	}
	target := nextTarget(snapshot.Task, snapshot.State)
	adapter := adapterForTarget(target)
	if adapter == "" {
		return NextExportReport{}, fmt.Errorf("no automatic handoff adapter for client %q", target)
	}
	entry, manifest, found, err := s.latestVerifiedHistory(ctx, taskID, target)
	if err != nil {
		return NextExportReport{}, err
	}
	if found && entry.ThroughEvent > snapshot.State.LastEventID {
		return NextExportReport{}, fmt.Errorf("handoff history exceeds current task event")
	}
	if found && entry.ThroughEvent == snapshot.State.LastEventID {
		return NextExportReport{ExportReport: ExportReport{Adapter: entry.Adapter, TargetClient: target, TaskID: taskID, ProjectID: snapshot.Project.ID, ThroughEvent: entry.ThroughEvent, PackageID: manifest.PackageID}, OutputDir: entry.OutputDir, FromEventExclusive: manifest.FromEventExclusive, Reused: true}, nil
	}
	afterEvent := int64(0)
	if found {
		afterEvent = entry.ThroughEvent
	}
	binding, err := s.singleAvailableBinding(ctx, snapshot.Project.ID)
	if err != nil {
		return NextExportReport{}, err
	}
	outputDir, relativeOutput, err := s.nextOutputDir(taskID, target)
	if err != nil {
		return NextExportReport{}, err
	}
	report, err := s.Export(ctx, ExportOptions{TaskID: taskID, ClientID: target, Adapter: adapter, DeviceID: binding.DeviceID, AfterEventID: afterEvent, OutputDir: outputDir})
	if err != nil {
		return NextExportReport{}, err
	}
	manifest, err = VerifyPackage(outputDir)
	if err != nil {
		return NextExportReport{}, fmt.Errorf("%w: generated package verification failed: %v", ErrHandoffOutcomeUnknown, err)
	}
	entry = HandoffHistoryEntry{TaskID: report.TaskID, TargetClient: report.TargetClient, Adapter: report.Adapter, PackageID: report.PackageID, ThroughEvent: report.ThroughEvent, OutputDir: relativeOutput, CreatedAt: s.now()}
	if err := s.History.Append(ctx, entry); err != nil {
		return NextExportReport{}, fmt.Errorf("%w: package published but history was not recorded: %v", ErrHandoffOutcomeUnknown, err)
	}
	return NextExportReport{ExportReport: report, OutputDir: relativeOutput, FromEventExclusive: afterEvent}, nil
}

func (s *Service) ListHistory(ctx context.Context, taskID string) ([]HandoffHistoryRecord, error) {
	if s.History == nil || !protocol.IsValidID(taskID) {
		return nil, fmt.Errorf("handoff history is not configured")
	}
	entries, err := s.History.List(ctx, taskID)
	if err != nil {
		return nil, err
	}
	records := make([]HandoffHistoryRecord, 0, len(entries))
	for _, entry := range entries {
		record := HandoffHistoryRecord{HandoffHistoryEntry: entry, Valid: true}
		path, err := resolveRuntimeHandoffPath(s.WorkspaceRoot, entry.OutputDir)
		if err != nil {
			record.Valid, record.Reason = false, err.Error()
		} else if manifest, err := VerifyPackage(path); err != nil {
			record.Valid, record.Reason = false, err.Error()
		} else if manifest.PackageID != entry.PackageID || manifest.TaskID != entry.TaskID || manifest.TargetData.ID != entry.TargetClient || manifest.Adapter != entry.Adapter || manifest.ThroughEvent != entry.ThroughEvent {
			record.Valid, record.Reason = false, "package does not match handoff history"
		}
		records = append(records, record)
	}
	return records, nil
}

func (s *Service) latestVerifiedHistory(ctx context.Context, taskID, target string) (HandoffHistoryEntry, Manifest, bool, error) {
	entries, err := s.History.List(ctx, taskID)
	if err != nil {
		return HandoffHistoryEntry{}, Manifest{}, false, err
	}
	var latest HandoffHistoryEntry
	var latestManifest Manifest
	found := false
	for _, entry := range entries {
		if entry.TargetClient != target {
			continue
		}
		path, err := resolveRuntimeHandoffPath(s.WorkspaceRoot, entry.OutputDir)
		if err != nil {
			return HandoffHistoryEntry{}, Manifest{}, false, err
		}
		manifest, err := VerifyPackage(path)
		if err != nil {
			return HandoffHistoryEntry{}, Manifest{}, false, fmt.Errorf("handoff history package %q cannot be verified: %w", entry.OutputDir, err)
		}
		if manifest.PackageID != entry.PackageID || manifest.TaskID != entry.TaskID || manifest.TargetData.ID != entry.TargetClient || manifest.Adapter != entry.Adapter || manifest.ThroughEvent != entry.ThroughEvent {
			return HandoffHistoryEntry{}, Manifest{}, false, fmt.Errorf("handoff history package %q does not match its record", entry.OutputDir)
		}
		if !found || entry.ThroughEvent > latest.ThroughEvent || (entry.ThroughEvent == latest.ThroughEvent && entry.CreatedAt.After(latest.CreatedAt)) {
			latest, latestManifest, found = entry, manifest, true
		}
	}
	return latest, latestManifest, found, nil
}

func (s *Service) singleAvailableBinding(ctx context.Context, projectID string) (store.ProjectBinding, error) {
	bindings, err := s.Bindings.ListBindings(ctx, projectID)
	if err != nil {
		return store.ProjectBinding{}, err
	}
	available := make([]store.ProjectBinding, 0, len(bindings))
	for _, binding := range bindings {
		if s.Bindings.BindingAvailable(ctx, binding.DeviceID, projectID) {
			available = append(available, binding)
		}
	}
	if len(available) == 0 {
		return store.ProjectBinding{}, fmt.Errorf("%w: no available binding for project %q", store.ErrBindingUnavailable, projectID)
	}
	if len(available) != 1 {
		return store.ProjectBinding{}, fmt.Errorf("automatic handoff requires exactly one available binding for project %q", projectID)
	}
	return available[0], nil
}

func (s *Service) nextOutputDir(taskID, target string) (string, string, error) {
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return "", "", err
	}
	name := s.now().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(nonce)
	relative := filepath.Join("collaboration", ".runtime", "handoffs", taskID, target, name)
	return filepath.Join(s.WorkspaceRoot, relative), filepath.ToSlash(relative), nil
}

func (s *Service) now() time.Time {
	if s.Clock == nil {
		return time.Now().UTC()
	}
	return s.Clock().UTC()
}

func nextTarget(task protocol.Task, state protocol.State) string {
	if state.Status == protocol.Blocked {
		return task.Creator
	}
	return state.ResponsibleClient
}

func adapterForTarget(clientID string) string {
	switch clientID {
	case "codex":
		return "manual-codex"
	case "cc-haha":
		return "manual-cc-haha"
	case "reasonix":
		return "manual-reasonix"
	default:
		return ""
	}
}

func (s *Service) validatePortableSnapshot(ctx context.Context, snapshot store.TaskSnapshot, binding store.ProjectBinding, target protocol.Client) error {
	for _, source := range []struct {
		source string
		value  string
	}{
		{"task_id " + snapshot.Task.ID + " title", snapshot.Task.Title},
		{"task_id " + snapshot.Task.ID + " objective", snapshot.Task.Objective},
		{"project_id " + snapshot.Project.ID + " name", snapshot.Project.Name},
		{"client_id " + target.ID + " name", target.Name},
		{"project revision", binding.Revision},
	} {
		if err := protocol.ValidatePortableText(source.source, source.value); err != nil {
			return portableScanError(source.source)
		}
	}
	for index, criterion := range snapshot.Task.Acceptance {
		source := fmt.Sprintf("task_id %s acceptance %d", snapshot.Task.ID, index+1)
		if err := protocol.ValidatePortableText(source, criterion); err != nil {
			return portableScanError(source)
		}
	}
	seenClients := map[string]bool{}
	for _, clientID := range []string{snapshot.Task.Creator, snapshot.Task.Reviewer, target.ID} {
		if seenClients[clientID] {
			continue
		}
		seenClients[clientID] = true
		client, err := s.Registry.ReadClient(ctx, clientID)
		if err != nil {
			return err
		}
		if err := protocol.ValidatePortableText("client name", client.Name); err != nil {
			return portableScanError("client_id " + clientID + " name")
		}
	}
	for _, event := range snapshot.Events {
		if event.Body != "" {
			if err := protocol.ValidatePortableText("event body", event.Body); err != nil {
				return portableScanError(fmt.Sprintf("event_id %d", event.EventID))
			}
		}
	}
	for _, evidence := range snapshot.Evidence {
		if err := protocol.ValidatePortableText("evidence summary", evidence.Summary); err != nil {
			return portableScanError("evidence_id " + evidence.ID)
		}
		for _, ref := range evidence.FileRefs {
			if err := protocol.ValidatePortableFileRef(ref); err != nil {
				return portableScanError("evidence_id " + evidence.ID)
			}
		}
	}
	return nil
}

func portableScanError(source string) error {
	return fmt.Errorf("handoff safety scan rejected %s", source)
}

func (s *Service) bindingView(ctx context.Context, binding store.ProjectBinding, target protocol.Client, evidence []protocol.Evidence, actionActor string) (BindingView, error) {
	view := BindingView{DeviceID: binding.DeviceID, ProjectID: binding.ProjectID, Revision: binding.Revision, TargetClient: target.ID, TargetClientName: target.Name, ActionActor: actionActor, Evidence: make([]BoundEvidence, 0, len(evidence))}
	for _, value := range evidence {
		bound := BoundEvidence{Evidence: value, Files: make([]store.ResolvedFileRef, 0, len(value.FileRefs))}
		for _, ref := range value.FileRefs {
			resolved, err := s.Resolver.Resolve(ctx, binding, ref)
			if err != nil {
				return BindingView{}, fmt.Errorf("resolve evidence_id %s: %w", value.ID, err)
			}
			bound.Files = append(bound.Files, resolved)
		}
		view.Evidence = append(view.Evidence, bound)
	}
	return view, nil
}

func adapterFor(name string) (ClientAdapter, error) {
	switch name {
	case "manual-codex":
		return ManualCodexAdapter{}, nil
	case "manual-cc-haha":
		return ManualCCHahaAdapter{}, nil
	case "manual-reasonix":
		return ManualReasonixAdapter{}, nil
	default:
		return nil, fmt.Errorf("unsupported handoff adapter")
	}
}

type ManualCodexAdapter struct{}

func (ManualCodexAdapter) Name() string { return "manual-codex" }

func (adapter ManualCodexAdapter) Export(ctx context.Context, snapshot store.TaskSnapshot, binding BindingView) (DeliveryPackage, error) {
	if binding.TargetClient != snapshot.Task.Creator && binding.TargetClient != snapshot.Task.Reviewer {
		return DeliveryPackage{}, fmt.Errorf("manual-codex target is not the task creator or reviewer")
	}
	if snapshot.State.Status == protocol.Blocked {
		if binding.TargetClient != snapshot.Task.Creator || !containsAction(snapshot.AllowedActions, protocol.Assign) {
			return DeliveryPackage{}, fmt.Errorf("manual-codex BLOCKED handoff requires creator assign permission")
		}
	} else if binding.TargetClient != snapshot.State.ResponsibleClient {
		return DeliveryPackage{}, fmt.Errorf("manual-codex target is not currently responsible")
	}
	role := "reviewer"
	if snapshot.State.Status == protocol.Blocked {
		role = "creator"
	}
	return buildPackage(ctx, adapter.Name(), snapshot, binding, role)
}

type ManualCCHahaAdapter struct{}

func (ManualCCHahaAdapter) Name() string { return "manual-cc-haha" }

func (adapter ManualCCHahaAdapter) Export(ctx context.Context, snapshot store.TaskSnapshot, binding BindingView) (DeliveryPackage, error) {
	if binding.TargetClient != snapshot.State.AssignedClient || binding.TargetClient != snapshot.State.ResponsibleClient {
		return DeliveryPackage{}, fmt.Errorf("manual-cc-haha target is not the assigned responsible executor")
	}
	if len(snapshot.AllowedActions) == 0 {
		return DeliveryPackage{}, fmt.Errorf("manual-cc-haha target has no permitted action")
	}
	return buildPackage(ctx, adapter.Name(), snapshot, binding, "executor")
}

type ManualReasonixAdapter struct{}

func (ManualReasonixAdapter) Name() string { return "manual-reasonix" }

func (adapter ManualReasonixAdapter) Export(ctx context.Context, snapshot store.TaskSnapshot, binding BindingView) (DeliveryPackage, error) {
	if binding.TargetClient != snapshot.State.ResponsibleClient {
		return DeliveryPackage{}, fmt.Errorf("manual-reasonix target is not currently responsible")
	}
	if snapshot.State.Status != protocol.Review {
		return DeliveryPackage{}, fmt.Errorf("manual-reasonix handoff requires REVIEW status")
	}
	return buildPackage(ctx, adapter.Name(), snapshot, binding, "reviewer")
}

type ManifestTask struct {
	Title      string   `json:"title"`
	Objective  string   `json:"objective"`
	Acceptance []string `json:"acceptance"`
}

type ManifestTarget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type Manifest struct {
	FormatVersion      string             `json:"format_version"`
	PackageID          string             `json:"package_id"`
	Adapter            string             `json:"adapter"`
	TargetData         ManifestTarget     `json:"target"`
	ActionActor        string             `json:"action_actor"`
	TaskID             string             `json:"task_id"`
	TaskData           ManifestTask       `json:"task"`
	ProjectID          string             `json:"project_id"`
	ProjectRevision    string             `json:"project_revision,omitempty"`
	Status             protocol.Status    `json:"status"`
	Version            int64              `json:"version"`
	FromEventExclusive int64              `json:"from_event_exclusive"`
	ThroughEvent       int64              `json:"through_event"`
	ResponsibleClient  string             `json:"responsible_client"`
	AllowedActions     []protocol.Action  `json:"allowed_actions"`
	Events             []protocol.Event   `json:"events"`
	Evidence           []ManifestEvidence `json:"evidence"`
}

type ManifestEvidence struct {
	ID        string                  `json:"id"`
	Kind      protocol.EvidenceKind   `json:"kind"`
	Summary   string                  `json:"summary"`
	CreatedBy string                  `json:"created_by"`
	CreatedAt time.Time               `json:"created_at"`
	Files     []store.ResolvedFileRef `json:"files"`
}

type manifestIdentity struct {
	FormatVersion      string             `json:"format_version"`
	Adapter            string             `json:"adapter"`
	TargetData         ManifestTarget     `json:"target"`
	ActionActor        string             `json:"action_actor"`
	TaskID             string             `json:"task_id"`
	TaskData           ManifestTask       `json:"task"`
	ProjectID          string             `json:"project_id"`
	ProjectRevision    string             `json:"project_revision,omitempty"`
	Status             protocol.Status    `json:"status"`
	Version            int64              `json:"version"`
	FromEventExclusive int64              `json:"from_event_exclusive"`
	ThroughEvent       int64              `json:"through_event"`
	ResponsibleClient  string             `json:"responsible_client"`
	AllowedActions     []protocol.Action  `json:"allowed_actions"`
	Events             []protocol.Event   `json:"events"`
	Evidence           []ManifestEvidence `json:"evidence"`
}

func (m Manifest) CanonicalPayload() ([]byte, error) {
	return json.Marshal(manifestIdentity{
		FormatVersion:      m.FormatVersion,
		Adapter:            m.Adapter,
		TargetData:         m.TargetData,
		ActionActor:        m.ActionActor,
		TaskID:             m.TaskID,
		TaskData:           m.TaskData,
		ProjectID:          m.ProjectID,
		ProjectRevision:    m.ProjectRevision,
		Status:             m.Status,
		Version:            m.Version,
		FromEventExclusive: m.FromEventExclusive,
		ThroughEvent:       m.ThroughEvent,
		ResponsibleClient:  m.ResponsibleClient,
		AllowedActions:     m.AllowedActions,
		Events:             m.Events,
		Evidence:           m.Evidence,
	})
}

func (m Manifest) ComputedPackageID() (string, error) {
	payload, err := m.CanonicalPayload()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (m Manifest) Validate() error {
	if m.FormatVersion != "1" || (m.Adapter != "manual-codex" && m.Adapter != "manual-cc-haha" && m.Adapter != "manual-reasonix") || !protocol.IsValidID(m.ActionActor) || !protocol.IsValidID(m.TaskID) || !protocol.IsValidID(m.ProjectID) || !protocol.IsValidID(m.ResponsibleClient) || protocol.ValidatePortableText("project revision", m.ProjectRevision) != nil {
		return fmt.Errorf("invalid handoff manifest identity")
	}
	if err := m.TargetData.Validate(); err != nil {
		return err
	}
	if err := m.TaskData.Validate(); err != nil {
		return err
	}
	if m.ActionActor != m.TargetData.ID || m.Version < 1 || m.FromEventExclusive < 0 || m.ThroughEvent < m.FromEventExclusive || !knownStatus(m.Status) {
		return fmt.Errorf("invalid handoff manifest state")
	}
	if !strings.HasPrefix(m.PackageID, "sha256:") || len(m.PackageID) != len("sha256:")+64 {
		return fmt.Errorf("invalid package_id")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(m.PackageID, "sha256:")); err != nil {
		return fmt.Errorf("invalid package_id")
	}
	seenActions := map[protocol.Action]bool{}
	for _, action := range m.AllowedActions {
		if !knownAction(action) || seenActions[action] {
			return fmt.Errorf("invalid allowed action")
		}
		seenActions[action] = true
	}
	nextEventID := m.FromEventExclusive + 1
	for _, event := range m.Events {
		if err := event.Validate(m.TaskID); err != nil || event.EventID != nextEventID || event.EventID > m.ThroughEvent {
			return fmt.Errorf("invalid manifest event")
		}
		nextEventID++
	}
	if nextEventID != m.ThroughEvent+1 {
		return fmt.Errorf("manifest event range is incomplete")
	}
	seenEvidence := map[string]bool{}
	for _, evidence := range m.Evidence {
		if !protocol.IsValidID(evidence.ID) || seenEvidence[evidence.ID] || !isEvidenceKind(evidence.Kind) || !protocol.IsValidID(evidence.CreatedBy) || protocol.ValidatePortableText("evidence summary", evidence.Summary) != nil || evidence.CreatedAt.IsZero() || evidence.CreatedAt.Location() != time.UTC {
			return fmt.Errorf("invalid manifest evidence")
		}
		seenEvidence[evidence.ID] = true
		for _, file := range evidence.Files {
			if err := validateResolvedFile(file); err != nil {
				return err
			}
		}
	}
	expected, err := m.ComputedPackageID()
	if err != nil || m.PackageID != expected {
		return fmt.Errorf("package_id does not match manifest")
	}
	return nil
}

func (task ManifestTask) Validate() error {
	if strings.TrimSpace(task.Title) == "" || strings.TrimSpace(task.Objective) == "" || len(task.Acceptance) == 0 {
		return fmt.Errorf("invalid manifest task")
	}
	for _, value := range []struct {
		field string
		text  string
	}{
		{"task title", task.Title},
		{"task objective", task.Objective},
	} {
		if err := protocol.ValidatePortableText(value.field, value.text); err != nil {
			return fmt.Errorf("invalid manifest task")
		}
	}
	for _, criterion := range task.Acceptance {
		if strings.TrimSpace(criterion) == "" || protocol.ValidatePortableText("task acceptance", criterion) != nil {
			return fmt.Errorf("invalid manifest task")
		}
	}
	return nil
}

func (target ManifestTarget) Validate() error {
	if !protocol.IsValidID(target.ID) || strings.TrimSpace(target.Name) == "" || protocol.ValidatePortableText("target name", target.Name) != nil || !knownTargetRole(target.Role) {
		return fmt.Errorf("invalid manifest target")
	}
	return nil
}

func knownTargetRole(role string) bool {
	switch role {
	case "creator", "executor", "reviewer":
		return true
	default:
		return false
	}
}

func DecodeManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Manifest{}, fmt.Errorf("manifest contains multiple JSON values")
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func buildPackage(_ context.Context, adapter string, snapshot store.TaskSnapshot, binding BindingView, role string) (DeliveryPackage, error) {
	if snapshot.ActionActor != binding.TargetClient || binding.ActionActor != binding.TargetClient {
		return DeliveryPackage{}, fmt.Errorf("handoff action actor does not match target")
	}
	manifest := Manifest{
		FormatVersion:      "1",
		Adapter:            adapter,
		TargetData:         ManifestTarget{ID: binding.TargetClient, Name: binding.TargetClientName, Role: role},
		ActionActor:        binding.ActionActor,
		TaskID:             snapshot.Task.ID,
		TaskData:           ManifestTask{Title: snapshot.Task.Title, Objective: snapshot.Task.Objective, Acceptance: append([]string(nil), snapshot.Task.Acceptance...)},
		ProjectID:          snapshot.Project.ID,
		ProjectRevision:    binding.Revision,
		Status:             snapshot.State.Status,
		Version:            snapshot.State.Version,
		FromEventExclusive: snapshot.FromEvent,
		ThroughEvent:       snapshot.ThroughEvent,
		ResponsibleClient:  snapshot.State.ResponsibleClient,
		AllowedActions:     append([]protocol.Action(nil), snapshot.AllowedActions...),
		Events:             append([]protocol.Event(nil), snapshot.Events...),
		Evidence:           make([]ManifestEvidence, 0, len(binding.Evidence)),
	}
	for _, value := range binding.Evidence {
		manifest.Evidence = append(manifest.Evidence, ManifestEvidence{ID: value.Evidence.ID, Kind: value.Evidence.Kind, Summary: value.Evidence.Summary, CreatedBy: value.Evidence.CreatedBy, CreatedAt: value.Evidence.CreatedAt, Files: append([]store.ResolvedFileRef(nil), value.Files...)})
	}
	packageID, err := manifest.ComputedPackageID()
	if err != nil {
		return DeliveryPackage{}, err
	}
	manifest.PackageID = packageID
	if err := manifest.Validate(); err != nil {
		return DeliveryPackage{}, err
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return DeliveryPackage{}, err
	}
	candidateData, err := marshalCandidateResponse(NewCandidateResponse(manifest))
	if err != nil {
		return DeliveryPackage{}, err
	}
	handoffData, err := renderHandoff(manifest)
	if err != nil {
		return DeliveryPackage{}, err
	}
	return DeliveryPackage{
		Handoff:                 handoffData,
		Manifest:                append(manifestData, '\n'),
		CandidateResponse:       candidateData,
		CandidateResponseSchema: CandidateResponseSchema(),
	}, nil
}

func containsAction(actions []protocol.Action, wanted protocol.Action) bool {
	for _, action := range actions {
		if action == wanted {
			return true
		}
	}
	return false
}

func knownAction(action protocol.Action) bool {
	switch action {
	case protocol.Assign, protocol.Accept, protocol.Submit, protocol.RequestChanges, protocol.Resume, protocol.Approve, protocol.Block, protocol.Message, protocol.AddEvidence:
		return true
	default:
		return false
	}
}

func knownStatus(status protocol.Status) bool {
	switch status {
	case protocol.Draft, protocol.Assigned, protocol.Working, protocol.Review, protocol.RevisionRequired, protocol.Done, protocol.Blocked:
		return true
	default:
		return false
	}
}

func isEvidenceKind(kind protocol.EvidenceKind) bool {
	switch kind {
	case protocol.EvidenceDiff, protocol.EvidenceArtifact, protocol.EvidenceTest, protocol.EvidenceBlocker:
		return true
	default:
		return false
	}
}

func validateResolvedFile(file store.ResolvedFileRef) error {
	if err := protocol.ValidatePortableFileRef(file.RelativeRef); err != nil {
		return err
	}
	if !file.Available {
		if file.Size != 0 || file.SHA256 != "" {
			return fmt.Errorf("unavailable file has hash data")
		}
		return nil
	}
	if file.Size < 0 || len(file.SHA256) != 64 {
		return fmt.Errorf("invalid resolved file")
	}
	if _, err := hex.DecodeString(file.SHA256); err != nil || strings.ToLower(file.SHA256) != file.SHA256 {
		return fmt.Errorf("invalid resolved file hash")
	}
	return nil
}

package handoff

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

type Publisher interface {
	Publish(string, DeliveryPackage) error
}

type Service struct {
	Query     store.TaskQuery
	Bindings  store.BindingStore
	Resolver  store.BindingResolver
	Registry  store.RegistryStore
	Publisher Publisher
}

func NewService(query store.TaskQuery, bindings store.BindingStore, resolver store.BindingResolver, registry store.RegistryStore, workspaceRoot ...string) *Service {
	root := ""
	if len(workspaceRoot) > 0 {
		root = workspaceRoot[0]
	}
	return &Service{
		Query:     query,
		Bindings:  bindings,
		Resolver:  resolver,
		Registry:  registry,
		Publisher: NewDirectoryPublisher(root),
	}
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
	return buildPackage(ctx, adapter.Name(), snapshot, binding, "审查者或创建者", "仅生成候选响应 JSON；操作者审核后手工执行建议的 CLI 命令，不会控制 Codex Desktop。")
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
	return buildPackage(ctx, adapter.Name(), snapshot, binding, "被指派的执行者", "仅生成候选响应 JSON；操作者审核后手工执行建议的 CLI 命令，不会读取或控制 CC-HAHA 的内部会话、技能、MCP 或登录态。")
}

type Manifest struct {
	FormatVersion      string             `json:"format_version"`
	PackageID          string             `json:"package_id"`
	Adapter            string             `json:"adapter"`
	TargetClient       string             `json:"target_client"`
	ActionActor        string             `json:"action_actor"`
	TaskID             string             `json:"task_id"`
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
	TargetClient       string             `json:"target_client"`
	ActionActor        string             `json:"action_actor"`
	TaskID             string             `json:"task_id"`
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
		TargetClient:       m.TargetClient,
		ActionActor:        m.ActionActor,
		TaskID:             m.TaskID,
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
	if m.FormatVersion != "1" || (m.Adapter != "manual-codex" && m.Adapter != "manual-cc-haha") || !protocol.IsValidID(m.TargetClient) || !protocol.IsValidID(m.ActionActor) || !protocol.IsValidID(m.TaskID) || !protocol.IsValidID(m.ProjectID) || !protocol.IsValidID(m.ResponsibleClient) || protocol.ValidatePortableText("project revision", m.ProjectRevision) != nil {
		return fmt.Errorf("invalid handoff manifest identity")
	}
	if m.ActionActor != m.TargetClient || m.Version < 1 || m.FromEventExclusive < 0 || m.ThroughEvent < m.FromEventExclusive || !knownStatus(m.Status) {
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

func buildPackage(_ context.Context, adapter string, snapshot store.TaskSnapshot, binding BindingView, role, outputRequirement string) (DeliveryPackage, error) {
	if snapshot.ActionActor != binding.TargetClient || binding.ActionActor != binding.TargetClient {
		return DeliveryPackage{}, fmt.Errorf("handoff action actor does not match target")
	}
	manifest := Manifest{
		FormatVersion:      "1",
		Adapter:            adapter,
		TargetClient:       binding.TargetClient,
		ActionActor:        binding.ActionActor,
		TaskID:             snapshot.Task.ID,
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
	candidate := NewCandidateResponse(manifest)
	candidateData, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return DeliveryPackage{}, err
	}
	handoffData, err := renderHandoff(manifest, snapshot, binding, role, outputRequirement)
	if err != nil {
		return DeliveryPackage{}, err
	}
	return DeliveryPackage{
		Handoff:                 handoffData,
		Manifest:                append(manifestData, '\n'),
		CandidateResponse:       append(candidateData, '\n'),
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

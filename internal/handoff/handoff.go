package handoff

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	Evidence         []BoundEvidence
}

type BoundEvidence struct {
	Evidence protocol.Evidence
	Files    []store.ResolvedFileRef
}

type DeliveryPackage struct {
	Handoff  []byte
	Manifest []byte
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
	Force        bool
}

type ExportReport struct {
	Adapter      string
	TargetClient string
	TaskID       string
	ProjectID    string
	ThroughEvent int64
}

type Publisher interface {
	Publish(string, DeliveryPackage, bool) error
}

type Service struct {
	Query     store.TaskQuery
	Bindings  store.BindingStore
	Resolver  store.BindingResolver
	Registry  store.RegistryStore
	Publisher Publisher
}

func NewService(query store.TaskQuery, bindings store.BindingStore, resolver store.BindingResolver, registry store.RegistryStore) *Service {
	return &Service{Query: query, Bindings: bindings, Resolver: resolver, Registry: registry, Publisher: DirectoryPublisher{}}
}

func (s *Service) Export(ctx context.Context, options ExportOptions) (ExportReport, error) {
	if s.Query == nil || s.Bindings == nil || s.Resolver == nil || s.Registry == nil || s.Publisher == nil {
		return ExportReport{}, fmt.Errorf("handoff service is not configured")
	}
	if !protocol.IsValidID(options.TaskID) || !protocol.IsValidID(options.ClientID) || !protocol.IsValidID(options.DeviceID) || options.AfterEventID < 0 || strings.TrimSpace(options.OutputDir) == "" {
		return ExportReport{}, fmt.Errorf("invalid handoff export options")
	}
	snapshot, err := s.Query.Snapshot(ctx, options.TaskID, options.AfterEventID)
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
	binding, err := s.Bindings.ReadBinding(ctx, options.DeviceID, snapshot.Project.ID)
	if err != nil {
		return ExportReport{}, err
	}
	if !s.Bindings.BindingAvailable(ctx, options.DeviceID, snapshot.Project.ID) {
		return ExportReport{}, fmt.Errorf("%w: project %q on device %q", store.ErrBindingUnavailable, snapshot.Project.ID, options.DeviceID)
	}
	target, err := s.Registry.ReadClient(ctx, options.ClientID)
	if err != nil {
		return ExportReport{}, err
	}
	if err := s.validatePortableSnapshot(ctx, snapshot, binding, target); err != nil {
		return ExportReport{}, err
	}
	view, err := s.bindingView(ctx, binding, target, snapshot.Evidence)
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
	if err := s.Publisher.Publish(options.OutputDir, packageData, options.Force); err != nil {
		return ExportReport{}, err
	}
	return ExportReport{Adapter: adapter.Name(), TargetClient: target.ID, TaskID: snapshot.Task.ID, ProjectID: snapshot.Project.ID, ThroughEvent: snapshot.ThroughEvent}, nil
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

func (s *Service) bindingView(ctx context.Context, binding store.ProjectBinding, target protocol.Client, evidence []protocol.Evidence) (BindingView, error) {
	view := BindingView{DeviceID: binding.DeviceID, ProjectID: binding.ProjectID, Revision: binding.Revision, TargetClient: target.ID, TargetClientName: target.Name, Evidence: make([]BoundEvidence, 0, len(evidence))}
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
	if binding.TargetClient != snapshot.State.ResponsibleClient {
		return DeliveryPackage{}, fmt.Errorf("manual-codex target is not currently responsible")
	}
	return buildPackage(ctx, adapter.Name(), snapshot, binding, "审查者或创建者", "审查证据、测试与变更摘要后，使用建议命令回写审查结论；不会控制 Codex Desktop。")
}

type ManualCCHahaAdapter struct{}

func (ManualCCHahaAdapter) Name() string { return "manual-cc-haha" }

func (adapter ManualCCHahaAdapter) Export(ctx context.Context, snapshot store.TaskSnapshot, binding BindingView) (DeliveryPackage, error) {
	if binding.TargetClient != snapshot.State.AssignedClient {
		return DeliveryPackage{}, fmt.Errorf("manual-cc-haha target is not the assigned executor")
	}
	if binding.TargetClient != snapshot.State.ResponsibleClient {
		return DeliveryPackage{}, fmt.Errorf("manual-cc-haha target is not currently responsible")
	}
	return buildPackage(ctx, adapter.Name(), snapshot, binding, "被指派的执行者", "完成工作后通过 CLI 回写消息、Evidence、提交或阻塞；不会读取或控制 CC-HAHA 的内部会话、技能、MCP 或登录态。")
}

type Manifest struct {
	FormatVersion      string             `json:"format_version"`
	Adapter            string             `json:"adapter"`
	TargetClient       string             `json:"target_client"`
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

func buildPackage(_ context.Context, adapter string, snapshot store.TaskSnapshot, binding BindingView, role, outputRequirement string) (DeliveryPackage, error) {
	manifest := Manifest{
		FormatVersion:      "1",
		Adapter:            adapter,
		TargetClient:       binding.TargetClient,
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
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return DeliveryPackage{}, err
	}
	manifestData = append(manifestData, '\n')
	return DeliveryPackage{Manifest: manifestData, Handoff: renderHandoff(manifest, snapshot, binding, role, outputRequirement)}, nil
}

func renderHandoff(manifest Manifest, snapshot store.TaskSnapshot, binding BindingView, role, outputRequirement string) []byte {
	var output bytes.Buffer
	fmt.Fprintln(&output, "# 协作交接包")
	fmt.Fprintln(&output, "\n## 协议与安全边界")
	fmt.Fprintln(&output, "本包仅包含可迁移的任务、事件与证据索引。它不包含本机绝对路径、PID、PTY、会话标识、登录态或凭据，也不会控制任何客户端。")
	fmt.Fprintln(&output, "\n## 目标客户端")
	fmt.Fprintf(&output, "- 客户端：`%s`（%s）\n- 适配器：`%s`\n- 角色：%s\n", binding.TargetClient, binding.TargetClientName, manifest.Adapter, role)
	fmt.Fprintln(&output, "\n## 任务目标")
	fmt.Fprintf(&output, "- 标题：%s\n- 目标：%s\n", snapshot.Task.Title, snapshot.Task.Objective)
	fmt.Fprintln(&output, "\n## 验收标准")
	for _, criterion := range snapshot.Task.Acceptance {
		fmt.Fprintf(&output, "- %s\n", criterion)
	}
	fmt.Fprintln(&output, "\n## 当前状态与版本")
	fmt.Fprintf(&output, "- 状态：`%s`\n- 版本：%d\n- 事件范围：(%d, %d]\n", manifest.Status, manifest.Version, manifest.FromEventExclusive, manifest.ThroughEvent)
	fmt.Fprintln(&output, "\n## 当前责任方")
	fmt.Fprintf(&output, "- `%s`\n", manifest.ResponsibleClient)
	fmt.Fprintln(&output, "\n## 自上次游标后的事件")
	if len(manifest.Events) == 0 {
		fmt.Fprintln(&output, "- 无新增事件。")
	}
	for _, event := range manifest.Events {
		fmt.Fprintf(&output, "- #%d `%s`，actor=`%s`，at=`%s`", event.EventID, event.Type, event.Actor, event.At.UTC().Format(time.RFC3339Nano))
		if event.TargetClient != "" {
			fmt.Fprintf(&output, "，target=`%s`", event.TargetClient)
		}
		if event.Body != "" {
			fmt.Fprintf(&output, "，body=%q", event.Body)
		}
		if len(event.EvidenceRefs) > 0 {
			fmt.Fprintf(&output, "，evidence=`%s`", strings.Join(event.EvidenceRefs, ","))
		}
		fmt.Fprintln(&output)
	}
	fmt.Fprintln(&output, "\n## Evidence 索引")
	if len(manifest.Evidence) == 0 {
		fmt.Fprintln(&output, "- 无已公告 Evidence。")
	}
	for _, evidence := range manifest.Evidence {
		fmt.Fprintf(&output, "- `%s`（%s）：%s\n", evidence.ID, evidence.Kind, evidence.Summary)
	}
	fmt.Fprintln(&output, "\n## 项目相对文件与校验值")
	for _, evidence := range manifest.Evidence {
		for _, file := range evidence.Files {
			if file.Available {
				fmt.Fprintf(&output, "- `%s`：size=%d，sha256=%s\n", file.RelativeRef, file.Size, file.SHA256)
			} else {
				fmt.Fprintf(&output, "- `%s`：unavailable\n", file.RelativeRef)
			}
		}
	}
	fmt.Fprintln(&output, "\n## 当前允许动作")
	if len(manifest.AllowedActions) == 0 {
		fmt.Fprintln(&output, "- 无。")
	}
	for _, action := range manifest.AllowedActions {
		fmt.Fprintf(&output, "- `%s`\n", action)
	}
	fmt.Fprintln(&output, "\n## 建议的 CLI 回写命令")
	for _, command := range commandsFor(snapshot, binding.TargetClient) {
		fmt.Fprintf(&output, "- `%s`\n", command)
	}
	fmt.Fprintln(&output, "\n## 客户端输出要求")
	fmt.Fprintln(&output, outputRequirement)
	return output.Bytes()
}

func commandsFor(snapshot store.TaskSnapshot, clientID string) []string {
	commands := make([]string, 0, len(snapshot.AllowedActions))
	for _, action := range snapshot.AllowedActions {
		switch action {
		case protocol.Assign:
			commands = append(commands, fmt.Sprintf("collab task assign --task %s --client <executor-client> --expected-version %d", snapshot.Task.ID, snapshot.State.Version))
		case protocol.Accept:
			commands = append(commands, fmt.Sprintf("collab task accept --task %s --actor %s --expected-version %d", snapshot.Task.ID, clientID, snapshot.State.Version))
		case protocol.Message:
			commands = append(commands, fmt.Sprintf("collab message add --task %s --actor %s --body <message> --expected-version %d", snapshot.Task.ID, clientID, snapshot.State.Version))
		case protocol.AddEvidence:
			commands = append(commands, fmt.Sprintf("collab evidence add --task %s --id <evidence-id> --kind <diff|artifact|test|blocker> --summary <summary> --created-by %s --file-ref <project-relative-path> --expected-version %d", snapshot.Task.ID, clientID, snapshot.State.Version))
		case protocol.Submit:
			commands = append(commands, fmt.Sprintf("collab task submit --task %s --actor %s --evidence <diff-or-artifact-id> --evidence <test-id> --expected-version %d", snapshot.Task.ID, clientID, snapshot.State.Version))
		case protocol.RequestChanges:
			commands = append(commands, fmt.Sprintf("collab review request-changes --task %s --actor %s --body <feedback> --expected-version %d", snapshot.Task.ID, clientID, snapshot.State.Version))
		case protocol.Resume:
			commands = append(commands, fmt.Sprintf("collab task resume --task %s --actor %s --expected-version %d", snapshot.Task.ID, clientID, snapshot.State.Version))
		case protocol.Approve:
			commands = append(commands, fmt.Sprintf("collab review approve --task %s --actor %s --expected-version %d", snapshot.Task.ID, clientID, snapshot.State.Version))
		case protocol.Block:
			commands = append(commands, fmt.Sprintf("collab task block --task %s --actor %s --evidence <blocker-evidence-id> --expected-version %d", snapshot.Task.ID, clientID, snapshot.State.Version))
		}
	}
	return commands
}

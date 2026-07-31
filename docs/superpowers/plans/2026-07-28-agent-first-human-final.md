# Agent-first、人工终审协作模式 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Codex 与 CC-HAHA 通过本机结构化提交自动登记非终态协作事实，而网页控制台只要求人类处理最终审查、阻塞处置和异常候选。

**Architecture:** 在不改变 Protocol v1 状态机的前提下，给项目增加可审计的协作策略；新增本地 Agent Intake 服务，将交接包候选响应先验证、留档，再以带来源与策略决定的事件安全写入任务账本。交接包导出从任务上下文、Binding 和历史游标自动推导；网页改为“待我审查 / 协作活动 / 交接历史”三视图，所有日常动作不再要求填写 actor、版本、设备或输出目录。

**Tech Stack:** Go 标准库、现有 YAML/JSON 文件存储、嵌入式原生 HTML/CSS/JavaScript、`go test`、`go vet`、`gofmt`。

---

## 先决边界

- 当前工作树已有用户确认的网页控制台与 `collaboration/` 配置，直接在当前仓库继续；本计划不创建隔离 worktree，不重置、不覆盖现有未提交改动。
- 本阶段只支持同一台受信任设备上的显式本地命令。它不是远程身份认证，也不能阻止一个拥有本机文件权限的进程伪装为人工 CLI；这项安全边界必须在文档和界面中写明。
- 不加入 AO、sidecar、ConPTY、客户端启动/控制、浏览器自动化、轮询、远程 HTTP、自动 `DONE`、自动 push/merge/deploy。
- 不提交、不推送。本轮只产生可验证的工作树改动。

## 文件结构与责任分配

| 路径 | 责任 |
| --- | --- |
| `internal/protocol/model.go` | 项目协作策略、策略变更审计记录以及项目默认策略。 |
| `internal/protocol/event.go` | 为已登记的自动/人工事件保存来源、策略决定和提交 ID。 |
| `internal/protocol/policy.go` | 保留通用状态授权，并增加“Agent 在当前项目策略下是否可自动登记”的纯函数。 |
| `internal/protocol/*_test.go` | 协议兼容性、默认值、非法策略、事件来源字段和 `human_final` 限制。 |
| `internal/store/registry.go` | 原子更新项目策略，并在同一 YAML 写入中追加审计记录。 |
| `internal/store/journal.go` | 在单个 task lock 内应用已经验证的 Agent 候选，并带审计来源写事件。 |
| `internal/store/agent_submission.go` | `TaskJournal` 的 Agent 批量提交输入/结果接口，避免 CLI 按命令字符串逐条回放。 |
| `internal/store/*_test.go` | 策略版本冲突、Agent 事件账本、无效候选不改状态、写入恢复语义。 |
| `internal/agentintake/model.go` | 任务创建候选、候选收据、状态和异常视图的数据模型。 |
| `internal/agentintake/store.go` | 在 `collaboration/.runtime/agent-submissions/` 中保存原始候选、校验决定和结果。 |
| `internal/agentintake/service.go` | 复用 handoff 校验、检查实时版本/策略/能力，并调用 Journal 原子登记。 |
| `internal/agentintake/*_test.go` | 任务创建、CC-HAHA 提交至 REVIEW、版本冲突、越权批准、格式失败和收据可查看。 |
| `internal/handoff/history.go` | 本机交接历史、默认游标、自动输出目录和自动导出上下文。 |
| `internal/handoff/handoff.go` | 根据任务职责、唯一有效 Binding 和历史记录生成下一份包。 |
| `internal/handoff/publish.go` | 仅允许程序生成的 `.runtime/handoffs/` 输出目录，保留其余协作数据目录保护。 |
| `internal/handoff/*_test.go` | 首包 `after_event=0`、后续游标、Binding 歧义、输出目录限制与包历史。 |
| `internal/cli/app.go` | 装配 Intake、历史和默认本机上下文。 |
| `internal/cli/commands.go` | `agent task-create`、`agent submit`、`handoff next`、人类审查/阻塞处理与策略修改命令。 |
| `internal/cli/output.go` | 结构化输出自动提交收据、自动交接上下文和人类审查结果。 |
| `internal/cli/console.go` | 将审查队列、活动、异常和交接历史转换为网页 DTO；从任务快照推导人类动作。 |
| `internal/webconsole/model.go` | 三视图所需的只读工作台、异常、策略和自动交接 DTO。 |
| `internal/webconsole/server.go` | 仅暴露人类最终审查、策略修改、阻塞重分配和自动导出端点；删除日常自由表单写入路径。 |
| `internal/webconsole/assets/index.html` | 三项日常导航、审查工作区、异常条目和交接历史结构。 |
| `internal/webconsole/assets/app.js` | 不发送 actor/version/device/output 的上下文操作和确认对话框。 |
| `internal/webconsole/assets/style.css` | 延续现有 Technical Ledger 视觉 token，收敛为桌面审查工作台。 |
| `docs/specs/protocol-v1.md` | 记录策略、事件来源与向后兼容的 Protocol v1 增补。 |
| `docs/specs/cli-v1.md` | 记录新本地 Agent 与人工命令、输入格式、失败代码和信任边界。 |
| `docs/pilots/manual-client-pilot.md` | 将真实试点改为 Agent 自动登记、人工最终审查的运行步骤。 |

### Task 1: 项目协作策略与策略审计

**Files:**
- Modify: `internal/protocol/model.go`
- Modify: `internal/protocol/yaml.go`
- Modify: `internal/protocol/model_test.go`
- Modify: `internal/store/registry.go`
- Modify: `internal/store/registry_test.go`

- [ ] **Step 1: 写出默认策略和严格 YAML 兼容性的失败测试。**

```go
func TestDecodeProjectDefaultsToAgentAutoHumanFinal(t *testing.T) {
    project, err := DecodeProject([]byte("id: demo\nname: Demo\ncreated_at: 2026-07-28T00:00:00Z\n"), "demo.yaml")
    if err != nil { t.Fatal(err) }
    want := DefaultCollaborationPolicy()
    if project.CollaborationPolicy != want || project.PolicyVersion != 1 { t.Fatalf("got %#v v%d", project.CollaborationPolicy, project.PolicyVersion) }
}

func TestDecodeProjectRejectsAutoDoneWithHumanFinal(t *testing.T) {
    _, err := DecodeProject([]byte("id: demo\nname: Demo\ncreated_at: 2026-07-28T00:00:00Z\ncollaboration_policy:\n  submission_mode: agent_auto\n  final_review: human\n  auto_done: true\n"), "demo.yaml")
    if err == nil { t.Fatal("expected policy validation error") }
}
```

- [ ] **Step 2: 运行新测试并确认当前实现尚不支持。**

Run: `go test ./internal/protocol -run 'TestDecodeProject(Default|Rejects)' -count=1`

Expected: FAIL，提示 `Project` 尚无 `CollaborationPolicy` 或默认值不匹配。

- [ ] **Step 3: 在协议模型中加入最小的策略与审计类型。**

在 `internal/protocol/model.go` 增加以下稳定字段；`Project` 的新增字段必须是 YAML 命名字段，不能把本机路径、PID 或客户端会话写入项目记录。

```go
type SubmissionMode string
const SubmissionModeAgentAuto SubmissionMode = "agent_auto"

type FinalReviewMode string
const (
    FinalReviewHuman FinalReviewMode = "human"
    FinalReviewAgent FinalReviewMode = "agent"
)

type CollaborationPolicy struct {
    SubmissionMode SubmissionMode `yaml:"submission_mode"`
    FinalReview    FinalReviewMode `yaml:"final_review"`
    AutoDone       bool `yaml:"auto_done"`
}

type PolicyAuditEntry struct {
    Version  int64               `yaml:"version"`
    Actor    string              `yaml:"actor"`
    At       time.Time           `yaml:"at"`
    Previous CollaborationPolicy `yaml:"previous"`
    Current  CollaborationPolicy `yaml:"current"`
}

func DefaultCollaborationPolicy() CollaborationPolicy {
    return CollaborationPolicy{SubmissionMode: SubmissionModeAgentAuto, FinalReview: FinalReviewHuman, AutoDone: false}
}
```

扩展 `Project`：

```go
CollaborationPolicy CollaborationPolicy `yaml:"collaboration_policy"`
PolicyVersion       int64               `yaml:"policy_version"`
PolicyHistory       []PolicyAuditEntry  `yaml:"policy_history,omitempty"`
```

在 `DecodeProject` 完成 YAML 解码后调用一个只处理零值兼容性的 `NormalizeProjectPolicy`：旧文件缺少策略时赋予默认策略和版本 `1`；已写出策略但版本为零时拒绝，不要静默重写损坏的新数据。`CollaborationPolicy.Validate` 必须只接受两种规定模式，并要求 `human` 时 `auto_done == false`。

- [ ] **Step 4: 为策略更新写出并运行失败测试。**

```go
func TestFileRegistryStoreUpdateProjectPolicyAppendsAuditedEntry(t *testing.T) {
    store := newRegistryForTest(t)
    createProject(t, store, "demo")
    next := protocol.CollaborationPolicy{SubmissionMode: protocol.SubmissionModeAgentAuto, FinalReview: protocol.FinalReviewAgent, AutoDone: true}
    project, err := store.UpdateProjectPolicy(context.Background(), "demo", 1, "operator", next, fixedTime)
    if err != nil { t.Fatal(err) }
    if project.PolicyVersion != 2 || len(project.PolicyHistory) != 1 { t.Fatalf("got %#v", project) }
    if project.PolicyHistory[0].Previous != protocol.DefaultCollaborationPolicy() || project.PolicyHistory[0].Current != next { t.Fatal("audit mismatch") }
}
```

Run: `go test ./internal/store -run TestFileRegistryStoreUpdateProjectPolicyAppendsAuditedEntry -count=1`

Expected: FAIL，提示 `UpdateProjectPolicy` 不存在。

- [ ] **Step 5: 用项目锁和原子替换实现更新。**

扩展 `RegistryStore` 与 `FileRegistryStore`：

```go
UpdateProjectPolicy(ctx context.Context, id string, expectedVersion int64, actor string, policy protocol.CollaborationPolicy, at time.Time) (protocol.Project, error)
```

实现顺序必须是：校验 ID/actor/time/policy → 获取 `Locks.Projects` → 读取当前 YAML → 比对 `PolicyVersion`（不一致返回新增的 `ErrPolicyVersionConflict`）→ 追加 `PolicyAuditEntry` → 递增版本 → `writeAtomically` 替换同一项目 YAML。不得把政策和审计拆成两个文件或两个非原子写入。

- [ ] **Step 6: 运行协议与 store 相关测试。**

Run: `gofmt -w internal/protocol/model.go internal/protocol/yaml.go internal/protocol/model_test.go internal/store/registry.go internal/store/registry_test.go; go test ./internal/protocol ./internal/store -count=1`

Expected: PASS。

### Task 2: 可审计的事件来源与 Agent 策略授权

**Files:**
- Modify: `internal/protocol/event.go`
- Modify: `internal/protocol/state.go`
- Modify: `internal/protocol/policy.go`
- Modify: `internal/protocol/event_test.go`
- Modify: `internal/protocol/policy_test.go`

- [ ] **Step 1: 写出 Agent 事件元数据与人类终审限制的失败测试。**

```go
func TestAgentProvenanceRequiresSubmissionAndDecision(t *testing.T) {
    event := validEvent()
    event.Origin = EventOriginAgent
    if err := event.Validate(event.TaskID); err == nil { t.Fatal("expected missing provenance error") }
}

func TestAuthorizeAgentRejectsFinalReviewInHumanMode(t *testing.T) {
    err := AuthorizeAgentAction(DefaultCollaborationPolicy(), protocol.Approve)
    if !errors.Is(err, ErrHumanFinalReviewRequired) { t.Fatalf("got %v", err) }
}
```

- [ ] **Step 2: 运行测试确认失败。**

Run: `go test ./internal/protocol -run 'Test(AgentProvenance|AuthorizeAgent)' -count=1`

Expected: FAIL，因为来源字段和 `AuthorizeAgentAction` 尚不存在。

- [ ] **Step 3: 添加只表达审计事实的 provenance 字段。**

在 `event.go` 定义：

```go
type EventOrigin string
const (
    EventOriginAgent EventOrigin = "agent"
    EventOriginHuman EventOrigin = "human"
)
```

并在 `Event` 追加：

```go
Origin         EventOrigin `json:"origin,omitempty"`
SubmissionID   string      `json:"submission_id,omitempty"`
PolicyDecision string      `json:"policy_decision,omitempty"`
```

验证规则：三者全部为空时保持旧事件可读；`agent` 必须同时有合法 `submission_id` 与固定非空的安全文本 `policy_decision`；`human` 只允许 `policy_decision == "human_final"` 或 `"human_operator"`，且不得携带 `submission_id`；未知来源或半填字段一律拒绝。扩展 `TransitionIntent` 增加同名 provenance 字段，以便 journal 创建事件时不依赖 CLI 字符串解析。

- [ ] **Step 4: 添加最小纯函数授权。**

在 `policy.go` 新增：

```go
var ErrHumanFinalReviewRequired = errors.New("human final review is required")

func AuthorizeAgentAction(policy CollaborationPolicy, action Action) error {
    if err := policy.Validate(); err != nil { return err }
    if policy.SubmissionMode != SubmissionModeAgentAuto { return errors.New("agent automatic submission is disabled") }
    if policy.FinalReview == FinalReviewHuman && (action == Approve || action == RequestChanges) {
        return ErrHumanFinalReviewRequired
    }
    if action == Approve && !policy.AutoDone { return errors.New("automatic DONE is disabled") }
    return nil
}
```

这只是策略层；任务职责、客户端 capability、状态和 Evidence 条件继续由现有 `DefaultActionPolicy.Authorize` 与 `Transition` 验证，不能在此复制第二份状态机。

- [ ] **Step 5: 运行格式化和协议测试。**

Run: `gofmt -w internal/protocol/event.go internal/protocol/state.go internal/protocol/policy.go internal/protocol/event_test.go internal/protocol/policy_test.go; go test ./internal/protocol -count=1`

Expected: PASS，旧序列化 event 测试仍通过。

### Task 3: 单 task lock 内登记 Agent 候选

**Files:**
- Create: `internal/store/agent_submission.go`
- Modify: `internal/store/journal.go`
- Modify: `internal/store/journal_test.go`
- Modify: `internal/store/replay_test.go`

- [ ] **Step 1: 先定义 store 层输入，并写出多 Evidence + submit 的失败测试。**

```go
input := AgentSubmission{
    ID: "sub-001", Actor: "cc-haha", Decision: "agent_auto_human_final",
    Action: protocol.Submit, Evidence: []protocol.Evidence{diff, test},
    EvidenceRefs: []string{"diff-001", "test-001"}, At: fixedTime,
}
result, err := journal.ApplyAgentSubmission(ctx, "TASK-1", working.Version, input)
if err != nil { t.Fatal(err) }
if result.State.Status != protocol.Review { t.Fatalf("got %s", result.State.Status) }
if got := result.State.Version; got != working.Version+3 { t.Fatalf("got version %d", got) }
for _, event := range result.Events { if event.Origin != protocol.EventOriginAgent || event.SubmissionID != "sub-001" { t.Fatalf("missing provenance: %#v", event) } }
```

同时加入版本冲突测试：`expectedVersion != state.Version` 时返回 `ErrVersionConflict`，不创建 Evidence 文件，不追加 event，不改变 `state.json`。

- [ ] **Step 2: 运行失败测试。**

Run: `go test ./internal/store -run 'TestFileTaskJournalApplyAgentSubmission' -count=1`

Expected: FAIL，提示 `AgentSubmission` 或 `ApplyAgentSubmission` 未定义。

- [ ] **Step 3: 在 `agent_submission.go` 固定批量语义。**

定义：

```go
type AgentSubmission struct {
    ID           string
    Actor        string
    Decision     string
    Action       protocol.Action
    NextAssignee string
    Message      string
    Feedback     string
    Evidence     []protocol.Evidence
    EvidenceRefs []string
    At           time.Time
}

type AgentSubmissionResult struct {
    State  protocol.State
    Events []protocol.Event
}
```

扩展 `TaskJournal`：

```go
ApplyAgentSubmission(context.Context, string, int64, AgentSubmission) (AgentSubmissionResult, error)
CreateTaskFromAgent(context.Context, protocol.Task, string, string, string, time.Time) error
```

`ID`、`Actor`、`Decision`、时间和 action 的校验必须在任何持久化前完成。重复的 Evidence ID、重复/未引用 Evidence、任务 ID 不一致或非法 portable file ref 必须在此层失败。

- [ ] **Step 4: 在一个 task lock 下实现批量写入。**

`ApplyAgentSubmission` 必须：

1. 获取单一 `Locks.Task`；
2. 检查 journal health 与实时 expected version；
3. 读取 task 和注入的 `ProjectPolicyReader`；构造 journal 时由 `FileRegistryStore` 同时提供 protocol references 与项目读取能力；
4. 先调用 `AuthorizeAgentAction`，再调用现有 action policy 和 `Transition`；
5. 在内存中计算每条 Evidence event 和最终 transition event 的连续 `ExpectedVersion`、`EventID`、状态；
6. 只在所有业务校验通过后 `EnsureEvidence`，随后按计算顺序使用已有 `commitRecord` 写 Evidence event 和最终 transition event；
7. 给每个自动 event 填入 `Origin: EventOriginAgent`、`SubmissionID: input.ID`、`PolicyDecision: input.Decision`。

文件系统写入中断仍遵循已有 journal 的 `RECOVERABLE_TAIL / ErrCommitOutcomeUnknown` 语义；调用方必须把结果标成 `UNKNOWN` 而不是盲目重试。确定性的格式、权限、版本和策略失败绝不能在第 6 步前写入任何 Evidence 或 event。

`CreateTaskFromAgent` 复用现有临时目录创建流程，但 task-created event 标为 Agent 来源；它只接受 `actor == task.Creator` 且该客户端具备 `create_task` capability。

- [ ] **Step 5: 覆盖 replay 和失败边界。**

新增断言：

```go
func TestReplayAcceptsLegacyEventWithoutOrigin(t *testing.T) {
    event, err := protocol.DecodeEventLine([]byte(`{"event_id":1,"task_id":"TASK-1","type":"task_created","actor":"codex","at":"2026-07-28T00:00:00Z","body":"title","evidence_refs":[],"expected_version":0}`), "TASK-1")
    if err != nil || event.Origin != "" { t.Fatalf("event=%#v err=%v", event, err) }
}

func TestApplyAgentSubmissionRejectsApproveBeforeWrite(t *testing.T) {
    before := inspectState(t, journal, "TASK-1")
    _, err := journal.ApplyAgentSubmission(ctx, "TASK-1", before.Version, AgentSubmission{ID: "sub-approve", Actor: "codex", Decision: "agent_auto_human_final", Action: protocol.Approve, At: fixedTime})
    if !errors.Is(err, protocol.ErrHumanFinalReviewRequired) { t.Fatalf("got %v", err) }
    if after := inspectState(t, journal, "TASK-1"); after != before { t.Fatalf("state changed: before=%#v after=%#v", before, after) }
}

func TestCreateTaskFromAgentRecordsProvenance(t *testing.T) {
    if err := journal.CreateTaskFromAgent(ctx, validTask("TASK-1"), "sub-create", "codex", "agent_auto_human_final", fixedTime); err != nil { t.Fatal(err) }
    events := readEvents(t, journal, "TASK-1")
    if len(events) != 1 || events[0].Origin != protocol.EventOriginAgent || events[0].SubmissionID == "" { t.Fatalf("events=%#v", events) }
}
```

- [ ] **Step 6: 运行 store 相关测试。**

Run: `gofmt -w internal/store/agent_submission.go internal/store/journal.go internal/store/journal_test.go internal/store/replay_test.go; go test ./internal/store ./internal/protocol -count=1`

Expected: PASS。

### Task 4: 本机 Agent Intake、收据和异常队列

**Files:**
- Create: `internal/agentintake/model.go`
- Create: `internal/agentintake/store.go`
- Create: `internal/agentintake/service.go`
- Create: `internal/agentintake/service_test.go`
- Create: `internal/agentintake/store_test.go`

- [ ] **Step 1: 写出先留档、后验证、失败不改 task 的测试。**

```go
func TestServiceSubmitResponseRejectsStaleVersionWithoutTaskWrite(t *testing.T) {
    result, err := service.SubmitResponse(ctx, packageDir, staleResponsePath)
    if !errors.Is(err, store.ErrVersionConflict) { t.Fatalf("got %v", err) }
    if result.Receipt.Status != Rejected { t.Fatalf("got %s", result.Receipt.Status) }
    if got := inspectState(t, journal, "TASK-1"); got.Version != 3 { t.Fatalf("state changed: %#v", got) }
    records, _ := receipts.List(ctx)
    if len(records) != 1 || records[0].Reason == "" { t.Fatal("rejection was not retained") }
}
```

再写一条真实流程：`ASSIGNED → agent accept → WORKING → agent response(diff+test+submit) → REVIEW`，所有 receipt 都显示来源 `cc-haha`、提交 ID、实时结果版本。

- [ ] **Step 2: 运行失败测试。**

Run: `go test ./internal/agentintake -run 'TestServiceSubmitResponse' -count=1`

Expected: FAIL，因为 package 尚不存在。

- [ ] **Step 3: 定义稳定的候选与收据格式。**

`model.go` 中定义以下类型，并用严格 JSON decoder 拒绝额外字段：

```go
type ReceiptStatus string
const (
    Received ReceiptStatus = "RECEIVED"
    Accepted ReceiptStatus = "ACCEPTED"
    Rejected ReceiptStatus = "REJECTED"
    Unknown  ReceiptStatus = "UNKNOWN"
)

type TaskCreateCandidate struct {
    FormatVersion  string   `json:"format_version"`
    SubmissionID   string   `json:"submission_id"`
    SourceClientID string   `json:"source_client_id"`
    ID             string   `json:"id"`
    ProjectID      string   `json:"project_id"`
    Title          string   `json:"title"`
    Objective      string   `json:"objective"`
    Acceptance     []string `json:"acceptance"`
    Creator        string   `json:"creator"`
    Reviewer       string   `json:"reviewer"`
}

type Receipt struct {
    ID, Kind, SourceClientID, TaskID, PackageID string
    Status ReceiptStatus
    Reason string
    ObservedVersion, CurrentVersion int64
    ReceivedAt time.Time
    AppliedEventIDs []int64
}
```

候选原始 JSON 不做 shell 拼接、不写入任务事件；收据目录固定为 `collaboration/.runtime/agent-submissions/<submission-id>.json`。同一 ID 内容完全相同视为幂等读取，内容不同返回冲突。

- [ ] **Step 4: 实现任务创建与交接包响应入口。**

`Service` 依赖 `handoff.ValidateResponsePackage`、registry、query、journal、receipt store 和 clock。实现：

```go
SubmitResponse(ctx context.Context, packageDir, inputPath string) (Result, error)
CreateTask(ctx context.Context, inputPath string) (Result, error)
ListReceipts(ctx context.Context) ([]Receipt, error)
```

`SubmitResponse` 顺序固定为：读取原始候选 → 写 `RECEIVED` 收据 → `handoff.ValidateResponsePackage` → 读取当前 task snapshot → 比对 `TaskID`、`ObservedVersion`、`ObservedThroughEvent` → 读取当前项目策略 → `ApplyAgentSubmission` → 更新为 `ACCEPTED`。任何验证错误写 `REJECTED`，状态写入结果不确定写 `UNKNOWN`；两者都不调用重试循环。

`CreateTask` 使用服务 clock 生成 `protocol.Task.CreatedAt`，只允许候选 `source_client_id == creator`，且 creator/reviewer 都已登记并具备对应能力；它调用 `CreateTaskFromAgent` 并记录收据。不要接受客户端自报时间作为任务创建时间。

- [ ] **Step 5: 运行 Intake 测试。**

Run: `gofmt -w internal/agentintake; go test ./internal/agentintake -count=1`

Expected: PASS。

### Task 5: 自动交接上下文、游标历史和安全输出目录

**Files:**
- Create: `internal/handoff/history.go`
- Create: `internal/handoff/history_test.go`
- Modify: `internal/handoff/handoff.go`
- Modify: `internal/handoff/publish.go`
- Modify: `internal/handoff/handoff_test.go`
- Modify: `internal/handoff/publish_test.go`
- Modify: `internal/store/binding.go`
- Modify: `internal/store/binding_test.go`

- [ ] **Step 1: 为唯一可用 Binding、首包和后续游标写失败测试。**

```go
func TestExportNextUsesZeroForFirstPackage(t *testing.T) {
    report, context, err := service.ExportNext(ctx, "TASK-1")
    if err != nil { t.Fatal(err) }
    if context.AfterEvent != 0 || report.ThroughEvent == 0 { t.Fatalf("got %#v %#v", context, report) }
}

func TestExportNextUsesPreviousTargetCursor(t *testing.T) {
    recordHistory(t, "TASK-1", "cc-haha", 4)
    _, context, err := service.ExportNext(ctx, "TASK-1")
    if err != nil { t.Fatal(err) }
    if context.AfterEvent != 4 { t.Fatalf("got %d", context.AfterEvent) }
}
```

另加测试：零个或多个有效 Binding 返回明确的 `ErrBindingSelectionRequired`，不退回为自由文本；普通 `collaboration/tasks/...` 输出仍被拒绝，只有新生成的 `collaboration/.runtime/handoffs/...` 可以通过。

- [ ] **Step 2: 运行 handoff 失败测试。**

Run: `go test ./internal/handoff ./internal/store -run 'Test(ExportNext|DirectoryPublisher)' -count=1`

Expected: FAIL，提示 `ExportNext`、历史或 Binding listing 未定义。

- [ ] **Step 3: 提供只读 Binding 列表与本机历史。**

扩展 `BindingStore`：

```go
ListBindings(context.Context, string) ([]ProjectBinding, error)
```

返回该项目所有结构合法且文件不是 symlink 的 Binding，排序稳定；调用 `BindingAvailable` 后只有唯一可用条目才可作为默认设备。

`history.go` 定义：

```go
type HistoryRecord struct {
    TaskID, TargetClient, Adapter, DeviceID, PackageID, OutputDir string
    AfterEvent, ThroughEvent int64
    ExportedAt time.Time
}
```

历史只保存在 `.runtime/handoff-history.jsonl`，因为它含本机输出路径；用单独锁、追加完整 JSON 行和严格读取保持可诊断性。`LastThroughEvent(taskID, targetClient)` 仅从已完成本地 `VerifyPackage` 的记录返回值。

- [ ] **Step 4: 实现 `ExportNext`，不再让 UI 填协议字段。**

在 handoff service 增加：

```go
type NextContext struct {
    TaskID, TargetClient, Adapter, DeviceID, OutputDir string
    AfterEvent int64
}

func (s *Service) NextContext(ctx context.Context, taskID string) (NextContext, error)
func (s *Service) ExportNext(ctx context.Context, taskID string) (ExportReport, NextContext, error)
```

目标规则：`BLOCKED` 发送给 creator；其他状态发送给 `ResponsibleClient`。只为 `codex` 选 `manual-codex`，只为 `cc-haha` 选 `manual-cc-haha`；未知客户端返回 `ErrNoDefaultAdapter`，不猜测适配器。输出目录必须由服务创建为：

```text
collaboration/.runtime/handoffs/<task-id>/<target-client>/<UTC timestamp>-<unique nonce>
```

`unique nonce` 由注入的随机字节来源生成；测试使用固定 nonce。调用现有 `Export` 后再写 HistoryRecord；若验证/记录不成功，返回错误且不声称包已确认。

- [ ] **Step 5: 收紧 publisher 例外。**

`DirectoryPublisher.validateWorkspaceOutput` 继续拒绝仓库根、`collaboration/` 根、project/client/task/binding 目录和所有 symlink 路径；只允许 `pathWithin(filepath.Join(root, "collaboration", ".runtime", "handoffs"), outputDir)`，且 output 的父目录和最终目标都必须解析为该允许根内。不要放宽到整个 `.runtime`。

- [ ] **Step 6: 运行 handoff 与 binding 测试。**

Run: `gofmt -w internal/handoff internal/store/binding.go internal/store/binding_test.go; go test ./internal/handoff ./internal/store -count=1`

Expected: PASS。

### Task 6: CLI 装配与最小人工操作命令

**Files:**
- Modify: `internal/cli/app.go`
- Modify: `internal/cli/commands.go`
- Modify: `internal/cli/output.go`
- Modify: `internal/cli/app_test.go`
- Modify: `internal/cli/console_test.go`

- [ ] **Step 1: 写出 CLI 合约测试。**

```go
func TestAgentSubmitWritesReviewWithoutManualSteps(t *testing.T) {
    code, output, stderr := runJSON(t, app, "agent", "submit", "--package", packageDir, "--input", responsePath)
    if code != ExitOK || stderr != "" { t.Fatalf("code=%d stderr=%s", code, stderr) }
    if got := decodeSubmission(t, output); got.Status != "ACCEPTED" || got.State.Status != protocol.Review { t.Fatalf("got %#v", got) }
}

func TestHumanApproveDerivesReviewerAndVersion(t *testing.T) {
    code, output, _ := runJSON(t, app, "review", "human-approve", "--task", "TASK-1")
    if code != ExitOK || decodeState(t, output).Status != protocol.Done { t.Fatal("human approval failed") }
}
```

- [ ] **Step 2: 运行失败测试。**

Run: `go test ./internal/cli -run 'Test(AgentSubmit|HumanApprove)' -count=1`

Expected: FAIL，因为命令还不存在。

- [ ] **Step 3: 在 `App` 中装配本机服务。**

给 `App` 新增 `Intake *agentintake.Service` 和 handoff history 的构造参数。clock 必须沿用 `a.Clock`，以便 CLI 测试可固定时间，不能在测试路径中直接调用 `time.Now()`。

- [ ] **Step 4: 添加明确、不可混淆的命令。**

新增命令路由：

```text
collab agent task-create --input <candidate.json>
collab agent submit --package <package-dir> --input <candidate-response.json>
collab handoff next --task <task-id>
collab review human-approve --task <task-id>
collab review human-request-changes --task <task-id> --body <feedback>
collab task human-reassign --task <task-id> --client <client-id>
collab project policy --project <project-id> --expected-policy-version <n> --final-review <human|agent> --auto-done <true|false>
```

`agent` 命令输出 receipt（状态、原因、当前版本、写入 event IDs）；`handoff next` 输出自动推导的上下文、package id 和路径；人类命令在执行前读取 snapshot，自动使用 reviewer/creator 与实时 version，并写 `Origin: human` 及对应 policy decision。旧底层命令保持兼容，但文档标注它们是人工/诊断接口，网页和 Agent Intake 不调用它们。

- [ ] **Step 5: 运行 CLI 测试。**

Run: `gofmt -w internal/cli; go test ./internal/cli -count=1`

Expected: PASS。

### Task 7: 面向人工审核的只读工作台 API

**Files:**
- Modify: `internal/webconsole/model.go`
- Modify: `internal/cli/console.go`
- Modify: `internal/webconsole/server.go`
- Modify: `internal/webconsole/server_test.go`
- Modify: `internal/cli/console_test.go`

- [ ] **Step 1: 用 reader/server 测试定义不需要日常上下文输入的 API。**

```go
func TestWorkbenchListsReviewBlockedAndRejectedCandidates(t *testing.T) {
    response := getJSON(t, server, "/api/v1/workbench")
    if len(response.ReviewQueue) != 1 || len(response.Exceptions) != 1 { t.Fatalf("got %#v", response) }
}

func TestHumanReviewEndpointDoesNotAcceptActorOrExpectedVersion(t *testing.T) {
    response := postJSON(t, server, "/api/v1/tasks/TASK-1/human-review", map[string]any{"decision": "approve", "actor": "cc-haha"})
    if response.Code != http.StatusBadRequest { t.Fatalf("got %d", response.Code) }
}
```

- [ ] **Step 2: 运行失败测试。**

Run: `go test ./internal/webconsole ./internal/cli -run 'Test(Workbench|HumanReviewEndpoint)' -count=1`

Expected: FAIL，因为新 DTO 和端点尚未实现。

- [ ] **Step 3: 建立工作台 DTO。**

在 `webconsole/model.go` 加入：

```go
type Workbench struct {
    ReviewQueue []TaskSummary `json:"review_queue"`
    Activity    []ActivityItem `json:"activity"`
    Exceptions  []SubmissionReceipt `json:"exceptions"`
    Handoffs    []HandoffRecord `json:"handoffs"`
}
```

任务详情增加 project policy、`CanHumanApprove`、`CanHumanRequestChanges`、`CanHumanReassign` 和自动交接 `NextContext`；这些字段由 `appConsoleReader` 从快照、项目策略、Binding 和 History 派生，浏览器不提供 actor/version/device/output 参数。

- [ ] **Step 4: 仅增加必要的本地端点。**

保留 GET `/api/v1/overview` 与 `/api/v1/tasks/<id>` 兼容现有页面，同时新增：

```text
GET  /api/v1/workbench
POST /api/v1/tasks/<id>/human-review
POST /api/v1/tasks/<id>/human-reassign
POST /api/v1/tasks/<id>/handoff-next
POST /api/v1/projects/<id>/policy
```

`human-review` 请求只接受 `decision` 与 `feedback`；approve 的 feedback 必须为空，request-changes 必须非空。`handoff-next` 请求体为空。所有写入继续通过既有 CSRF、同源、JSON content-type 和 loopback 限制；不新增用于 Agent 远程调用的 HTTP endpoint。

- [ ] **Step 5: 运行 API/reader 测试。**

Run: `gofmt -w internal/webconsole internal/cli/console.go; go test ./internal/webconsole ./internal/cli -count=1`

Expected: PASS。

### Task 8: 重构网页为三视图人工审查工作台

**Files:**
- Modify: `internal/webconsole/assets/index.html`
- Modify: `internal/webconsole/assets/app.js`
- Modify: `internal/webconsole/assets/style.css`
- Modify: `internal/webconsole/server_test.go`

- [ ] **Step 1: 先写静态资源与 API 表现测试。**

```go
func TestConsoleAssetsExposeHumanReviewWorkbench(t *testing.T) {
    page := getAsset(t, server, "/")
    for _, marker := range []string{"待我审查", "协作活动", "交接历史", "批准完成", "要求返工"} {
        if !strings.Contains(page, marker) { t.Fatalf("missing %q", marker) }
    }
    if strings.Contains(page, "操作角色") || strings.Contains(page, "expected-version") { t.Fatal("manual protocol form leaked into daily UI") }
}
```

- [ ] **Step 2: 运行失败测试。**

Run: `go test ./internal/webconsole -run TestConsoleAssetsExposeHumanReviewWorkbench -count=1`

Expected: FAIL，旧页面仍包含日常手填表单。

- [ ] **Step 3: 用固定三栏信息架构替换日常界面。**

`index.html` 只保留以下工作内容：

```text
侧栏：待我审查 / 协作活动 / 交接历史 / 次级设置
待我审查：REVIEW、BLOCKED、异常候选的紧凑账本
任务审查：目标、验收条件、Evidence 覆盖、最近活动、两个正式结论
协作活动：追加式事件和 Intake 收据，不做聊天气泡
交接历史：下一份包的自动上下文、生成按钮、既往包和路径复制
```

不要显示任务创建、客户端注册、项目绑定、Evidence 手工添加、actor、expected version、设备、after-event 或输出路径的大表单。初始化/绑定/策略只在“次级设置”抽屉中按需出现；`BLOCKED` 只提供明确的“重新分配”上下文弹窗。

- [ ] **Step 4: 实现基于派生上下文的交互。**

`app.js` 只能发送：

```js
await request("POST", `/api/v1/tasks/${taskID}/human-review`, { decision: "approve", feedback: "" });
await request("POST", `/api/v1/tasks/${taskID}/human-review`, { decision: "request_changes", feedback });
await request("POST", `/api/v1/tasks/${taskID}/handoff-next`, {});
```

确认弹窗显示前后状态、任务版本、当前责任方、Evidence 数量和写入事件类型，但不得允许用户改写协议字段。异常条目要完整显示：来源、任务、候选 ID、收据状态、拒绝原因、观察版本与当前版本；只提供“查看原始候选”和“复制诊断”，不提供“一键 Apply”。

- [ ] **Step 5: 维持已选定的视觉约束。**

继续使用浅色 Technical Ledger token：冷灰画布、白色主表面、钢蓝主动作、低饱和状态色、`Segoe UI Variable` 与 `Cascadia Mono`。禁止新增渐变、霓虹、玻璃拟态、机器人头像、在线呼吸点、任务聊天气泡、环形统计图和任何“自动执行”暗示。使用真实表格、分隔线和 4–8px 圆角；主按钮高至少 36px，状态同时含图标与文字。

- [ ] **Step 6: 运行网页服务测试与人工可视化检查。**

Run: `go test ./internal/webconsole -count=1`

Expected: PASS。

随后：构建本机 `collab.exe`，使用现有 `启动网页控制台.cmd` 打开 `http://127.0.0.1:8567/`，确认三项导航、任务空状态、异常空状态、设置抽屉和桌面 1440px 布局均可读取；不把浏览器调试数据写入仓库。

### Task 9: 协议、CLI、试点文档与端到端验证

**Files:**
- Modify: `docs/specs/protocol-v1.md`
- Modify: `docs/specs/cli-v1.md`
- Modify: `docs/pilots/manual-client-pilot.md`
- Modify: `README.md`（仅当已有 CLI/控制台使用章节需同步时）
- Modify: `启动网页控制台.cmd` / `scripts/start-web-console.ps1`（仅当 `collab ui` 的命令契约变更时）

- [ ] **Step 1: 增补协议文档，不重写既有状态机。**

在 `protocol-v1.md` 记录：项目默认 YAML、合法 `human_final`/未来 `agent` 组合、政策历史字段、Event provenance 字段、旧 event 的兼容规则、Agent 可自动登记的动作表、人工独占结论、失败时不写任务状态的保证，以及“本机受信任通道不是远程认证”的限制。

- [ ] **Step 2: 编写可复制的最小 CLI 流程。**

在 `cli-v1.md` 给出实际命令形状：

```text
collab agent task-create --input codex-task.json
collab handoff next --task PILOT-VERSION-001
collab agent submit --package collaboration/.runtime/handoffs/... --input candidate-response.cc-haha.json
collab review human-request-changes --task PILOT-VERSION-001 --body "请补充 Windows 构建证据。"
collab review human-approve --task PILOT-VERSION-001
```

同时明确 `agent submit` 不会执行 shell 命令、`response validate` 保持只读、候选拒绝在本机异常队列中保留、人工审核才可到 `DONE`。

- [ ] **Step 3: 更新试点 runbook。**

将当前手工填写 actor/version/device/output 的段落改为：由 Agent 原生会话生成候选 → `collab agent submit` 自动登记 → 人类在网页阅读 Evidence → 人类 request changes/approve → 下一包 `handoff next`。保留至少一次真实返工的要求，明确还没有进入 Agent 自审。

- [ ] **Step 4: 执行完整静态验证。**

Run: `gofmt -w internal; go vet ./...; go test ./...; go build ./cmd/collab`

Expected: 全部 PASS。

- [ ] **Step 5: 执行临时目录 CLI E2E。**

在系统临时目录复制最小 collaboration fixture，执行：

```text
Codex agent task-create
→ human reassign 给 cc-haha
→ CC-HAHA agent submit accept
→ handoff next
→ CC-HAHA agent submit（diff + test + submit）
→ 网页/API 读取 REVIEW 队列
→ human request changes
→ CC-HAHA resume + 新 Evidence + submit
→ human approve
```

逐步断言 `DRAFT → ASSIGNED → WORKING → REVIEW → REVISION_REQUIRED → WORKING → REVIEW → DONE`，并断言每个 Agent event 都有 `origin=agent`、`submission_id` 和 `policy_decision`；人工结论都有 `origin=human`；版本冲突、越权 `approve` 和坏 Binding 都不会改变状态。

- [ ] **Step 6: 复核工作树与交付边界。**

Run: `git status --short; git diff --check; git diff --stat`

Expected: 只出现本计划和用户此前已批准的网页/配置改动，无构建二进制、`.runtime/`、Binding 或真实候选响应被纳入 Git。不得自行提交或推送。

## 计划自审

### 规格覆盖

| 需求 | 实现任务 |
| --- | --- |
| 项目级 `agent_auto / human / false` 默认策略与人工可审计修改 | Task 1、Task 6、Task 7、Task 9 |
| Codex/CC-HAHA 在原生客户端提交结构化事实 | Task 3、Task 4、Task 6 |
| Agent 只能推进非终态，人工独占 approve/request-changes/DONE | Task 2、Task 3、Task 6、Task 8 |
| 所有自动事件记录来源、策略、版本、Evidence | Task 2、Task 3、Task 4、Task 9 |
| 版本/权限/格式错误不改状态且进入异常队列 | Task 3、Task 4、Task 7、Task 8 |
| 项目、设备、适配器、游标自动推导 | Task 5、Task 6、Task 7、Task 8 |
| 首包游标为 0，后续包按同目标历史前进 | Task 5、Task 9 |
| 工作台三项导航与非 AI 味视觉边界 | Task 7、Task 8 |
| 不接 AO/sidecar/远程认证/自动控制/自动自审 | 先决边界、Task 9 文档 |
| 真实闭环前不启用 Agent 最终审查 | Task 1 策略限制、Task 9 runbook |

### 占位符检查

本计划不含未落地标记、未命名的错误处理或跨任务引用省略。每个新增行为都指定了文件、失败测试、实现入口和验证命令。

### 类型一致性检查

- `protocol.CollaborationPolicy` 是项目策略的唯一模型；registry、intake、console 只传递该类型。
- `store.AgentSubmission` 是已验证候选的 journal 输入；`agentintake.Receipt` 是候选处理结果，二者不混用。
- `handoff.NextContext` 只表示自动导出的派生参数；`handoff.HistoryRecord` 只保存本机历史，不进入 portable manifest。
- `EventOrigin`、`SubmissionID`、`PolicyDecision` 由 journal 生成并由 handoff/console 原样展示，旧 event 保持无字段兼容。

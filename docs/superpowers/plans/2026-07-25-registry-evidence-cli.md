# Registry、Evidence 与 CLI v1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 交付可在当前目录运行、以文件为持久化中枢的 `collab.exe` 命令行协作闭环。

**Architecture:** Registry 负责项目/客户端事实，EvidenceStore 负责不可变证据，Journal 在任务锁中协调证据、状态与事件。Replay 从审计日志和真实 Evidence 重建 State；CLI 只做参数解析、依赖注入、输出与退出码映射。

**Tech Stack:** Go 1.25、标准库 `flag`/`encoding/json`、yaml.v3、gofrs/flock、GitHub Actions。

---

## 文件结构

- `internal/protocol/evidence.go`：Evidence、TransitionIntent、严格 JSON 与路径验证。
- `internal/protocol/replay.go`：无副作用的审计回放与 Evidence resolver。
- `internal/store/registry.go`：项目/客户端 YAML 的原子 RegistryStore。
- `internal/store/evidence.go`：不可变 JSON EvidenceStore。
- `internal/store/journal.go`：引用注入、真实证据派生、AddEvidence、Replay 驱动 Inspect。
- `internal/cli/app.go`、`commands.go`、`output.go`、`exitcode.go`：可测试 CLI 应用层。
- `cmd/collab/main.go`：只组装 App 并返回退出码。
- 对应 `*_test.go`：协议、Store 和 CLI 单元/集成测试。
- `docs/specs/protocol-v1.md`、`docs/specs/cli-v1.md`、`docs/plans/mvp-v1-implementation.md`：与实现一致的规范。

### Task 1: 扩展 Protocol 模型与严格 Evidence 解码

**Files:**
- Create: `internal/protocol/evidence.go`
- Create: `internal/protocol/evidence_test.go`
- Modify: `internal/protocol/model.go`
- Modify: `internal/protocol/state.go`
- Test: `internal/protocol/evidence_test.go`

- [x] 写失败测试，覆盖 UTC Evidence、重复 file ref、绝对路径/凭据、未知 JSON 字段、无效 kind。
- [x] 运行 `go test ./internal/protocol -run Evidence`，确认缺少类型时失败。
- [x] 实现 `Evidence.Validate`、`DecodeEvidence`、`TransitionIntent` 和 capability 查询；`Task.Validate` 要求真实 References 能确认 capability。
- [x] 运行 `go test ./internal/protocol`，确认通过。
- [x] 提交：`git commit -m "feat: add evidence protocol model"`。

### Task 2: 实现 RegistryStore 与任务引用校验

**Files:**
- Create: `internal/store/registry.go`
- Create: `internal/store/registry_test.go`
- Modify: `internal/store/journal.go`
- Modify: `internal/store/journal_test.go`

- [x] 写失败测试：项目/客户端创建、重复拒绝、严格 YAML、`CreateTask` 拒绝未知项目或缺少 create/review capability。
- [x] 运行 `go test ./internal/store -run 'Registry|CreateTask'`，确认失败。
- [x] 实现原子 YAML 创建：锁、同目录临时文件、`writeFull`、Sync、Close、Replace；实现 `References` 和 `HasCapability`。
- [x] 把 `CreateTask(ctx, task, at)` 改为 `CreateTask(ctx, task)`，让 task 创建时间驱动首事件与 State。
- [x] 运行 `go test ./internal/store`，确认通过。
- [x] 提交：`git commit -m "feat: add registry-backed task creation"`。

### Task 3: EvidenceStore、幂等写入与完整 Replay

**Files:**
- Create: `internal/store/evidence.go`
- Create: `internal/store/evidence_test.go`
- Create: `internal/protocol/replay.go`
- Create: `internal/protocol/replay_test.go`
- Modify: `internal/store/journal.go`
- Modify: `internal/store/journal_test.go`

- [x] 写失败测试：Evidence 原子创建、同内容幂等、异内容冲突、submit/block 仅接受真实 evidence refs、`evidence_added` 不改变业务字段、非法历史顺序为 CORRUPT。
- [x] 运行相应 `go test`，确认缺失 Store/Replay 时失败。
- [x] 实现 EvidenceStore 的读写与 `EvidenceResolver`；实现 `Replay`：从 task_created 建立 State，按事件逐条验证版本、时间、角色、状态机和证据。
- [x] 在 Journal 中注入 EvidenceStore，新增 `AddEvidence`，把公开转移输入替换为 `TransitionIntent`，让 Inspect 用 Replay 复原并完整比较 State。
- [x] 运行 `go test ./internal/protocol ./internal/store`，确认通过。
- [x] 提交：`git commit -m "feat: replay journal from immutable evidence"`。

### Task 4: 建立可测试 CLI 应用层

**Files:**
- Create: `internal/cli/app.go`
- Create: `internal/cli/commands.go`
- Create: `internal/cli/output.go`
- Create: `internal/cli/exitcode.go`
- Create: `internal/cli/app_test.go`
- Modify: `cmd/collab/main.go`
- Modify: `.gitignore`

- [x] 写失败测试：init 幂等、注册/项目/任务创建、每个状态命令、JSON 严格可解析及所有错误退出码。
- [x] 运行 `go test ./internal/cli`，确认命令尚不存在时失败。
- [x] 实现 App 依赖注入（stdin/stdout/stderr/clock/stores）、根命令分派、重复 flag 解析、状态输出和错误映射。
- [x] `main.go` 从当前目录创建 App 并 `os.Exit(app.Run(...))`；init 只追加缺失 `.gitignore` 行。
- [x] 运行 `go test ./internal/cli`，确认通过。
- [x] 提交：`git commit -m "feat: add collaboration command line workflow"`。

### Task 5: CLI 端到端闭环与规范同步

**Files:**
- Modify: `internal/cli/app_test.go`
- Modify: `docs/specs/protocol-v1.md`
- Modify: `docs/specs/cli-v1.md`
- Modify: `docs/plans/mvp-v1-implementation.md`

- [x] 写一个只调用 App/CLI 的集成测试：init → 两客户端 → 项目 → 任务 → assign → accept → evidence → submit → request changes → resume → evidence → submit → approve → status DONE。
- [x] 为 outcome unknown、recoverable tail、corrupt、not found 写 CLI 错误消息和退出码测试。
- [x] 更新协议与 CLI 文档，删除本阶段不实现的 bind/handoff/adapter 命令，写明 Replay、Evidence 原子策略和状态码。
- [ ] 运行 `gofmt -w`、`go vet ./...`、`go test ./...`、`go test -race ./...`、`go build -o collab.exe ./cmd/collab`、`git diff --check`。
- [x] 提交：`git commit -m "docs: complete cli evidence workflow"`。

### Task 6: 发布验收

**Files:**
- Modify: only files required by test fixes or documentation corrections

- [x] 在临时目录运行编译出的 `collab.exe`，保存从 init 到 DONE 的真实命令输出，不直接编辑状态或日志文件。
- [x] 扫描仓库中的凭据模式与本机绝对路径；仅提交本阶段文件。
- [ ] 推送 main，等待 Windows、Ubuntu 与 Ubuntu Race CI 全绿。
- [ ] 报告 SHA、命令演示、CI 链接与未实现的 binding/handoff/手工适配器。

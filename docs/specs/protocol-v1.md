# Protocol v1

## 范围与非目标

Protocol v1 定义独立客户端之间可审计、可迁移的文件协作协议。Codex、CC-HAHA
保留各自的 UI、会话、登录态、技能和 MCP；中枢只保存任务、事件、证据、审查
与交接信息。

本版本不接入 AO、ConPTY、sidecar、GUI 或客户端自动控制；不执行 commit、push
或 merge。绝对路径、PID、PTY、客户端会话 ID 与本地凭据不得写入可迁移数据。

## 仓库与数据布局

```text
client-collaboration-hub/
  docs/specs/
  docs/plans/
  collaboration/
    projects/<project-id>.yaml
    clients/<client-id>.yaml
    tasks/<task-id>/task.yaml
    tasks/<task-id>/state.json
    tasks/<task-id>/messages.jsonl
    tasks/<task-id>/evidence/<evidence-id>.json
    tasks/<task-id>/handoff.md
    bindings/<device-id>/<project-id>.local.json  # .gitignore，不可迁移
    .runtime/locks/tasks/<task-id>.lock           # .gitignore，不可迁移
```

`collaboration/` 的任务数据可进入 Git；`bindings/` 是设备本地映射，必须忽略。
逻辑项目以 `project-id` 标识，换设备后重新 bind，不修改任务文件。

## 核心模型

`task.yaml`：`id`、`project_id`、`title`、`objective`、`acceptance`、`creator`、
`reviewer`、`created_at`。创建时 `reviewer` 默认等于 `creator`，可显式
指定；创建后不可更改 `id` 与 `project_id`。

`state.json`：

```json
{
  "task_id": "T-0001",
  "status": "ASSIGNED",
  "version": 2,
  "last_event_id": 4,
  "assigned_client": "cc-haha",
  "responsible_client": "cc-haha",
  "updated_at": "2026-07-24T14:00:00Z"
}
```

`messages.jsonl` 的每行均为事件，最小字段为 `event_id`（严格递增）、`type`、
`actor`、`at`、`body`、`evidence_refs`、`expected_version`。禁止修改或删除既有行。

`evidence/<id>.json` 包含 `id`、`task_id`、`kind`（`diff`、`test`、`artifact`、
`blocker`）、`summary`、`files`、`created_by`、`created_at`。被提交或审查引用时，
证据文件必须已存在且 `task_id` 匹配。

`handoff.md` 是人可直接阅读的自包含交接包：任务目标、当前状态、责任方、未读事件、
证据索引、下一步命令。它是导出物，不是状态真源。

## 状态机

```text
DRAFT -> ASSIGNED -> WORKING -> REVIEW -> DONE
                              \-> REVISION_REQUIRED -> WORKING
              \-------------------------------> BLOCKED
```

| 转换 | 前置条件 | 写入事件 |
| --- | --- | --- |
| create: `DRAFT` | 新 task ID，项目存在 | `task_created` |
| assign: `DRAFT/BLOCKED -> ASSIGNED` | 创建者、目标客户端已注册 | `assigned` |
| accept: `ASSIGNED -> WORKING` | 当前责任方 | `accepted` |
| submit: `WORKING -> REVIEW` | 当前责任方；至少一个 `diff` 或 `artifact`，且至少一个 `test` 证据 | `submitted` |
| request-changes: `REVIEW -> REVISION_REQUIRED` | `reviewer`；非空反馈 | `changes_requested` |
| resume: `REVISION_REQUIRED -> WORKING` | 当前责任方 | `revision_started` |
| approve: `REVIEW -> DONE` | `reviewer` | `approved` |
| block: `ASSIGNED/WORKING/REVIEW -> BLOCKED` | 当前责任方或创建者；`blocker` 证据 | `blocked` |

除表中转换外一律拒绝。`BLOCKED` 不能隐式恢复；创建者重新 assign 后才进入
`ASSIGNED`。

动态指派只存在于 `state.json` 和事件，不存在于 `task.yaml`。责任字段的固定语义为：

| 状态 | `assigned_client` | `responsible_client` |
| --- | --- | --- |
| `DRAFT` | 空 | `creator` |
| `ASSIGNED`、`WORKING` | 执行者 | 执行者 |
| `REVIEW`、`DONE` | 执行者 | `reviewer` |
| `REVISION_REQUIRED` | 执行者 | 执行者 |
| `BLOCKED` | 保留 | 保留阻塞前责任方 |

## 原子性与并发

正式变更只能由 CLI 完成。锁文件位于 `.runtime/locks/`：任务使用
`tasks/<task-id>.lock`，项目使用 `projects.lock`，客户端使用 `clients.lock`。持锁后必须
重新读取状态、检查 `--expected-version`、校验证据并写入；禁止全仓库锁。锁阻止同时通过版本检查，乐观锁拒绝
基于旧状态的调用。

MVP 保留 JSONL，且 `TaskJournal` 是事件与状态的唯一事务写入口。成功写入顺序为：
持任务锁并 Inspect → 校验版本和不变量 → 追加一条完整事件并 Sync → 将新 state 写入
同目录临时文件并 Sync/Close → 经 `AtomicReplacer` 原子替换 → 重新 Inspect。调用方不能
自行追加 JSONL 或单独保存 State。

健康状态为 `HEALTHY`、`RECOVERABLE_TAIL` 与 `CORRUPT`。只有“恰好多一条完整尾部
事件、ID 为 `last_event_id + 1`、expected version 等于当前 state version”可由 `recover`
在备份后截断。JSONL 行不完整、多出多条事件、ID 不连续、state 超前、非法 JSON 或任务
ID 不一致时为只读 `CORRUPT`；它不是业务状态，CLI 不得猜测修复。备份写入设备本地的
`.runtime/recovery/`。实现已在 Windows 与 Unix CI 测试原子替换，但不承诺任意文件系统
在断电情形都具备相同持久性。CLI 不提供人工 JSON 编辑入口。

YAML 以 `yaml.v3` 的 `KnownFields(true)` 解码，并在模型级 `Validate()` 中拒绝未知
字段、重复 key、多文档、缺失字段、非法 ID/枚举、非 UTC RFC 3339 时间、路径与文件 ID
不一致、受支持前缀的本地文件系统路径，以及 PID、PTY、session ID 或疑似凭据字段。
受支持的 Unix 本地路径前缀为 `/home/`、`/Users/`、`/root/`、`/tmp/`、`/var/`、`/etc/`、
`/opt/`、`/usr/`、`/mnt/`、`/srv/` 与 `/workspace/`；Windows 盘符、UNC、`~/` 与
`file://` 也被拒绝。逻辑路由如 `/health` 与 `/api/v1/tasks` 不是本地路径。任务还必须
验证项目和客户端引用存在。

## 客户端适配器

基础适配器只处理可人工携带的交接包，不能假装中枢能控制客户端：

```text
ClientAdapter
  client_id() -> stable logical id
  capabilities() -> declared capabilities
  export_assignment(task_ref) -> delivery_package
  import_events(client_output) -> validated events
  recover(cursor) -> unread events and state
```

`ManualCodexAdapter` 与 `ManualCCHahaAdapter` 都实现基础接口：导出一段可粘贴的
任务指令和 `handoff.md`，再将客户端给出的结构化结果交给 CLI 校验并写入。

只有经实际验证的自动化通道才可额外实现：

```text
ActiveClientAdapter
  deliver(task_ref) -> accepted | unavailable
```

未来 `CodexDesktopAdapter`、`CCHahaSidecarAdapter` 只能在实现并验证相应通道后加入，
且不改变 v1 数据或状态机。

## v1 演示闭环

`T-0001` 必须覆盖：Codex 创建 → 指派 CC-HAHA → 导出 CC-HAHA 交接包 → 写回结果、
diff、测试和说明 → Codex 要求返工一次 → CC-HAHA 修订 → Codex 审批 DONE → 在新
本地路径重新绑定同一项目并 recover。全程不启动 sidecar 或 AO。

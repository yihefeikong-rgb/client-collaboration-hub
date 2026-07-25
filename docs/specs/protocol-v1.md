# Protocol v1

## 范围与非目标

Protocol v1 是独立客户端间可审计、可迁移的文件协作协议。Codex、CC-HAHA 等客户端保留
各自的 UI、会话、登录态、技能和 MCP；协作中枢只保存逻辑项目、客户端、任务、事件和
证据。当前不实现 AO、ConPTY、sidecar、GUI、handoff、binding 命令或客户端自动控制。

绝对路径、PID、PTY、客户端会话 ID 与本地凭据不得进入可迁移数据。

## 布局与 Registry

```text
collaboration/
  projects/<project-id>.yaml
  clients/<client-id>.yaml
  tasks/<task-id>/task.yaml
  tasks/<task-id>/state.json
  tasks/<task-id>/messages.jsonl
  tasks/<task-id>/evidence/<evidence-id>.json
  bindings/                         # 预留且 .gitignore
  .runtime/                         # 锁与 recovery，.gitignore
```

项目和客户端由 `RegistryStore` 原子创建：目标同目录临时文件完整写入、Sync、Close 后再
替换，已有 ID 一律拒绝覆盖。YAML 使用严格已知字段、单文档和重复 key 校验。

任务创建必须确认 project、creator 与 reviewer 都已注册；creator 必须有 `create_task`，
reviewer 必须有 `review` capability。`task.created_at` 是唯一创建时间真源。

## 任务、State 与 Evidence

`task.yaml` 固定保存 `id`、`project_id`、`title`、`objective`、`acceptance`、`creator`、
`reviewer` 与 UTC `created_at`。`reviewer` 默认等于 creator。

`state.json` 保存业务状态、版本、最后事件、assigned/responsible client 与 UTC
`updated_at`。任务建立后始终满足：

```text
state.version == state.last_event_id
event.event_id == event.expected_version + 1
```

Evidence 是不可变 JSON，字段为 `id`、`task_id`、`kind` (`diff`、`artifact`、`test`、
`blocker`)、`summary`、`file_refs`、`created_by`、`created_at`。JSON 严格拒绝未知字段；
file refs 不能包含本机绝对路径、重复项或疑似凭据。相同 ID 的相同内容幂等，不同内容冲突。
合法但尚未被事件引用的 Evidence 是允许的孤立事实，不会使 Journal 损坏。

## 状态机与事件

```text
DRAFT -> ASSIGNED -> WORKING -> REVIEW -> DONE
                              \-> REVISION_REQUIRED -> WORKING
              \-------------------------------> BLOCKED
```

| 转换 | 事件 | 权限与证据 |
| --- | --- | --- |
| create | `task_created` | creator；第一条事件，ID 1，expected version 0 |
| assign | `assigned` | creator，记录 target_client |
| accept | `accepted` | assigned executor |
| submit | `submitted` | executor；真实 Evidence 至少有 diff/artifact 和 test |
| request changes | `changes_requested` | reviewer；body 为非空反馈 |
| resume | `revision_started` | executor |
| approve | `approved` | reviewer |
| block | `blocked` | creator 或责任方；真实 blocker Evidence |

`message_added` 和 `evidence_added` 都是版本事件：递增 version、last_event_id 与
updated_at，但不改变业务 status、assigned_client 或 responsible_client。
`evidence_added` 必须恰好引用一份同任务 Evidence，body 等于其 summary，actor 与
created_by 一致。

## 事务、Replay 与恢复

公共 `TaskJournal` 写接口只接受 `Task`、`TransitionIntent`、消息意图或 `Evidence`；调用方
不能传 Event、next State、event ID、expected version 或 EvidenceKinds。Journal 在任务锁中：

```text
Inspect -> 校验 expected version -> 读取真实 Evidence -> 派生 kinds
-> 推导 Event/State -> append+Sync -> 原子 State 替换 -> Replay 复验
```

`Replay` 从首条 `task_created` 重建 State，逐条验证 Event ID、expected version、时间、
注册客户端、角色 capability、状态机转换、Evidence 引用和非业务事件。最终重建结果必须与
`state.json` 完全相等；任何非法中间顺序（例如 task_created 后 approve）均为 `CORRUPT`。

不存在整个任务目录返回 `ErrTaskNotFound`；目录存在但任务文件缺失或非法为 `CORRUPT`。
唯一完整、连续且尚未提交的尾事件是 `RECOVERABLE_TAIL`。`recover` 先完整备份再截断，且
必须证明恢复后的 State 与恢复前相同。替换已成功但最终 Replay 无法确认时返回
`ErrCommitOutcomeUnknown`；调用方必须先 Inspect，不能直接重试。

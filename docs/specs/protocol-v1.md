# Protocol v1

## 范围与非目标

Protocol v1 是独立客户端间可审计、可迁移的文件协作协议。Codex、CC-HAHA 等客户端保留
各自的 UI、会话、登录态、技能和 MCP；协作中枢只保存逻辑项目、客户端、任务、事件和
证据。当前提供设备本地 Binding、只读查询与手工交接包，但不实现 AO、ConPTY、sidecar、
GUI、自动 import 或客户端自动控制。

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
  bindings/<device>/<project>.local.json  # 设备本地且 .gitignore
  .runtime/                               # 锁与 recovery，.gitignore
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

`BLOCKED` 保留阻塞前的 `assigned_client` 和 `responsible_client`；它们描述责任归属，不等于
下一步允许写入的 actor。创建者在 `BLOCKED` 中可重新 `assign`，即使责任方仍是执行客户端。

Evidence 是不可变 JSON，字段为 `id`、`task_id`、`kind` (`diff`、`artifact`、`test`、
`blocker`)、`summary`、`file_refs`、`created_by`、`created_at`。JSON 严格拒绝未知字段；
相同 ID 的相同内容幂等，不同内容冲突。

FileRef 是用 `/` 分隔的项目相对路径。协议拒绝绝对路径、盘符、UNC、反斜杠、`..`、空段、
URL/file scheme、控制字符和疑似凭据。项目/客户端显示名、任务文本、消息、反馈与 Evidence
摘要同样拒绝明显凭据及本机路径。

合法但尚未被事件引用的 Evidence 是允许的孤立事实，不会使 Journal 损坏；但它不能用于
`submitted` 或 `blocked`。只有先出现唯一、内容一致的 `evidence_added` 事件，才算进入
审计链。相同已公告 Evidence 的重复 `evidence add` 返回现有 State，不会新增版本或事件。

## 状态机与事件

```text
DRAFT -> ASSIGNED -> WORKING -> REVIEW -> DONE
                              \-> REVISION_REQUIRED -> WORKING
              \-------------------------------> BLOCKED
```

| 转换 | 事件 | 权限与证据 |
| --- | --- | --- |
| create | `task_created` | creator；第一条事件，ID 1，expected version 0 |
| assign | `assigned` | creator 且有 `create_task`；target_client 已注册且有 `execute` |
| accept | `accepted` | assigned executor |
| submit | `submitted` | executor；已公告 Evidence 至少有 diff/artifact 和 test |
| request changes | `changes_requested` | reviewer；body 为非空反馈 |
| resume | `revision_started` | executor |
| approve | `approved` | reviewer |
| block | `blocked` | creator 或责任方；已公告 blocker Evidence |

`message_added` 和 `evidence_added` 都是版本事件：递增 version、last_event_id 与
updated_at，但不改变业务 status、assigned_client 或 responsible_client。
`evidence_added` 必须恰好引用一份同任务 Evidence，body 等于其 summary，actor 与
created_by 一致。

`ActionPolicy` 是 Journal、Replay、TaskQuery 和 Handoff 唯一的 actor、角色、capability 与
状态许可来源。`AllowedActions(task, state, actor, refs)` 和
`Authorize(task, state, actor, action, refs)` 必须互相一致；状态转换本身只处理状态和 payload
约束，不能另建角色判断。`message` 与 `evidence_add` 在非 `DONE` 状态只允许 creator、reviewer
或 assigned client。`DONE` 是终态：拒绝全部 transition、message 和 evidence 写入，只允许
status/query 与只读 handoff。

## 事务、Replay 与恢复

公共 `TaskJournal` 写接口只接受 `Task`、`TransitionIntent`、消息意图或 `Evidence`；调用方
不能传 Event、next State、event ID、expected version 或 EvidenceKinds。Journal 在任务锁中：

```text
Inspect -> 校验 expected version -> 读取已公告 Evidence -> 派生 kinds
-> 推导 Event/State -> append+Sync -> 原子 State 替换 -> Replay 复验
```

`Replay` 从首条 `task_created` 重建 State，逐条验证 Event ID、expected version、时间、
注册客户端、角色 capability、状态机转换、Evidence 引用和非业务事件。最终重建结果必须与
`state.json` 完全相等；任何非法中间顺序（例如 task_created 后 approve）均为 `CORRUPT`。

Replay 维护已公告 Evidence ID 集合：重复 `evidence_added`、提交前未公告、或提交后才公告
的 Evidence 都会使任务变为 `CORRUPT`。`assigned` 同时重放目标客户端注册和 capability
校验，不能以篡改的合法 ID 绕过 Registry。

不存在整个任务目录返回 `ErrTaskNotFound`；目录存在但任务文件缺失或非法为 `CORRUPT`。
唯一完整、连续且尚未提交的尾事件是 `RECOVERABLE_TAIL`。`recover` 先完整备份再截断，且
必须证明恢复后的 State 与恢复前相同。替换已成功但最终 Replay 无法确认时返回
`ErrCommitOutcomeUnknown`；调用方必须先 Inspect，不能直接重试。

## 本地 Binding、查询与交接包

`ProjectBinding` 仅保存于 `bindings/<device>/<project>.local.json`，字段为 device_id、
project_id、规范绝对 local_path、可选 revision 和 bound_at。它验证项目存在与目录可用，
在 `.runtime/locks/bindings/<device>/<project>.lock` 下原子更新。Binding 绝不进入 Task、
Event、Evidence、manifest 或 handoff.md。

`BindingResolver` 先校验 portable FileRef，再从本地 binding root 解析。存在的文件通过
EvalSymlinks 后必须仍位于真实 root 内；符号链接、junction/reparse point、跨卷与大小写
绕过都会被拒绝。不存在的文件只导出 `available: false`；可用文件导出 relative_ref、size
和 sha256，不导出本机路径。默认只哈希不超过 64 MiB 的普通文件；App 可注入更小或更大的
上限。大小从已打开文件句柄取得，读取循环检查 cancellation，并比较前后及当前路径的文件
标识、大小和修改时间。检测到变化即拒绝导出。

这是一条可信本地 worktree 的并发变化检测边界，不宣称能抵御所有恶意竞态；当前实现没有把
平台专用 nofollow/openat/最终路径句柄验证作为安全承诺。

`TaskQuery.SnapshotForActor(task, after_event, actor)` 在任务锁中返回一致的 Project、Task、
State、健康度、事件增量、按首次公告排序的 Evidence、`action_actor` 与该 actor 的
allowed_actions。`Snapshot` 为 status 选择默认 actor（通常是责任方，`BLOCKED` 为 creator）。
after_event 必须介于 0 和 last_event_id；事件严格满足 `event_id > after_event`。只有
`HEALTHY` Snapshot 可导出。

`manual-codex`、`manual-cc-haha` 和 `manual-reasonix` 适配器只生成文件交接包。目标客户端
必须已注册、具备 `import_export`，并符合适配器角色：`manual-codex` 面向 creator/reviewer，
`manual-cc-haha` 面向 assigned executor，`manual-reasonix` 面向 REVIEW 状态的责任审核者。
普通状态导出给责任方；`BLOCKED` 的 manual-codex 可交给具备 `assign` 权限的 creator。manifest
中的 `action_actor` 明确标识可以执行建议命令的客户端，不能用 responsible_client 替代。

包固定包含 `handoff.md`、`manifest.json`、`candidate-response.json` 与
`candidate-response.schema.json`，不允许额外文件。`package_id` 为 `sha256:` 加 canonical
manifest payload 的 SHA-256；canonical payload 排除 package_id 本身。Manifest 是交接包的完整
语义真源：除 adapter、状态、事件与 Evidence 外，还必须包含 `task`（title、objective、
acceptance）和 `target`（id、name、role）。这些字段全部参与 package_id。同一输入必须生成
完全相同的四个文件；任务、目标、事件、Evidence/file hash、版本或目标变化必须改变 ID。

导出前扫描所有可迁移内容中的凭据、本机路径、file URI 和控制字符；命中时只报告来源 ID，
不输出疑似秘密。输出目录必须不存在，且不能是仓库根、`collaboration/`、其下路径、已有文件
或已有符号链接。发布器不重命名、移动、删除或覆盖已有目录；发布后重新严格验证四个文件、
manifest、由 Manifest 重新渲染的 handoff.md、严格等于初始模板的 candidate-response.json、
schema、package_id 与目录无额外文件。后验验证失败返回
`ErrHandoffOutcomeUnknown`，目录保留，不能对同一路径盲目重试。

`handoff.md` 固定包括协议边界、目标客户端、目标、验收、状态、责任方、事件、Evidence、
相对文件校验值、允许动作、回写方式和客户端输出要求。它只接收已验证 Manifest；固定的
adapter 输出要求由 Manifest.adapter 推导。任务目标、验收、历史
Event body 与 Evidence summary 均按缩进 JSON data block 渲染，不能创建新的协议标题或关闭
Markdown fence。manifest format_version 为 `1`，并记录 adapter、target、action_actor、
任务/项目、revision、状态、版本、游标、责任方、allowed_actions、事件增量与 Evidence 索引。

包内的 `candidate-response.json` 只能是未填写的严格初始模板：action、next_assignee、message、
feedback 为空，evidence_refs 与 evidence 为非 null 的空数组。它用于包完整性校验，不能直接通过
`response validate`。包外的真实候选响应包含 next_assignee、message、feedback、evidence_refs 与
candidate evidence，并按 action 语义校验：assign 必须给合法 next_assignee；message/request_changes/
evidence_add 分别只给非空 message/feedback/evidence；submit 必须引用至少一项 diff 或 artifact 和
一项 test；block 必须引用 blocker；accept/resume/approve 不得携带 payload。引用只能来自 Manifest
已公告 Evidence 或当前候选 Evidence，且 ID 不可重复或冲突。

`collab response validate` 只读取包和输入，校验 schema、package_id、task、观察到的
version/cursor、actor、allowed action、动作语义与 portable Evidence；成功时输出
`steps: [{program, args}]`。每个参数都保留为独立 args 元素，文本模式明确标记“仅供人工审核，
不会自动执行”。它不创建 Evidence、不改变 State，也不执行客户端动作。

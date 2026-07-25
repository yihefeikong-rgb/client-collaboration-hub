# Registry、Evidence 与 CLI v1 设计

## 目标

在当前工作目录的 `collaboration/` 下提供第一个可实际运行的 `collab.exe`。它能注册
Codex、CC-HAHA 等逻辑客户端，建立项目与任务，保存不可变证据，并通过命令行完成一次
含返工的审查闭环。它不控制任何客户端，也不实现 AO、handoff、binding 或 GUI。

## 根目录与布局

CLI 的唯一根目录是进程当前工作目录；不会搜索父目录、读取全局配置或接受另一个隐式根。
`collab init` 幂等创建：

```text
collaboration/
  projects/
  clients/
  tasks/
  bindings/
  .runtime/
```

它只追加缺失的 `.gitignore` 条目：`collaboration/.runtime/`、
`collaboration/bindings/` 与 `collab.exe`。可迁移事实只保存在项目、客户端、任务、事件和
证据文件中。

## 组件边界

`RegistryStore` 是项目和客户端 YAML 的唯一写入口。项目和客户端分别使用 scoped lock，
在目标文件同目录创建临时文件，使用完整写入、Sync、Close 和原子替换发布。读取使用严格
YAML 解码，`RegistryStore` 同时实现 `protocol.References`。

`EvidenceStore` 管理 `tasks/<task-id>/evidence/<evidence-id>.json`。Evidence 是不可变 JSON：
ID、task ID、kind、summary、file refs、创建者与 UTC 创建时间均经过验证。重复 ID 的同内容
写入视为幂等；不同内容是冲突。可孤立存在但尚未被事件引用的有效证据不是 Journal 损坏。

`FileTaskJournal` 注入 `protocol.References` 和 `EvidenceStore`。任务创建必须校验项目、
创建者、审查者及其 `create_task`/`review` capability，且唯一时间真源是
`Task.CreatedAt`。公共写 API 接收业务意图，不接收 Event、State 或 EvidenceKind。

`internal/cli` 仅解析命令、调用 Store/Journal、映射错误到退出码并格式化输出。它通过注入
Clock 保证所有新时间是 UTC；用户不能传 Event ID、事件类型、State、EvidenceKind 或事件时间。

## 事务与证据

`AddEvidence` 在任务锁内先验证版本、客户端和 Evidence，再原子写入证据文件。若日志尚未
包含该 evidence ID，它追加 `evidence_added` 事件并写入版本加一但业务字段不变的新 State。
若同内容 Evidence 已存在而事件未提交，则继续该事件发布；若事件已存在，则保持幂等并返回
现有 State。证据文件先于事件是允许的未引用事实；事件绝不引用不存在、其他任务或重复的
Evidence。

`CommitTransition` 接收 `TransitionIntent` 与 evidence IDs。Journal 在锁内加载每个 Evidence，
确认 ID 不重复、任务匹配，再从文件的 kind 推导内部 `TransitionRequest.EvidenceKinds`。因此
CLI 无法用伪造 kind 绕过 submit 或 block 的证据条件。

## 审计回放

`Replay(task, events, resolver)` 是 Inspect 的唯一语义判断。它从首条 `task_created` 建立
`DRAFT/v1`，然后逐条验证连续 Event ID、expected version、时间和角色：

- 状态事件映射到内部 `protocol.Transition`；assigned 使用 `target_client`，changes requested
  使用 body，submit/block 从真实 Evidence 推导 kind。
- `message_added` 与 `evidence_added` 不改变业务字段，但增加 version、last_event_id 和
  updated_at；证据事件必须精确引用一份匹配任务的 Evidence。
- 每个 message actor 必须是 creator、reviewer 或当前 assigned client，且已注册。

最终重建 State 必须与 `state.json` 完全相等；任意非法中间顺序、角色、证据或状态差异均为
`CORRUPT`。不存在整个任务目录单独返回 `ErrTaskNotFound`，不会伪装为损坏。

## CLI

实现 `init`、`client register`、`project create`、`task create`、`task assign`、`task accept`、
`task resume`、`message add`、`evidence add`、`task submit`、`review request-changes`、
`review approve`、`task block`、`status` 与 `recover`。成功状态输出固定字段：task ID、status、
version、last event ID、assigned client、responsible client、updated at。`--json` 时只输出严格
JSON；普通模式输出键值文本。

退出码固定为：0 成功，2 参数/权限/状态/引用校验失败，3 版本冲突，4 I/O 或内部错误，
6 可恢复尾部，7 损坏，8 提交结果未知，9 资源不存在。结果未知会明确要求先运行
`collab status`，不建议重试。

## 验收

测试覆盖严格模型、原子创建/幂等、伪造 EvidenceKinds 拒绝、完整回放、无关消息发送者、
不存在与损坏的区分、每类 CLI 退出码和 JSON。集成测试只能调用 CLI，在临时目录完成：init、
注册两个客户端、创建项目/任务、指派、接受、添加 diff/test 证据、提交、要求返工、恢复工作、
添加新证据、再次提交、审批和 status DONE。最后运行格式、vet、全部测试、race、build 及
Windows/Ubuntu CI。

# 试运行就绪交接包设计

## 目标与边界

本阶段使文件协作中枢可以安全地进入人工双客户端试运行。Codex 和 CC-HAHA 保留各自的
原生界面、会话、登录态、技能和 MCP；中枢只生成、校验和记录可审计的文件协议数据。
不实现自动导入、自然语言到 Event 的自动转换、AO、ConPTY、sidecar、GUI 或客户端控制。

交接包是待人工处理的资料，不是对客户端或操作者的控制指令。任务目标、验收标准、历史
消息/Event body 和 Evidence summary 都按不可信数据渲染，不能改变固定协议边界或建议命令。

## 方案选择

采用“不可覆盖发布 + 单一动作策略 + 只读候选响应校验”的方案。它保留已有文件状态机和
手工 CLI 回写路径，避免引入新的服务、队列或客户端自动化。

导出目录必须不存在。发布器拒绝仓库根、`collaboration/`、其下任意路径、已有文件、已有
符号链接和已有用户目录；不会重命名、移动、删除或覆盖用户目录。正式发布后重新读取并
验证包。若该验证失败，返回 `ErrHandoffOutcomeUnknown`，保留目录，操作者必须检查结果，
不能对同一目录盲目重试。

## 统一 Action Policy

`protocol.ActionPolicy` 是 Journal、Replay、TaskQuery 和 Handoff 唯一的角色、能力与状态
许可来源：

```go
type ActionPolicy interface {
    AllowedActions(Task, State, string, References) []Action
    Authorize(Task, State, string, Action, References) error
}
```

State transition 仅应用业务状态和 payload 约束；它不再单独维护 actor 的角色判断。
`Authorize` 统一检查注册、capability、creator/reviewer/assigned/responsible 关系与状态。
Query 按明确的 `action_actor` 生成 allowed actions；Handoff 使用目标客户端作为
`action_actor`，建议命令也始终使用该 actor。

`BLOCKED` 保留 `assigned_client` 与阻塞前的 `responsible_client`。一般交接目标仍是责任方；
但 `manual-codex` 可为具备 assign 权限的 creator 导出，以便重新指派。manifest 明确记录
`action_actor`，不把它与 `responsible_client` 混同。

`DONE` 是终态：只允许 status/query 与只读 handoff，拒绝 transition、message 和 evidence
写入。其他非终态中 message/evidence 仅允许任务参与者，且同样由 Action Policy 判定。

## 交接包与候选响应

包固定包含 `handoff.md`、`manifest.json`、`candidate-response.json` 和
`candidate-response.schema.json`，且不允许额外文件。Manifest 是包的完整语义真源：`task`
包含 title、objective、acceptance，`target` 包含 id、name、role，连同 adapter、状态、事件和
Evidence 一起进入 canonical payload。manifest 的 `package_id` 是 `sha256:` 加 canonical JSON
后的 SHA-256；canonical payload 排除 `package_id` 自身且只含可迁移、确定排序的字段。因此同一
输入会产生完全一致的四个文件与 ID；任一任务、目标、事件、Evidence/file hash、版本或游标变化
都会改变 ID。

`handoff.md` 只能从已验证 Manifest 确定性生成；验证包时必须重新生成并逐字节比较。包内
`candidate-response.json` 是严格的未填写模板，固定为空 action、空文本和非 null 空数组；验证时
也必须与程序生成模板逐字节一致。它不能作为真实响应通过 `response validate`。

包外候选响应是待人工审核的 JSON。`collab response validate --package <dir> --input <file>`
只读取包和输入，严格校验 schema、包身份、task/version/cursor、actor、allowed action、动作级
语义和 portable Evidence；成功时输出 `{program, args}` 结构化步骤，绝不写 Journal、创建
Evidence、执行步骤或改变 State。文本模式只能标记为“仅供人工审核，不会自动执行”。

`assign` 仅接受合法 next_assignee；`accept`、`resume`、`approve` 不接受业务 payload；
`message`、`request_changes`、`evidence_add` 分别要求唯一的非空 message、feedback、evidence；
`submit` 引用的 Evidence 必须至少覆盖 diff/artifact 和 test；`block` 至少引用 blocker。引用只能
来自包中已公告 Evidence 或同一候选响应中的 Evidence，所有 ID、引用和文件引用都不得重复。

Handoff Markdown 的固定标题、协议边界和回写说明由程序生成，adapter 输出要求只由
Manifest.adapter 推导。所有不可信业务文本以单行 JSON
并缩进为 Markdown data block，不进入标题、列表结构或 fenced code block，因此其中的 heading
和 backtick 无法关闭或新增协议部分。

## Binding 哈希边界

`FileBindingResolver` 默认仅哈希不超过 64 MiB 的普通文件；该值可通过 App 构造配置注入。
它从已打开句柄获取大小，读循环检查 context，并在哈希前后比较同一文件标识、大小和修改时间。
发现并发变化时拒绝导出。

这是可信本地 worktree 上的并发变化检测，不宣称能够抵御所有恶意竞态（例如缺少平台专用
nofollow/openat/最终路径句柄验证的环境）。该限制在协议与试运行文档中明确说明。

## 验证范围

测试覆盖：禁止 force 与危险输出不变性；所有角色/状态的 Action Policy 双向一致性；BLOCKED
creator 交接与无关客户端拒绝；DONE 终态；`import_export` capability；确定 package ID；四个
文件的确定性、handoff/template 任意修改拒绝、候选 action 语义、实参化结构化步骤与无写入验证；
Markdown 注入；绑定大小、取消和变化检测；发布后验证结果未知；以及 Windows/Ubuntu 的二进制
E2E（Ubuntu 继续运行 race）。

真实客户端试运行另以 runbook 定义。只有用户提供真实客户端输出后才可报告 `PILOT_PASSED`；
本阶段的完成状态为 `PILOT_READY`。

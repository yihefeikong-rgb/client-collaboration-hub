# Binding、交接包与手工客户端适配设计

## 目标与边界

本阶段把现有可运行的文件状态机 CLI 升级为两个独立客户端可人工协作使用的中枢。Codex
与 CC-HAHA 仍各自保留原生 UI、会话、登录态、技能和 MCP；系统只生成可审计、可迁移的
交接包，绝不启动、控制或读取任一客户端内部状态。

本阶段不实现 AO、ConPTY、sidecar、GUI、自动 import，或将自然语言自动转换为 Journal
事件。Binding 是被 `.gitignore` 排除的设备本地数据；portable 数据绝不包含本机绝对路径、
PID、会话标识或凭据。

## 先修复的协议不变量

`assigned` 的 actor 必须是创建者并具备 `create_task`；目标客户端必须已注册且具备
`execute`。写入路径与 Replay 使用同一验证，避免篡改日志可回放。

Evidence 只有在唯一的 `evidence_added` 事件公告后才可被 `submitted` 或 `blocked` 引用。
Journal 内的 `AddEvidence` 区分未公告孤立文件、已公告同内容重试和冲突内容：前两者分别
补写一次事件或直接返回当前 State，后者拒绝。Replay 发现重复公告或未来公告引用即为
`CORRUPT`。

FileRef 是使用 `/` 的项目相对路径。协议拒绝绝对路径、盘符/UNC、`..`、空段、反斜杠、
URL/file scheme、控制字符与疑似凭据。可迁移文本字段共用敏感内容扫描，防止消息、反馈和
Evidence 摘要写入明显的 token、密码或本机路径。

## 本地 Binding 与安全解析

`BindingStore` 在 `collaboration/bindings/<device-id>/<project-id>.local.json` 保存
`ProjectBinding`。它验证项目存在、路径存在且为目录，将路径规范为绝对路径，并用
`.runtime/locks/bindings/<device>/<project>.lock` 对单个 binding 原子更新。binding-status
只公开可用性、项目、设备和 revision，不显示 local_path。

`BindingResolver` 以 binding 根目录解析 portable FileRef：先 clean，再在文件存在时
EvalSymlinks，最后比较最终真实路径是否仍位于真实 binding 根内。不存在的文件是
`unavailable`，不是 Journal 损坏；可用文件导出 `relative_ref`、`size`、`sha256` 与
`available`，从不导出绝对路径。

## 只读查询与导出

`TaskQuery.Snapshot` 在任务锁中读取 Registry、健康状态、事件和首次公告顺序的 Evidence，
并提供当前 allowed actions。游标必须介于 `0` 与 `last_event_id`；返回的事件严格满足
`event_id > afterEventID`。只有 `HEALTHY` 任务可导出。status 复用该查询并显示
allowed_actions 与 binding_available（不显示路径）。

交接导出由 `ClientAdapter.Export` 驱动，先扫描 portable 内容，再在临时目录完整生成后安全
发布。此早期两文件与 `--force` 覆盖设想已由
`2026-07-25-pilot-ready-handoff-design.md` 取代：包固定四个文件，输出目录必须不存在，绝不
替换用户目录。输出稳定排序，便于 diff 和审计。

`manual-cc-haha` 面向执行者：ASSIGNED 建议 accept，WORKING 建议 message、evidence add、
submit、block，REVISION_REQUIRED 建议 resume。`manual-codex` 面向创建者/审查者：REVIEW
聚焦证据、测试与变更摘要，仅建议当前合法的 approve、request-changes、block、message。
两个适配器仅产生交接包，操作者仍使用 CLI 回写。

## 错误语义与验证

`recover` 的损坏诊断会在文本和 JSON 中显示 health、reason 与本地 backup_path；备份失败
与损坏确认分开报告。JSON 错误始终为严格 JSON。

测试覆盖非法 assign、Evidence 公告及幂等、FileRef 穿越、Binding 原子性与逃逸、Snapshot
游标、交接包安全与确定性、角色化适配器、recover 诊断，以及通过已编译二进制运行的
Windows/Ubuntu E2E。E2E 走完整闭环并验证 manifest、哈希、游标、退出码和交接包中无
binding 绝对路径；Ubuntu 继续执行 race。

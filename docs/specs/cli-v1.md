# CLI v1

## 原则

命令名为 `collab`。它是所有正式状态变化的唯一入口：负责状态机校验、事件追加、
证据引用、乐观锁、临时文件原子替换和恢复。文件保持可读可审计，但人或客户端不得
直接编辑 `state.json`、`messages.jsonl` 或证据索引。

成功退出 `0`；参数/状态/引用校验失败退出 `2`；预期版本冲突退出 `3`；I/O 或原子
写入失败退出 `4`；本地 binding 缺失或不可用退出 `5`。所有失败均不得发布新状态。

## 命令面

| 命令 | 作用 | 必要输入 |
| --- | --- | --- |
| `collab init` | 创建 `collaboration/` 布局与忽略规则 | 根目录 |
| `collab project create` | 创建逻辑项目 | `--id --name` |
| `collab project bind` | 写入本机私有项目绑定 | `--project --path --device` |
| `collab task create` | 创建 `DRAFT` 任务 | `--id --project --title --objective --creator` |
| `collab task assign` | 从 `DRAFT` 或 `BLOCKED` 指派/重新指派 | `--task --client --expected-version` |
| `collab task accept` | 接受任务 | `--task --actor --expected-version` |
| `collab task resume` | 从 `REVISION_REQUIRED` 开始返工 | `--task --actor --expected-version` |
| `collab message add` | 追加非状态消息 | `--task --actor --body --expected-version` |
| `collab evidence add` | 写入并校验证据 | `--task --id --kind --summary` |
| `collab task submit` | 带证据提交审查 | `--task --actor --evidence --expected-version` |
| `collab review approve` | 审批为 `DONE` | `--task --actor --expected-version` |
| `collab review request-changes` | 进入返工 | `--task --actor --body --expected-version` |
| `collab task block` | 记录阻塞 | `--task --actor --evidence --expected-version` |
| `collab recover` | 显示未读事件与一致性状态 | `--task --after-event` |
| `collab handoff export` | 生成面向客户端的交接包 | `--task --client --output` |
| `collab status` | 读取状态及下一步合法动作 | `--task` |

所有读取版本并可能写入状态、事件或证据的命令均在任务锁内重新读取文件并要求
`--expected-version`。`message add` 也要求版本，确保事件 ID 与 state 的
`last_event_id` 同步。`evidence add` 不改变任务状态，但必须校验 task ID、证据 ID
唯一性及引用文件存在性。项目创建与客户端注册使用各自目录锁，而不是全局锁。

`task create` 默认 `reviewer=creator`，可用 `--reviewer` 覆盖。`review approve` 和
`review request-changes` 只允许该 `reviewer` 执行。

## 手工交接格式

`handoff export` 生成 Markdown，包含以下固定段落：

```text
任务：<id> / <title>
当前状态与版本：<status> / <version>
目标、验收标准、逻辑项目：...
责任方与待办：...
事件摘要与证据路径：...
允许的 CLI 回写命令：...
```

ManualCodexAdapter 导出审查包，ManualCCHahaAdapter 导出执行包。客户端输出只作为
候选事件；操作者调用相应 CLI 命令后，CLI 才接受或拒绝其内容。这样“手工”只指
交接，不是手改状态文件。

## 绑定隔离

`project bind` 仅写 `bindings/<device>/<project>.local.json`，记录本机规范化路径与
可选 revision。该目录必须被忽略，`status` 可以显示 binding 是否可用，但不得把
绝对路径写入 task、event、evidence 或 handoff 的可迁移正文。

# CLI v1

## 根目录、输出与时间

`collab` 始终以当前工作目录为项目根，在其中读写 `collaboration/`；不会搜索父目录或读取
全局配置。所有新时间由 CLI Clock 生成 UTC，用户不能传 Event 时间、Event ID、事件类型、
next State 或 EvidenceKinds。

所有状态变更成功时输出 task_id、status、version、last_event_id、assigned_client、
responsible_client、updated_at。全局 `--json` 输出单一 JSON 值，不混入日志文本。

## 命令

| 命令 | 必要输入 |
| --- | --- |
| `collab init` | 无；幂等建立目录并追加缺失 `.gitignore` 条目 |
| `collab client register` | `--id --name --capability`（可重复） |
| `collab project create` | `--id --name` |
| `collab task create` | `--id --project --title --objective --acceptance`（可重复）`--creator [--reviewer]` |
| `collab task assign` | `--task --client --expected-version`；actor 默认为 task creator |
| `collab task accept` / `resume` | `--task --actor --expected-version` |
| `collab message add` | `--task --actor --body --expected-version` |
| `collab evidence add` | `--task --id --kind --summary --created-by --expected-version [--file-ref]`（可重复） |
| `collab task submit` / `block` | `--task --actor --evidence`（可重复）`--expected-version` |
| `collab review request-changes` | `--task --actor --body --expected-version` |
| `collab review approve` | `--task --actor --expected-version` |
| `collab status` / `recover` | `--task` |

所有正式写入都经过 Journal。`evidence add` 是非业务状态事件；submit/block 只接收 Evidence
ID，由 Journal 读取文件后派生真实 kind。消息仅允许 creator、reviewer 或当前 assigned client。

`init` 创建 `projects/`、`clients/`、`tasks/`、`bindings/`、`.runtime/`，并且不覆盖已有
`.gitignore` 内容；只追加 `collaboration/.runtime/`、`collaboration/bindings/` 和 `collab.exe`。

## 退出码

| 代码 | 含义 |
| --- | --- |
| 0 | 成功 |
| 2 | 参数、权限、状态或引用校验失败 |
| 3 | expected-version 冲突 |
| 4 | I/O 或内部错误 |
| 5 | 本地 binding 不可用（预留） |
| 6 | `RECOVERABLE_TAIL`，需要 `collab recover` |
| 7 | `CORRUPT` |
| 8 | `ErrCommitOutcomeUnknown`；先运行 `collab status`，不得盲重试 |
| 9 | 资源不存在 |

本阶段不提供 `project bind`、handoff export、ManualCodexAdapter、ManualCCHahaAdapter、AO 或
任何客户端自动接入。

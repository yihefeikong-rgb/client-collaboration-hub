# CLI v1

## 根目录、输出与时间

`collab` 始终以当前工作目录为项目根，在其中读写 `collaboration/`；不会搜索父目录或读取
全局配置。所有新时间由 CLI Clock 生成 UTC，用户不能传 Event 时间、Event ID、事件类型、
next State 或 EvidenceKinds。

所有状态变更成功时输出 task_id、status、version、last_event_id、assigned_client、
responsible_client、updated_at。`evidence add` 额外输出 `changed`；同一已公告 Evidence 的
重试为 `changed: false`。全局 `--json` 输出单一 JSON 值，不混入日志文本。

## 命令

| 命令 | 必要输入 |
| --- | --- |
| `collab init` | 无；幂等建立目录并追加缺失 `.gitignore` 条目 |
| `collab client register` | `--id --name --capability`（可重复） |
| `collab project create` | `--id --name` |
| `collab project bind` | `--project --device --path [--revision]` |
| `collab project binding-status` | `--project --device`；只显示 available、project、device、revision |
| `collab task create` | `--id --project --title --objective --acceptance`（可重复）`--creator [--reviewer]` |
| `collab task assign` | `--task --client --expected-version`；actor 默认为 task creator |
| `collab task accept` / `resume` | `--task --actor --expected-version` |
| `collab message add` | `--task --actor --body --expected-version` |
| `collab evidence add` | `--task --id --kind --summary --created-by --expected-version [--file-ref]`（可重复） |
| `collab task submit` / `block` | `--task --actor --evidence`（可重复）`--expected-version` |
| `collab review request-changes` | `--task --actor --body --expected-version` |
| `collab review approve` | `--task --actor --expected-version` |
| `collab status` | `--task [--device]`；显示 allowed_actions 与 binding_available，不显示 local_path |
| `collab recover` | `--task` |
| `collab handoff export` | `--task --client --adapter --device --after-event --output [--force]` |

所有正式写入都经过 Journal。`evidence add` 是非业务状态事件；submit/block 只接收已公告
Evidence ID，由 Journal 读取文件后派生真实 kind。消息仅允许 creator、reviewer 或当前
assigned client。assign 同时验证 creator 的 create_task capability 与目标 execute capability。

`init` 创建 `projects/`、`clients/`、`tasks/`、`bindings/`、`.runtime/`，并且不覆盖已有
`.gitignore` 内容；只追加 `collaboration/.runtime/`、`collaboration/bindings/` 和 `collab.exe`。

handoff adapter 只支持 `manual-codex` 与 `manual-cc-haha`，并且只生成文件交接包；不会导入、
启动、控制或读取 Codex、CC-HAHA、AO 或任何客户端的内部状态。

handoff export 仅允许 HEALTHY 任务。它要求所选设备已绑定任务项目，输出目录默认不可覆盖。
manifest 与 handoff.md 是 portable 内容，不能包含 Binding 的绝对 local_path。`recover` 发现
CORRUPT 时会在文本或 JSON 中输出 health、reason 与本地诊断 backup_path；诊断备份失败时
额外显示其失败状态，不能掩盖已确认的 CORRUPT。

## 退出码

| 代码 | 含义 |
| --- | --- |
| 0 | 成功 |
| 2 | 参数、权限、状态或引用校验失败 |
| 3 | expected-version 冲突 |
| 4 | I/O 或内部错误 |
| 5 | 本地 binding 不可用 |
| 6 | `RECOVERABLE_TAIL`，需要 `collab recover` |
| 7 | `CORRUPT` |
| 8 | `ErrCommitOutcomeUnknown`；先运行 `collab status`，不得盲重试 |
| 9 | 资源不存在 |

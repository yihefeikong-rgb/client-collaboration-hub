# CLI v1

## 根目录、输出与时间

正式二进制默认在 `%LOCALAPPDATA%\client-collaboration-hub\` 读写统一的 `collaboration/`；
`COLLAB_HOME` 可为测试或隔离实例显式覆盖该目录。程序不再依赖当前工作目录，也不会搜索父目录。
所有新时间由 CLI Clock 生成 UTC，用户不能传 Event 时间、Event ID、事件类型、next State 或
EvidenceKinds。

所有状态变更成功时输出 task_id、status、version、last_event_id、assigned_client、
responsible_client、updated_at。`evidence add` 额外输出 `changed`；同一已公告 Evidence 的
重试为 `changed: false`。全局 `--json` 输出单一 JSON 值，不混入日志文本。

## 命令

| 命令 | 必要输入 |
| --- | --- |
| `collab init` | 无；幂等建立目录并追加缺失 `.gitignore` 条目 |
| `collab ui` | 无；仅在 `127.0.0.1:8567` 启动本地网页控制台；端口被占用时自动使用本机空闲端口并输出实际 URL |
| `collab mcp` | 无；通过 stdio 启动本机 MCP Server，stdout 仅用于 MCP 协议 |
| `collab client register` | `--id --name --capability`（可重复） |
| `collab project create` | `--id --name` |
| `collab project register-local` | `--path [--id] [--name]`；自动生成项目 ID、设备 ID 和本机 Binding |
| `collab project bind` | `--project --device --path [--revision]` |
| `collab project binding-status` | `--project --device`；只显示 available、project、device、revision |
| `collab task create` | `--id --project --title --objective --acceptance`（可重复）`--creator [--reviewer]` |
| `collab agent task-create` | `--input <task-candidate.json>`；先写入可审计回执，再按项目策略登记代理创建任务 |
| `collab agent submit` | `--package <handoff-directory> --input <candidate-response.json>`；先校验包与候选，再登记允许的代理事实 |
| `collab task assign` | `--task --client --expected-version`；actor 默认为 task creator |
| `collab task accept` / `resume` | `--task --actor --expected-version` |
| `collab message add` | `--task --actor --body --expected-version` |
| `collab evidence add` | `--task --id --kind --summary --created-by --expected-version [--file-ref]`（可重复） |
| `collab task submit` / `block` | `--task --actor --evidence`（可重复）`--expected-version` |
| `collab review request-changes` | `--task --actor --body --expected-version` |
| `collab review approve` | `--task --actor --expected-version` |
| `collab status` | `--task [--device] [--actor]`；显示 action_actor、该 actor 的 allowed_actions 与 binding_available，不显示 local_path |
| `collab recover` | `--task` |
| `collab handoff export` | `--task --client --adapter --device --after-event --output`；输出目录必须不存在 |
| `collab handoff next` | `--task`；自动推断当前目标客户端、唯一可用 Binding 与同一 `(task,target)` 的历史游标 |
| `collab response validate` | `--package <handoff-directory> --input <candidate-response.json>`；只读校验并输出结构化 `steps` |

所有正式写入都经过 Journal。`evidence add` 是非业务状态事件；submit/block 只接收已公告
Evidence ID，由 Journal 读取文件后派生真实 kind。消息与 evidence add 在非 DONE 状态仅允许
creator、reviewer 或当前 assigned client。所有动作由同一 Action Policy 授权；assign 同时验证
creator 的 create_task capability 与目标 execute capability。DONE 拒绝所有写入。

项目策略默认是 `submission_mode: agent_auto`、`final_review: human`、`auto_done: false`。
代理创建、接受、恢复、登记 Evidence、提交、阻塞和消息都必须以收件回执关联到 `origin: agent` 的
事件；候选校验失败会留下 `REJECTED` 回执且不会写入任务。代理不能 request-changes、approve 或推进
`DONE`。人工通过普通正式命令做的写入记录为 `origin: human`；在 human-final 策略中，最终审核
事件使用 `human_final` 决策标记。

`ui` 是当前工作目录中 `collaboration/` 的本地操作者界面。它只监听 loopback，以固定的命令
映射调用同一套 CLI 写入路径，不接受任意 shell 命令或任意 CLI 参数。默认页面是“待我审查”，
另外显示代理提交收件箱、任务事件和交接历史；不会启动、控制、读取或修改 Codex、CC-HAHA、AO
或其他客户端。网页写入口仅有人工最终审核与“生成下一份交接包”；代理的提交、Evidence 和消息
只能通过本地 `collab agent` 收件箱命令进入中枢。

`init` 创建 `projects/`、`clients/`、`tasks/`、`bindings/`、`.runtime/`，并且不覆盖已有
`.gitignore` 内容；只追加 `collaboration/.runtime/`、`collaboration/bindings/` 和 `collab.exe`。

handoff adapter 支持 `manual-codex`、`manual-cc-haha` 与 `manual-reasonix`，并且只生成文件
交接包；不会导入、启动、控制或读取 Codex、CC-HAHA、Reasonix、AO 或任何客户端的内部状态。
目标客户端除角色与当前动作权限外，必须具备 `import_export` capability。

handoff export 仅允许 HEALTHY 任务。它要求所选设备已绑定任务项目，输出目录必须不存在，
并且拒绝仓库根、`collaboration/`、其下路径、已有文件与已有符号链接；自动 `handoff next` 唯一
允许写入 `collaboration/.runtime/handoffs/` 下的受控目录；没有 `--force`。
manifest 与 handoff.md 是 portable 内容，不能包含 Binding 的绝对 local_path。成功输出的包固定
有四个文件，并在结果中显示 `package_id`。发布后验证失败返回未知结果，必须检查该目录，不能
重试相同路径。

`handoff next` 首次使用 `after_event=0`；后续只读取同一 task 与同一目标客户端的已验证历史包，
并使用其 `through_event` 作为游标。历史包被篡改、消失或无法重算 package_id 时会停止，而不是猜测
游标或覆盖目录。若本机存在零个或多个可用 Binding，也会停止并要求先处理 Binding 歧义。

`response validate` 不调用 Journal：它只读取已发布包和候选响应，验证 package_id、任务、
version/cursor、actor、allowed action、动作级语义、schema 与 portable Evidence，成功后输出
`steps` 数组；每项为独立的 `{program, args}`，不会生成或执行拼接的 shell 命令。文本输出仅供
人工审核，不会自动执行。包内的空 candidate-response 模板只能用于包验证；真实响应必须在包外
填写有效 action。只有显式调用 `agent submit` 的候选才会被再次校验，并在策略允许时登记为
`origin: agent` 事件；`response validate` 本身永远不会写入。`recover` 发现 CORRUPT 时会在文本或 JSON
中输出 health、reason 与本地诊断 backup_path；诊断备份失败时额外显示其失败状态，不能掩盖已确认的 CORRUPT。

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
| 8 | `ErrCommitOutcomeUnknown` 或 `ErrHandoffOutcomeUnknown`；前者先运行 `collab status`，后者检查已发布目录且不得重试同一路径 |
| 9 | 资源不存在 |

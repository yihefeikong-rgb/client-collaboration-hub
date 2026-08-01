# client-collaboration-hub

本机、可审计的多客户端协作中枢。它让 Codex、CC-HAHA 等**独立、平等的 AI 客户端**通过持久化任务状态、证据、事件和交接包协作，并保留人工最终审查。

这不是聊天机器人界面，不是 AI 助手产品，也不是 Agent 自动控制中心。中枢不控制任何客户端的界面、会话、登录态、技能或 MCP；它只管理协作事实。

## 设计边界

- 客户端各自保留原生界面、会话、上下文、技能和 MCP。
- 任务、消息、证据、事件、交接包全部持久化在本地，可审计、可迁移。
- Agent 可以创建和推进普通协作事实；**最终批准和要求返工由人操作**。
- 消息不是完成证据；只有登记后的 Evidence 和事件才是中枢事实。

任务状态机：

```text
DRAFT → ASSIGNED → WORKING → REVIEW
                            ↘ REVISION_REQUIRED → WORKING
REVIEW → DONE / BLOCKED
```

## 快速开始

### 1. 构建

需要 Go 1.25+。

```powershell
go build -o collab.exe ./cmd/collab
```

### 2. 登记项目

```powershell
.\collab.exe project register-local --id my-project --name "我的项目" --path "D:\path\to\project"
```

重复登记同一目录是幂等的。项目代码不会被复制进中枢，只记录本机路径绑定。

### 3. 启动网页控制台（人工审核台）

```powershell
.\scripts\start-web-console.ps1
```

或直接双击 `启动网页控制台.cmd`，然后浏览器打开 <http://127.0.0.1:8567>。

### 4. 让 AI 客户端接入 MCP

`collab.exe mcp` 是一个本地 stdio MCP 服务器，不开放网络端口，不需要 API Key。

**Codex**（`%USERPROFILE%\.codex\config.toml`）：

```toml
[mcp_servers.client-collaboration-hub]
command = 'D:\path\to\collab.exe'
args = ["mcp"]
default_tools_approval_mode = "writes"
startup_timeout_sec = 10
tool_timeout_sec = 60
```

**CC-HAHA / Claude Code**（PowerShell 执行一次）：

```powershell
claude mcp add --scope user client-collaboration-hub -- "D:\path\to\collab.exe" mcp
```

**Reasonix（RE）**（`%APPDATA%\reasonix\config.toml`）：

```toml
[[plugins]]
name = "client-collaboration-hub"
command = 'D:\path\to\collab.exe'
args = ["mcp"]
```

客户端连接后，先读取 `collab://manual/agent-operating-guide`，再开始调用工具。完整说明见 [docs/mcp/AI-OPERATING-GUIDE.md](docs/mcp/AI-OPERATING-GUIDE.md)。

客户端注册表是统一协议层：每个客户端声明角色（executor/reviewer/both）、支持的工作模式、
审批模式与模型档位。`collab watch` 未显式传参时采用注册表的默认工作模式，显式参数优先；
网页控制台的“已登记客户端”列表会展示这些声明。维护声明：

```powershell
.\collab.exe client register --id reasonix --name "Reasonix (RE)" --capability review --capability create_task --capability import_export --role reviewer --work-profile balanced --work-profile delivery --default-work-profile delivery --approval-mode auto --approval-mode yolo --default-approval-mode auto --update
```

### 5. 第一个任务

```powershell
.\collab.exe task create --id T-0001 --project my-project --title "任务标题" --objective "任务目标" --acceptance "验收标准1" --acceptance "验收标准2" --creator codex
.\collab.exe task assign --task T-0001 --client cc-haha --expected-version 1
```

分配后 CC-HAHA 通过 MCP 的 `collab_get_next_work` 看到任务，`collab_generate_handoff` 生成交接包，执行完成后 `collab_submit_candidate` 提交。Codex 在网页控制台批准完成或要求返工。

### 6. 启动唤醒服务（可选）

`collab watch` 会持续监视任务状态，并在任务需要推进时自动唤醒负责的 AI 客户端（CC-HAHA、Codex 或 Reasonix），方便电脑重启后快速恢复协作。

唤醒采用“一个任务固定一个 CC-HAHA 会话”的机制：首次唤醒时用确定性会话 ID 创建会话，之后的返工或补充消息都会恢复同一个会话继续对话，不会每次重新开一个空白上下文。在网页控制台给任务发送补充消息后，watch 会自动唤醒负责客户端并把消息带进同一会话；能够确认未投递的唤醒失败会自动退避重试（默认 60 秒）。

Reasonix 作为 `reasonix` 审查客户端时，需要运行带“桌面协作桥”的 RE 桌面版。`collab watch` 不会启动后台 RE CLI：它会发现 `%APPDATA%\reasonix\desktop-collaboration-bridge.json`，先核对 RE 进程、协议主版本和必需能力，再按“工作区 + 任务 ID”确定性地创建或续接同一个 RE 话题，并通过 RE 自己的用户消息提交路径发送审核请求，因此该消息和后续回复会立即出现在前台对话中。任何不兼容或不可用状态都不会发送 turn，也绝不回退为不可见的后台 CLI 会话。正式运行固定默认“常规执行 / 交付工作模式 / 自动审批”；若需联调 RE，可显式使用 `--reasonix-work-profile balanced`，仅允许 `balanced` 或 `delivery`，桥接回执必须确认同一工作模式。RE 原生的自动上下文压缩继续负责长对话总结。创建任务时指定 `--reviewer reasonix` 即可让 `REVIEW` 阶段唤醒它；最终批准与要求返工仍由人工网页完成。协议字段与失败边界见 [desktop-collaboration-bridge/v1](docs/specs/desktop-collaboration-bridge-v1.md)；只读检查可运行 `collab adapter doctor --client reasonix`（或 `--client all`，不会调用 CC 的旧接口）。

为避免网络中断时重复插入用户消息，桌面投递只有收到同一 `delivery_id` 的明确回执才视为成功。其他不确定结果会暂停该次投递，并在日志中打印 `delivery_id`，不会自动重投。先停止 watch，再由人工核对前台会话后执行其一：确认已投递用 `collab watch delivery resolve --delivery <id> --actor operator --note "已核对"`；确认未投递、允许下一轮重发用 `collab watch delivery abandon --delivery <id> --actor operator --note "未投递"`。两种决定都会写入本地审计记录。

```powershell
.\collab.exe watch --reasonix-work-profile balanced
```

一键后台启动：

```powershell
.\scripts\start-watch.ps1 -Root "D:\path\to\project"
```

或直接双击 `启动自动唤醒.cmd`。脚本会后台启动 `collab.exe watch`，输出 watch 进程 PID 和日志文件路径（位于项目 `logs\` 目录）；若 watch 已在运行则提示后直接退出，不会重复启动。

脚本优先使用项目目录下的 `collab.exe`，找不到时会回退到全局安装目录或 PATH。想让任何项目文件夹都能使用，可执行：

```powershell
.\scripts\install-global.ps1
```

该脚本把 `collab.exe` 复制到 `%LOCALAPPDATA%\Programs\client-collaboration-hub\` 并加入用户 PATH；数据仍在全局目录，客户端会话始终在各自项目的 Binding 目录中创建。

## 数据位置

默认全局数据目录：

```text
%LOCALAPPDATA%\client-collaboration-hub\
```

可通过环境变量 `COLLAB_HOME` 覆盖。数据包含项目、客户端、任务、绑定、事件、证据和交接包历史；不要直接手工修改其中的 Journal 文件。

## 常用命令

```text
collab init
collab project register-local / bind / binding-status
collab task create / assign / accept / resume / submit / block
collab evidence add
collab review request-changes / approve
collab message add
collab handoff export / next
collab response validate
collab status --task <id>
collab ui
collab mcp
collab version
```

## 目录结构

```text
cmd/collab/             CLI 入口
internal/cli/           CLI、MCP 服务器、网页控制台命令
internal/protocol/      状态机、策略、可移植文本校验
internal/store/         Journal、Evidence、Registry、Binding 持久化
internal/handoff/       交接包导出、候选响应校验、历史
internal/agentintake/   Agent 提交回执与权限策略
internal/webconsole/    人工审核台（HTTP + 静态资源）
internal/version/       单一版本源
docs/mcp/               Agent 操作说明与客户端接入文档
docs/superpowers/       设计与计划文档
scripts/                启动与端到端测试脚本
```

## 测试

```powershell
go vet ./...
go test ./...
go build ./cmd/collab
.\scripts\e2e-cli.ps1        # Windows 二进制端到端
bash scripts/e2e-cli.sh      # Linux 二进制端到端
```

## 安全与审计要点

- Journal 是追加式事件账本，事件不可覆盖删除；损坏状态会明确标记而不是静默修复。
- 交接包只包含可迁移内容，不写入本机绝对路径、PID、PTY、会话、登录态或凭据。
- 候选响应只被校验和展示为人工步骤，验证器不拼接命令、不自动执行。
- MCP 工具清单不包含批准/返工动作，这两类动作仅存在于网页控制台和人工 CLI。

## License

MIT License，见 [LICENSE](LICENSE)。

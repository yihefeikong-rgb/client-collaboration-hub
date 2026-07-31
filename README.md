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

客户端连接后，先读取 `collab://manual/agent-operating-guide`，再开始调用工具。完整说明见 [docs/mcp/AI-OPERATING-GUIDE.md](docs/mcp/AI-OPERATING-GUIDE.md)。

### 5. 第一个任务

```powershell
.\collab.exe task create --id T-0001 --project my-project --title "任务标题" --objective "任务目标" --acceptance "验收标准1" --acceptance "验收标准2" --creator codex
.\collab.exe task assign --task T-0001 --client cc-haha --expected-version 1
```

分配后 CC-HAHA 通过 MCP 的 `collab_get_next_work` 看到任务，`collab_generate_handoff` 生成交接包，执行完成后 `collab_submit_candidate` 提交。Codex 在网页控制台批准完成或要求返工。

### 6. 启动唤醒服务（可选）

`collab watch` 会持续监视任务状态，并在任务需要推进时自动唤醒负责的 AI 客户端（CC-HAHA 或 Codex），方便电脑重启后快速恢复无人值守协作。

一键后台启动：

```powershell
.\scripts\start-watch.ps1 -Root "D:\path\to\project"
```

或直接双击 `启动自动唤醒.cmd`。脚本会后台启动 `collab.exe watch`，输出 watch 进程 PID 和日志文件路径（位于项目 `logs\` 目录）；若 watch 已在运行则提示后直接退出，不会重复启动。

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

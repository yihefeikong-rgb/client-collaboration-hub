# 连接本地 MCP

`client-collaboration-hub` 使用本地 stdio MCP。客户端启动 `collab.exe mcp` 后，通过标准输入输出交换
MCP 消息；不开放网络端口，不需要 API Key。

## Codex

在 PowerShell 中执行一次：

```powershell
codex mcp add client-collaboration-hub -- `
  "D:\tools\client-collaboration-hub\collab.exe" mcp
```

然后重启 Codex。可用以下命令检查：

```powershell
codex mcp list
```

Codex App、CLI 和 IDE 扩展共享同一 MCP 配置。也可以在 Codex 设置中的 “MCP servers” 添加一个
STDIO Server：

```text
command: D:\tools\client-collaboration-hub\collab.exe
args:    mcp
```

建议在 `%USERPROFILE%\.codex\config.toml` 中保留写工具确认：

```toml
[mcp_servers.client-collaboration-hub]
command = 'D:\tools\client-collaboration-hub\collab.exe'
args = ["mcp"]
default_tools_approval_mode = "writes"
startup_timeout_sec = 10
tool_timeout_sec = 60
```

`writes` 会允许只读查询直接运行，并在登记项目、创建任务、生成交接包或提交候选前要求确认。

## 其他支持 stdio MCP 的客户端

使用等价配置：

```json
{
  "mcpServers": {
    "client-collaboration-hub": {
      "command": "D:\\tools\\client-collaboration-hub\\collab.exe",
      "args": ["mcp"]
    }
  }
}
```

不同客户端的配置文件位置可能不同，但 command 和 args 不变。不要把 `collab.exe mcp` 当成普通
交互式终端命令长期手工运行；它由 MCP Host 启动和管理。

将示例中的 `D:\tools\client-collaboration-hub\collab.exe` 替换为本机真实 `collab.exe` 路径。

## 数据位置

默认全局数据目录：

```text
%LOCALAPPDATA%\client-collaboration-hub\
```

如测试需要隔离，可仅为 MCP Server 设置：

```text
COLLAB_HOME=D:\temporary\collab-test-home
```

不要让两个生产 MCP 配置使用不同的 `COLLAB_HOME`，否则它们会看到两套不同任务。

## 连接后第一步

让 Agent 先读取：

```text
collab://manual/agent-operating-guide
```

然后调用 `collab_list_projects`。最终批准和要求返工不会出现在 MCP 工具清单中，仍由用户在网页
控制台操作。

# desktop-collaboration-bridge/v1

这是协作中枢与 Reasonix（RE）桌面端之间的最小本机协议。它只用于让中枢把一条任务提示写入**可见的 RE 用户对话**；不控制 RE 的原生会话、登录态、技能或界面。

## 发现与认证

RE 启动时，在当前用户的 Reasonix 状态目录发布 `desktop-collaboration-bridge.json`：

```json
{
  "version": 1,
  "endpoint": "http://127.0.0.1:43123",
  "token": "每次桌面启动轮换的随机值",
  "pid": 1234
}
```

`version` 是发现文件格式版本，不是下面的协议主版本。中枢必须先严格验证该文件、回环 HTTP 地址和 RE 桌面进程 PID；不得记录或显示 `token`。所有请求必须带 `Authorization: Bearer <token>`，未认证请求必须得到 `401`。

### Windows 进程信任

发现文件只提供 PID、回环端点和短期 token，**不能**提供或覆盖可执行文件路径、安装根目录或开发信任设置。中枢取得 PID 后会解析桌面进程的实际映像路径；无法完成该校验时 fail-closed，绝不访问该发现文件所指的 HTTP 服务。

生产安装仅接受以下 Windows 已知目录中的精确文件名 `reasonix-desktop.exe`，并要求系统 Authenticode 校验成功：

- `%LocalAppData%\Programs\Reasonix\reasonix-desktop.exe`（由 Windows Known Folder API 取得，不读取环境变量）；
- `%ProgramFiles%\Reasonix\reasonix-desktop.exe`（同上）。

路径会在比较前解析链接；`C:\Temp` 及其子目录在解析前后都明确拒绝。便携版、复制到其他目录的版本、非标准安装、未签名或不受系统信任的生产可执行文件都会得到 `INCOMPATIBLE`，中枢和 `collab adapter doctor` 都不会发送 turn。

本地源码构建是唯一例外。启动 **Hub 进程前**，开发人员必须显式设置：

```powershell
$env:COLLAB_REASONIX_DESKTOP_DEV_ROOT = 'D:\path\to\DeepSeek-Reasonix'
```

该值必须是包含 `go.mod` 与 `desktop\go.mod` 的绝对源码根目录；中枢只接受其精确输出
`desktop\build\bin\reasonix-desktop.exe`，不接受直接指定 exe、任意临时目录或其他构建位置。此环境变量只影响 Hub 进程的开发信任根，不改变发现位置；`REASONIX_STATE_HOME`、`REASONIX_HOME`、发现 JSON 及其中任何字段均不能改变受信根。发布/生产运行应保持该变量未设置，并使用已签名的标准安装。

## 健康协商

在任何 `POST /v1/collaboration/turns` 之前，中枢必须调用经认证的
`GET /v1/collaboration/health`，且仅接受 `200`：

```json
{
  "status": "ok",
  "protocol": {
    "name": "desktop-collaboration-bridge",
    "major": 1,
    "minor": 0
  },
  "client": {
    "name": "reasonix",
    "version": "v1.2.3",
    "build": "0123456789abcdef0123456789abcdef01234567"
  },
  "capabilities": [
    "visible_user_turn",
    "delivery_idempotency",
    "profile_receipt"
  ],
  "collaboration_mode": "normal",
  "work_profile": "delivery",
  "tool_approval_mode": "auto"
}
```

`client.version` 和 `client.build` 必须非空。开发或无法取得构建标识的桌面端可以返回安全默认值 `unknown`；它们仅用于诊断，不是身份验证或更新授权依据。

v1 的中枢要求：

- `protocol.name` 为 `desktop-collaboration-bridge`，`protocol.major` 为 `1`；
- `client.name` 为 `reasonix`；
- 三项必需能力都存在：`visible_user_turn`（消息进入可见用户对话）、`delivery_idempotency`（同一 `delivery_id` 不重复创建消息）和 `profile_receipt`（回执确认 normal/work-profile/auto）；
- `minor` 允许向前扩展；v1 中枢只发送本文规定的字段。

主版本不匹配、客户端名不匹配或缺少必需能力时，中枢状态为 `INCOMPATIBLE`，必须 fail-closed：不发送 turn 请求，不把结果标记为不确定投递，也不退回后台 CLI。网络、认证或无效健康响应是 `UNAVAILABLE`，同样不得发送 turn 请求。

## Turn 与诊断

健康协商成功后，`POST /v1/collaboration/turns` 继续使用现有的 `delivery_id`、任务、工作区、提示和工作模式字段；成功回执必须回显同一 `delivery_id`，并确认 `normal/<选定 work_profile>/auto`。投递的不确定性与人工 resolve/abandon 规则不变。

`collab adapter doctor --client reasonix` 只读执行同一发现、PID 和健康协商；`--client all` 目前也只检查这个新 RE 协议，明确不会调用或猜测 CC-HAHA 的旧接口。诊断不得发送 `POST /turns`，不得写入协作存储。

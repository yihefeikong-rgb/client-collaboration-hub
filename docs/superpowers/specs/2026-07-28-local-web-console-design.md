# 本地网页控制台设计

## 决策与目标

为文件协作中枢新增一个仅在本机运行的网页控制台。它服务于人类操作者：集中查看任务、
交接包和候选响应，并以表单方式发起已有的正式写入动作。

控制台是 CLI 的本地操作界面，不是新的协作协议、远程服务或客户端适配器。Protocol v1、
Journal、Evidence、Handoff 的文件格式和状态机均保持不变。

本期命令入口为：

```text
collab ui
```

该命令从当前工作目录读取 `collaboration/`，在 `127.0.0.1` 的临时端口监听，并在终端输出
可手工打开的本地 URL。它不自动打开浏览器，也不监听局域网地址。

## 非目标

控制台不得实现以下功能：

- 启动、控制、读取或修改 Codex、CC-HAHA、AO、sidecar、ConPTY 或任何客户端内部状态；
- 自动 import、自动执行候选响应、自动将候选 JSON 转换为 Event；
- 远程访问、账户、多用户协同、局域网监听、WebSocket 推送或后台轮询；
- Git commit/push/merge、工作区自动修改、文件上传或任意 shell 命令执行；
- 修改 Protocol v1、状态机、Journal、Evidence 或 Handoff 文件格式。

候选响应的边界尤其保持不变：控制台只调用现有只读校验并显示 `{program, args}` 步骤；
不得提供“一键应用”“批量执行”或用候选 JSON 预填并直接提交正式写入的功能。

## 方案

采用 Go 内嵌静态资源 + 本地 HTTP 服务的单二进制方案，不引入 Node、npm、外部前端框架或
额外运行时。

```text
浏览器
  │  仅 localhost 的 JSON 请求
  ▼
collab ui（本机 HTTP 服务）
  │  白名单化的表单动作
  ▼
现有 CLI App / Registry / Journal / Handoff
  │
  ▼
collaboration/ 文件状态
```

网页后端不能接受任意命令字符串或任意 `args` 数组。每一种 UI 写操作都有独立、固定的请求
结构，后端只从经过校验的字段构建对应 CLI 参数数组，再调用同一套 CLI 业务入口。这样不会
创建绕过 CLI 校验、Journal、锁、Action Policy 或 Handoff 发布检查的第二条写入路径，也不会
产生 shell 注入面。

为了避免 `internal/cli` 与网页包产生导入环，网页包只依赖注入的两个窄接口：

```go
type CommandRunner interface {
    RunJSON(context.Context, []string) CommandResult
}

type ConsoleReader interface {
    Overview(context.Context, actor, device string) (Overview, error)
    Task(context.Context, taskID, actor, device string) (TaskView, error)
}
```

`cli` 包实现这两个接口：写入通过新建的 `App` 实例调用现有 JSON CLI 路径；读取通过受锁的
只读查询模型取得任务、事件、Evidence、客户端和项目摘要。网页包本身不直接写文件。

## 页面与可用动作

初版只提供三个页面，避免把 CLI 的每一个参数都重新设计为复杂产品界面。

### 1. 总览页

- 显示已注册客户端、项目和任务摘要；
- 每个任务显示 health、业务状态、version、last_event_id、责任方和 assigned client；
- 可选择本机 `actor` 与 `device`，据此显示该 actor 的 `allowed_actions` 和 binding 可用性；
- `RECOVERABLE_TAIL` 或 `CORRUPT` 时仅展示诊断，不显示普通写操作。

总览读取必须使用新的只读查询模型列举并严格解码已注册对象，不能把目录名或原始 YAML 直接
当作可信展示数据。它不返回 Binding 的 `local_path`。

### 2. 设置与任务页

此页提供以下显式表单：

- `init`；
- 注册客户端、创建项目、绑定项目；
- 创建任务；
- assign、accept、resume、message、evidence add、submit、block、request changes、approve。

表单只在当前 `allowed_actions` 包含对应动作时可用。每次任务写入都将当前已显示的
`expected_version` 原样提交；后端不能静默替换为更新后的版本，也不能自动重试。发生版本冲突、
恢复需求或结果未知时，页面显示原始安全错误并要求操作者刷新任务后再决定下一步。

`submit`、`block` 的 Evidence 引用必须由操作者逐项选择已公告 Evidence ID。Evidence add 与
后续 submit 是两个独立动作；控制台不会把它们合并为一个事务或猜测版本号。

项目绑定表单是唯一接收本机绝对路径的普通表单。该路径只传给现有 BindingStore，不能显示在
任务、事件、Evidence、交接包或可导出的页面数据中。

### 3. 交接与候选响应页

- 以新目录为前提导出 `manual-codex` 或 `manual-cc-haha` 交接包；
- 显示成功导出的 package_id、任务、游标和四个固定文件名；
- 接收操作者输入的本地 package 路径与包外候选 JSON 路径，执行只读 `response validate`；
- 将返回的每项 `{program, args}` 以不可执行的结构化列表显示，并标记“仅供人工审核”。

控制台不保存候选 JSON、不上传文件、不生成候选响应，也不提供把校验步骤送往正式写入表单的
按钮。操作者若要回写，必须返回设置与任务页重新填写独立表单并明确确认。

`recover` 不进入初版页面：它会截断可恢复尾部，继续保留为 CLI/RUNBOOK 中的显式诊断动作。

## 写入与并发语义

所有写操作继续由现有文件锁、Journal 事务与 Replay 校验保护。网页只处理其结果，规则如下：

- 成功：显示 CLI JSON 返回的最新 State 或 Handoff 报告，并刷新该任务视图；
- `ErrVersionConflict`：不重试，保留用户输入，要求刷新后重新确认；
- `ErrRecoveryRequired`、`ErrCorrupt`：冻结普通操作并展示 health/reason；
- `ErrCommitOutcomeUnknown` 或交接包结果未知：不重试，提示先执行只读 status/检查输出目录；
- 表单提交期间禁用同一表单按钮，服务端仍以 Journal 锁作为最终并发保护。

页面刷新是用户触发的只读动作；初版不轮询文件系统。这既避免不必要的锁竞争，也让文件状态
始终以明确刷新时刻为准。

## 本机安全与数据展示

服务必须固定绑定 `127.0.0.1`，拒绝非 loopback 的 listen 地址。写入 API 只接受 JSON、拒绝
CORS，并校验同源 `Origin` 和启动时生成的 anti-CSRF token。所有响应设置安全头，静态页面不
加载第三方脚本、字体或网络资源。

任务标题、目标、验收、消息、反馈和 Evidence 摘要均是未可信业务文本。前端必须以文本节点
渲染，不能使用 `innerHTML` 或 Markdown 渲染；错误信息也按文本展示。网页不得把本机路径、
绑定路径、令牌或客户端会话资料写入 `collaboration/`、交接包、浏览器日志或普通页面结果。

## 最小接口

页面使用版本化、同源的本地接口。具体 JSON 字段在实现时以现有 CLI JSON 输出为真源；不得
暴露通用命令执行接口。

```text
GET  /api/v1/overview?actor=<id>&device=<id>
GET  /api/v1/tasks/<task-id>?actor=<id>&device=<id>
POST /api/v1/init
POST /api/v1/clients
POST /api/v1/projects
POST /api/v1/bindings
POST /api/v1/tasks
POST /api/v1/tasks/<task-id>/actions/<action>
POST /api/v1/tasks/<task-id>/messages
POST /api/v1/tasks/<task-id>/evidence
POST /api/v1/handoffs
POST /api/v1/response-validations
```

`actions/<action>` 是服务器固定白名单中的 assign、accept、resume、submit、block、
request-changes 和 approve；未知动作返回验证错误。每个端点都有独立请求结构，不能复用为
“任意 CLI 代理”。

## 预期文件边界

实现预计只新增局部代码，不重构现有协议层：

```text
internal/webconsole/       # HTTP 服务、嵌入式静态页面、请求校验和只读视图
internal/webconsole/assets/# HTML、CSS、浏览器端 JavaScript
internal/cli/              # 增加 ui 命令和两个注入适配器
internal/store/            # 只读列举/控制台查询接口及测试
cmd/collab/                # 无结构性改动
docs/specs/cli-v1.md       # 记录 collab ui 的本地边界
docs/pilots/               # 追加网页控制台的人工操作说明
```

不修改 `internal/protocol/`、Journal 事件格式、Evidence 格式、Handoff manifest/schema 或真实试点
运行目录。

## 验收与测试

自动化验证在临时运行目录中完成，不读取或写入现有真实试点包：

1. `httptest` 覆盖 loopback 服务、静态资源、健康总览和任务详情；
2. 每个写表单至少覆盖成功、校验失败和 version conflict；
3. UI 写入后的 State、Event、Evidence 与直接 CLI 写入结果一致；
4. 候选响应校验前后任务 State/version/event 均不变，且页面没有执行步骤的 API；
5. 非 loopback 监听、跨域写请求、缺失/错误 CSRF token、未知 action 和通用命令路径被拒绝；
6. handoff 导出仍保留“四文件、目标目录不存在、后验验证”不变量；
7. `gofmt`、`go vet ./...`、`go test ./...`、`go build ./cmd/collab` 通过；
8. Windows 与 Ubuntu CI 的二进制 E2E 保持通过；新增 UI E2E 只使用临时目录和 HTTP 客户端。

## 延后事项

真实 CC-HAHA 响应到达并完成首次人工试点前，不扩大到桌面应用、远程访问、客户端自动化、
自动回写、权限账户、实时推送或更大范围的界面重构。当前阶段的交付状态仍为
`PILOT_READY`，不是 `PILOT_PASSED`。

## 2026-07-28 界面改版决策

网页控制台采用“工程任务账本 + 审查工作台”而非通用 Dashboard 或 Agent 运维大屏。
默认界面以浅色、高信息密度和稳定的三段层级为准：工作队列负责定位下一件需要人工处理的
任务；任务详情负责阅读目标、版本、Evidence 与追加式事件；上下文动作区只呈现当前状态和
当前 actor 实际允许的动作。

界面只能显示中枢已有的可验证事实。不得显示客户端“在线/思考中”、自动进度、虚构的系统
健康评分、周期指标、日历、优先级或任意未由协议提供的数据。客户端配置与 Binding 仅使用
“已配置”“本机 Binding 可用/未确认”等准确措辞。

首期视觉与交互规则：

- 左侧导航只保留工作队列、Evidence、交接包与设置四个稳定入口；
- 总览使用可筛选的任务表，突出状态、责任方、下一人工步骤、版本和数据健康度，不使用环形图
  或无行动价值的统计卡；
- 任务详情固定呈现任务身份、状态轨迹、expected/current version、责任归属与最后有效事件；
  Evidence 先显示其类型、来源与摘要，再按需展开原始信息；
- 所有写入表单移入上下文对话框或设置/交接二级页面，正式写入前显示前后状态、actor、版本与
  待写入动作的确认摘要；
- `response validate` 继续是只读动作；其结构化步骤只能显示、复制或导出，不能转化为一键执行；
- 深色主题、图表、命令面板、客户端启动入口和任何实时状态均不在本期实现。

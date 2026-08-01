# client-collaboration-hub：Agent 操作说明

## 你连接的是什么

这是一个本机、可审计的多客户端协作中枢。它保存项目、任务、状态、Evidence、事件、交接包和
Agent 提交回执。它不会控制 Codex、CC-HAHA 或其他客户端的界面、会话、登录态、技能和 MCP。

默认策略是：

```text
Agent 可以创建和推进普通协作事实
人类用户负责最终批准或要求返工
```

不要声称你完成了没有真实执行的代码修改、测试或外部操作。消息不是完成证据；只有登记后的
Evidence 和事件才是中枢事实。

## 开始工作前

1. 调用 `collab_list_projects`，确认逻辑项目和本机 Binding 已登记。
2. 调用 `collab_get_next_work`，查看当前由你负责的任务。
3. 对具体任务调用 `collab_get_task`，读取当前 `status`、`version`、`last_event_id`、验收条件和
   `allowed_actions`。
4. 状态或版本不确定时重新读取，不要猜测 expected version。

## 新项目

只有项目尚未登记时才调用 `collab_register_project`。提供真实存在的本机绝对目录；项目代码不会被
复制进中枢。重复登记相同目录是幂等的。

## 创建任务

使用 `collab_create_task`，提供：

- 清晰、可验证的标题和目标；
- 至少一条验收标准；
- 真实的创建客户端 ID；
- reviewer 通常为 `codex`。

任务创建成功后仍处于 `DRAFT`。根据任务允许动作生成交接包，让相应客户端提交下一条候选事实。

## 接收与提交工作

1. 调用 `collab_generate_handoff`。系统自动选择目标客户端、Binding 和事件游标。
2. 阅读返回的 `handoff.md`、Manifest 和候选模板。
3. 如果有多个客户端可能并行工作，先调用 `collab_task_claim` 认领任务的独立工作区
   （`worktree` 必须是真实存在的绝对目录，例如 `git worktree` 或独立副本），并确保
   后续所有修改只发生在这个目录；同一个任务同时只能有一个认领者。任务结束后调用
   `collab_task_claim` 并传 `release: true` 释放。
4. 在已认领的工作区（或未认领时的项目 Binding 目录）完成工作并运行真实测试。
5. 按模板产生候选响应，Evidence 应包含简短说明和真实的相对文件引用。
6. 调用 `collab_submit_candidate`。该工具会保存回执、校验版本和权限，再决定是否写入 Journal。
7. 若回执为 `REJECTED`，先修正原因；若为 `UNKNOWN`，调用 `collab_list_submissions` 和
   `collab_get_task` 核对，不要盲目重试。

## Evidence

Evidence 用于证明验收条件，而不是装饰。常用类型：

- `diff`：实际代码差异或补丁；
- `test`：真实执行的测试结果；
- `artifact`：构建物或报告；
- `log`：与结论直接相关的日志；
- `note`：无法由文件表达的人工或 Agent 说明。

只引用真实存在、位于所绑定项目内且允许打包的相对文件。不要写入令牌、密码、Cookie、会话 ID、
本机用户目录或客户端内部数据库。

## 冲突与失败

- 版本冲突：重新调用 `collab_get_task`，基于新版本重新生成交接包。
- Binding 不可用：停止并让用户在网页重新登记项目目录。
- 数据健康不是 `HEALTHY`：停止写入并报告用户，不要自行修改 Journal 文件。
- 候选被拒绝：读取持久化回执原因；被拒绝的候选没有改变任务。
- 结果为 `UNKNOWN`：先查询任务事件和 submission ID，确认是否已经写入。

## 终审模式

每个项目都有终审模式，见项目登记的 `collaboration_policy`：

- `final_review: human`（默认）：批准和要求返工只能由人在网页控制台执行。任务进入 `REVIEW`
  后，向用户说明已准备好的 Evidence 和测试结果，等待人工决定。
- `final_review: agent`：任务进入 `REVIEW` 后，担任 reviewer 的 Agent 可以在候选响应中提交
  `request_changes`（携带反馈）或 `approve`；批准后任务自动 DONE。每次策略切换都有人工审计记录。

无论哪种模式，你都不能：

- 任意修改状态；
- 删除或改写历史事件；
- 绕过 Evidence、版本、权限或策略校验；
- 冒充其他客户端或人类操作者提交动作。

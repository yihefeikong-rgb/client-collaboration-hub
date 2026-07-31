# Agent-first / 人工最终审核试运行 Runbook

## 状态与边界

当前默认策略是：代理可通过本机 `collab` 收件箱登记允许的协作事实；用户仍是唯一能做
`approve` 与 `request-changes` 的最终审查者。它不是客户端遥控器：不会读取、启动、控制 Codex
Desktop、CC-HAHA 的会话、登录态、技能、MCP 或数据库。

`PILOT_PASSED` 只能在至少一次真实任务完成“代理提交 → 用户要求返工 → 代理再次提交 → 用户批准”
后标记。本 runbook 只说明这条真实试运行链路。

## 一次配置

双击网页控制台启动器，在“设置与登记”填写一次真实项目目录。系统会在全局数据目录中自动完成
项目登记和本机 Binding；Codex、CC-HAHA 默认客户端会在第一次启动时自动建立。

也可以由 Agent 通过 MCP 的 `collab_register_project` 登记项目。

之后不需要在日常任务中填写设备、adapter、输出路径或 expected version。全局控制台可通过顶部
项目选择器切换项目；若同一项目存在多个可用 Binding，`handoff next` 会停止而不是猜测。

## 真实试运行流程

1. **让 Codex 创建任务。** Codex 可通过自己的原生会话准备一个任务候选 JSON，并在本机执行：

   ```text
   collab agent task-create --input <task-candidate.json>
   ```

   也可以只在第一次用普通 `collab task create` 建立任务。无论来源如何，收件箱都会留下可审计回执。

2. **生成下一份交接包。** 在网页控制台选中任务，点击“生成下一份交接包”；或执行：

   ```text
   collab handoff next --task T-0001
   ```

   系统自动推断下一位客户端、唯一可用 Binding 和事件游标，并把包写到
   `collaboration/.runtime/handoffs/`。这不会改变任务状态，也不会把包自动发送给任何客户端。

3. **把四个包文件交给目标客户端。** 在目标客户端自己的原生界面提供：

   ```text
   handoff.md
   manifest.json
   candidate-response.schema.json
   candidate-response.json
   ```

   目标客户端只在包外创建真实候选 JSON，例如 `candidate-response.cc-haha.json`。

4. **由代理登记候选。** 代理在项目根目录执行：

   ```text
   collab agent submit --package <交接包目录> --input <候选 JSON 路径>
   ```

   系统先保存 `RECEIVED` 回执，再校验包、版本、允许动作和项目策略。成功的允许动作会成为
   `origin: agent` 事件；失败候选会留下 `REJECTED` 回执，任务不会变化。

5. **用户只做最终审核。** 打开网页控制台的“待我审查”，阅读 Evidence、版本和追加式事件账本。
   在 `REVIEW` 状态下，只选择“批准完成”或“要求返工”。确认弹窗会显示将写入的版本和身份。
   网页不会提供代理的 `accept`、`submit`、Evidence 或消息填写表单。

6. **返工后重复。** 如果选择“要求返工”，再次生成下一份交接包，交给 CC-HAHA；它提交新
   Evidence 后重新进入你的审核队列。只有用户的批准会推进 `DONE`。

## 常见结果

- **没有可生成的交接包：** 先检查任务健康度和本机 Binding；零个或多个可用 Binding 都会被拒绝。
- **收件箱显示 REJECTED：** 打开“协作活动”读取原因，修正候选 JSON 或使用新交接包后重新提交。
- **看到旧版本 Evidence：** 不要批准；生成新包后让目标客户端按当前版本重新提交。
- **网页没有显示最新功能：** 重新双击 `启动网页控制台.cmd`。脚本会检测旧进程并启动新二进制。

## 保留的审计材料

保留交接包、收件回执、事件账本、Evidence、最终任务状态和项目 diff / 测试结果。不要把绝对路径、
令牌、会话 ID 或客户端内部资料写进交接包或候选 JSON。

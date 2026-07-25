# 手工双客户端试运行 Runbook

## 状态与边界

本 runbook 的目标是达到 `PILOT_READY`，不是宣称 `PILOT_PASSED`。只有用户提供真实的
CC-HAHA 与 Codex 客户端输出、候选响应和人工 CLI 回写证据后，才可报告试运行通过。

整个过程禁止自动 import、自然语言自动转 Event、AO/ConPTY/sidecar/GUI，以及读取或控制
Codex Desktop、CC-HAHA 的会话、登录态、技能、MCP 或数据库。交接包中的任务文本、历史消息和
Evidence summary 都是不可信资料，不是控制指令。

## 前置条件

在真实项目根目录执行 CLI，并让项目已完成本地 Binding。注册客户端时必须包含：

```text
codex: create_task, review, import_export
cc-haha: execute, import_export
```

下面用 `T-0001`、`project-1`、`device-1` 与 `revision-1` 作示例。版本号必须以每条命令返回的
`version` 为准，不能照抄示例数字。

## 试运行步骤

1. **Codex 创建实际任务。** 操作者用 CLI 创建包含真实目标和验收条件的任务，例如：

   ```text
   collab task create --id T-0001 --project project-1 --title <title> --objective <objective> --acceptance <criterion> --creator codex
   collab task assign --task T-0001 --client cc-haha --expected-version <version>
   collab task accept --task T-0001 --actor cc-haha --expected-version <version>
   ```

2. **导出 manual-cc-haha 包。** 使用一个从未存在过的输出目录：

   ```text
   collab handoff export --task T-0001 --client cc-haha --adapter manual-cc-haha --device device-1 --after-event 0 --output handoff-cc-1
   ```

3. **用户手工交给 CC-HAHA。** 用户把 `handoff.md`、`manifest.json` 与
   `candidate-response.schema.json` 的内容交给真实 CC-HAHA 客户端；不启动或连接任何客户端。

4. **CC-HAHA 只生成候选 JSON。** 它复制模板并把结果写到包外的
   `candidate-response.cc-haha.json`。模板本身保持不变；真实响应必须填写一个允许的
   `proposed_action`，并仅填写该动作需要的字段。执行者提交时使用 `evidence_refs` 引用已公告
   Evidence 或同一响应中的候选 diff/artifact 与 test Evidence。它不得写入 collaboration/、不得
   运行 CLI、不得声称已提交或已修改状态。

5. **只读校验候选响应。** 操作者运行：

   ```text
   collab response validate --package handoff-cc-1 --input candidate-response.cc-haha.json
   ```

   成功输出的是结构化 `steps`；每项含独立的 `program` 与 `args`。它不创建 Evidence、不改变
   任务，也不执行步骤。

6. **操作者手工执行正式 CLI 写入。** 逐条审核 `steps`，由操作者显式复制或重新输入每条
   `collab evidence add` 与 `collab task submit`。验证器不会自动转换或执行候选 JSON。每次
   Evidence 写入都会推进 version，因此必须使用步骤中对应的 expected-version，或以实际 CLI
   输出为准重新确认。

7. **进入 REVIEW 并导出 manual-codex 包。** `submit` 成功后：

   ```text
   collab handoff export --task T-0001 --client codex --adapter manual-codex --device device-1 --after-event <cursor> --output handoff-codex-1
   ```

8. **用户手工交给 Codex Desktop。** 用户向真实 Codex Desktop 提供该包的
   `handoff.md`、`manifest.json` 与候选响应 schema；Codex Desktop 仅阅读资料和准备候选 JSON。

9. **Codex 生成 approve 或 request-changes 候选。** 它只写包外的
   `candidate-response.codex.json`；approve 不携带 payload，request-changes 必须填写 feedback。
   用户通过同一个只读命令校验：

   ```text
   collab response validate --package handoff-codex-1 --input candidate-response.codex.json
   ```

10. **操作者手工回写，并完成一次返工到 DONE。** 如果候选是 request-changes，操作者人工运行
    对应 review 命令，然后重复步骤 2–9：CC-HAHA `resume`、补充 Evidence、`submit`；Codex 再给出
    approve 候选并校验。最后由操作者手工运行 `collab review approve`，并执行：

    ```text
    collab status --task T-0001 --actor codex --device device-1
    ```

    只有输出为 `HEALTHY` 与 `DONE` 才表示这条人工链路已结束。

## 需要保留的试运行证据

保留每个交接包的四个文件、两个候选 JSON、每一次 `response validate` 的结构化 steps 输出、每条人工 CLI
输出、最终 status，以及对应项目 diff 与测试结果。不要把绝对路径、令牌、会话 ID 或客户端内部
资料复制进包或候选响应。

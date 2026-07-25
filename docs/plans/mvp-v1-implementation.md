# MVP v1 实施状态

## 已完成

1. 严格 Project、Client、Task、State、Event 与 Evidence 模型。
2. RegistryStore 的项目/客户端原子创建、严格读取、引用与 capability 校验。
3. TaskJournal 的原子 task_created 初始化、版本链、锁、尾部恢复和结果未知语义。
4. EvidenceStore 的不可变、同内容幂等写入，以及从真实 Evidence 派生 submit/block kinds。
5. 完整审计 Replay：重建 State 并拒绝非法中间状态、无关参与者和不匹配 Evidence。
6. `collab` CLI：init、registry、任务、证据、审查、status 与 recover。
7. CLI 集成测试：init → 双客户端 → 项目 → 任务 → 指派 → 接受 → 证据 → 提交 →
   返工 → 修订 → 提交 → DONE。

## 验收边界

本阶段只交付文件中枢与命令行闭环。它不实现本地 binding、handoff、手工客户端适配器、AO、
ConPTY、sidecar、GUI、数据库服务、自动 commit/push/merge 或未验证的 Codex Desktop 控制。

## 下一阶段前提

进入 binding、handoff 或手工客户端适配器前，必须保持 Protocol v1 的逻辑身份与设备本地
状态分离，并新增相应的端到端验收，不得把本机路径、PID、PTY、登录态或 MCP 凭据写入
可迁移任务数据。

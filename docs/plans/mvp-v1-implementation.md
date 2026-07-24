# MVP v1 实施计划（仅计划，尚未开始编码）

## 成功标准

在独立仓库内，以文件协议和本地 CLI 跑通一次可审计的人工双客户端闭环：创建、
指派、执行、提交、一次返工、审批 DONE、换本地路径恢复。核心不导入或依赖 AO
的类型、数据库、session、harness、PTY、前端或运行时。

## 计划目录结构

```text
client-collaboration-hub/
  cmd/collab/                 # CLI 入口（后续）
  internal/protocol/          # 模型、状态机、事件验证（后续）
  internal/store/             # 文件读写、锁和原子替换（后续）
  internal/adapters/manual/   # Codex 与 CC-HAHA 手工交接（后续）
  testdata/demo-t0001/        # 演示 fixture（后续）
  collaboration/              # 运行时数据示例（后续）
  docs/specs/
  docs/plans/
```

目录仅是实现规划；本轮不创建代码、fixture 或运行时数据。

## 实施顺序与验证

1. Go module、文件模型、严格 YAML 与状态机 → 为每条合法/非法转换编写单元测试；验证不可越级、`reviewer` 和责任方限制。
2. 文件锁抽象与测试替身 → 验证每任务锁、项目锁、客户端锁的选择；协议层不依赖具体锁库。
3. 文件存储与原子写入 → 已实现业务意图型 `TaskJournal`、原子 task_created 初始化、JSONL 审计链检查、原子 State 替换、故障注入及唯一尾部截断恢复；`CORRUPT` 保持只读。
4. CLI 基础命令 → 后续验证输入、退出码、expected-version 冲突和证据引用失败。
5. 手工适配器 → 验证 export 的交接包不含绝对路径/凭据，import 的候选事件必须经 CLI。
6. 演示 fixture → 以端到端测试执行 `T-0001` 的完整返工和重新 binding。

## 测试矩阵

| 范畴 | 最小覆盖 |
| --- | --- |
| 状态机 | 每条合法转换；每种非法来源状态；错误 actor；缺少审查反馈或阻塞证据 |
| 乐观锁 | 当前版本成功；过期版本返回 3 且文件无变化；事件 ID 连续 |
| 进程锁 | 同任务串行、不同任务可并行；项目和客户端分别锁定 |
| 原子写入 | append 失败、临时写失败、替换失败均不发布新 state；recover 报告不一致 |
| 证据 | 缺失、跨任务、重复 ID、submit 缺 diff/artifact 或 test 均拒绝 |
| binding | 本机路径缺失返回 5；换路径重新 bind 不修改 task/event 数据 |
| 交接 | Codex/CC-HAHA 两种 Markdown 包都自包含；无 PID、PTY、绝对路径或凭据 |
| E2E | 创建→指派→接受→提交→返工→修订→审批→新路径恢复 |

## 明确排除

AO、ConPTY、CC-HAHA sidecar、真实自动控制、GUI、自动 commit/push/merge、数据库
服务、多客户端并行调度和任何未验证的 Codex Desktop 外部接口均不属于 MVP v1。

## 编码开始前的最小确认

已确定：Go 生成 Windows x64 单文件 `collab.exe`（未来保持跨平台可构建）；锁使用
`github.com/gofrs/flock` 的实现适配器并由接口隔离；YAML 使用 `gopkg.in/yaml.v3`、
`KnownFields(true)` 和显式 `Validate()`。第一阶段仅实现第 1、2 步，不实现事务、
CLI 正式命令、手工适配器或 fixture。

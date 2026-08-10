<p align="right"><a href="architecture.md">English</a> | <strong>简体中文</strong></p>

# 架构

AlpineForm 使用单向的核心边界：

```text
parser -> merge -> IR -> graph -> plan -> engine -> provider -> backend
                                      |                    |
                                      +------ state -------+
```

- `parser` 发现 HCL 和变量输入，并验证公开语法。
- `merge` 将 profile、component、表达式和默认值解析为 IR。
- `ir` 保存与 provider 无关的期望状态和源码位置。
- `graph` 分配稳定地址、依赖关系和变更触发器。
- `plan` 比较期望状态、先前状态和观测状态，不产生副作用。
- `engine` 调度 inspect、apply、check、取消和租约工作流。
- `provider` 负责 Alpine 和 BusyBox 的观测与收敛。
- `backend` 负责 OpenSSH 传输、原子远端 state 和运行时租约。
- `state` 验证 AlpineForm 信封和 schema 兼容性。

离线 plan 在 graph 编译后结束。在线 plan 首先仅编译经过验证的 SSH 身份，发现固定的
Alpine facts，使用这些 facts 重新编译完整程序，然后读取 state 并检查受管资源。因此，plan
来自观测状态，而不只是上一次的 state 快照。

Docker 被编译为 host domain，而不是兼容性 component。它复用原生 APK、group、membership
和 OpenRC provider；专用的 daemon-config 和 Compose provider 则保持先验证后变更以及运行时
观测。graph 在 daemon 之前安排 package/repository 就绪，把 daemon 变更聚合为一次 service
restart，并在显式 absence 时反转 project、service、configuration 和 package 的依赖关系。

一次 apply 是一项两次审查的事务：preview、获取租约、锁内重新 plan、批准、provider 操作，
以及原子 state 持久化。graph 安排依赖关系并聚合 `on_change` 或 service 触发器，所以即使多个
资源发生变化，每个已解析 declaration 在每台 host 上也只运行一次。

资源地址和 state schema 是兼容性表面；请参阅[兼容性政策](compatibility-policy.zh.md)。
目标端安全和脱敏边界在[安全模型](security-model.zh.md)中说明。

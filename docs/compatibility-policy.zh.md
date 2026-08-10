<p align="right"><a href="compatibility-policy.md">English</a> | <strong>简体中文</strong></p>

# 兼容性政策

AlpineForm `v0.1.0-alpha.5` 是预发布版本。本政策定义用户可以依赖的内容，同时避免把 alpha
行为描述为稳定行为。

## 版本管理

- tag 使用带前导 `v` 的语义化版本。
- Alpha release 可以在后续预发布版本中引入破坏性变更，但 release notes 必须指出受影响的
  CLI、DSL、资源地址、state、plan JSON、installer 或 artifact 契约，并提供迁移或回滚指导。
- 已发布的 tag 及其 release artifact 不可变。修正必须使用新的预发布 tag。
- 在非预发布 release 明确作出稳定性承诺之前，不承诺稳定兼容性。

## 配置和 CLI

接受的 block 名称、attribute、默认值、文件发现、变量优先级、命令名、flag、退出行为和人类可读
输出均为 alpha 接口。移除或更改这些接口时必须添加 release-note 条目。自动化程序应优先使用
plan JSON，而不是解析文本输出。

AlpineForm 独立进行版本管理。它不接受 `.dbf.hcl`、DebianForm 变量、DebianForm state 或
DebianForm 资源地址。

## Component Artifact Source

允许挂载的 component input 用于预构建的 `source.url` 和 `source.sha256`，是一项增量 alpha
DSL 变更。在执行 source 表达式求值和架构选择之前，会针对每个挂载 instance 规范化并验证
input。依赖 input 但未挂载的 template 仍会验证静态结构，不会伪造 input 值。

表达式边界不包含 component `type`、`version`、source label、`extract`、`build` 或
`install`；目标端 source build 保持其现有的独立语义。已有的 literal source 行为、以 checksum
为键的 cache、资源地址、desired/state 表示、state schema v3 和 `alpineform.plan.alpha1`
保持兼容。

受保护的已解析 URL 和 checksum 是内存 payload，而不是序列化后对兼容性可见的内容。它们的
稳定 cache identity 基于保留的物理 component identity 加上规范化 source label，绝不基于
原始或派生的受保护材料。更改这项 identity 规则，或者通过 graph、plan、state、diagnostic、
debug、error 或远端命令日志暴露受保护值，属于破坏性安全变更。

## 资源地址和 State

资源地址是持久化 identity。会重新解释现有地址的变更必须提供显式迁移，或者拒绝旧 state。
禁止静默重新分配。

顶层 `moved` block 是挂载 component instance 重命名的显式迁移。它们是一项增量 alpha DSL
功能，端点仍限于同一 host 上的静态 component root。兼容 move 会保留 ownership 和远端
object identity，在每台 host 上保持原子性，并在重试时保持幂等；它绝不会把仅 move 的重命名
变成远端资源操作。削弱端点或冲突验证、更改 payload rebasing，或者以违反上述属性的方式更改
apply 顺序，均属于破坏性安全变更。

State 包含 AlpineForm product marker、host identity、schema version、serial、facts 和受管资源。
decoder 会拒绝外部 product、未知的更新 schema 和 host 不匹配的 state。Schema v2 引入了有界
保留的 component physical identity。当前 schema v3 保留这些 identity，并添加 authored resource
dependency metadata。Schema-v3 binary 可以读取 v1 和 v2，在内存中规范化，并在下一次写入 state
时写为 v3；schema-v1 和 schema-v2 binary 会拒绝 v3。每台 host 首次执行会写入 v3 的 apply
之前，都必须备份当前 v1 或 v2 state，并保留匹配的旧配置和 binary。降级必须恢复该精确备份。
不存在 imperative state migration 命令，也不支持手动转换；可能有 apply 正在运行时，绝不能编辑
state。

## 资源关系

资源级 `depends_on` 是一项增量 alpha DSL 接口，仅限于 `packages.package`、`files.file` 和运行时
`services.service` declaration 之间、同一静态 scope 内的引用。生成的 `openrc.service`
declaration 不属于该 authored relationship 表面。它只添加顺序，不能激活 OpenRC operation 或
共享 `on_change` script。Plan 格式保持 `alpineform.plan.alpha1`：plan `depends_on` 暴露
当前 graph 资源的完整有效顺序集合，而 `triggered_by` 暴露独立的结构性或已激活变更触发集合。
仅存在于 state 中的 orphan 不会伪造当前 relationship 字段。consumer 不得从顺序推断触发行为。

State v3 仅持久化目标仍在跟踪中的 authored dependency address。Authored graph edge 使当前的
显式远端删除按 dependent-first 顺序执行；持久化的 v3 metadata 为之后的 orphan teardown 保留该
顺序。默认的 declaration removal 仍然只从 state 中 forget，不删除远端 object。Relationship
绝不选择 `ensure`、`on_remove` 或 `prevent_destroy` 行为。更改允许的引用 scope、trigger
分离或 teardown 保证，需要进行兼容性和迁移审查。

## Plan JSON

当前格式为 `alpineform.plan.alpha1`。在同一 release 内，相同的离线 input 会生成确定性的 JSON。
破坏性的结构或语义变更必须使用新的 `format_version`；alpha 系列期间可以添加字段，consumer
必须忽略未知字段。

顶层 `moves` array 和 `summary.move` 是 alpha1 的增量字段。consumer 必须将它们视为 state
地址迁移，与 create/update/adopt/delete/destroy/forget action 分开，并继续忽略未知字段。

敏感值和临时值绝不是对兼容性可见的内容。它们的脱敏表示可以增加 metadata，但绝不能泄露值。

## 受管目标兼容性

v0.1 Beta 目标是 x86_64 上的 Alpine 3.21、3.22、3.23 和 3.24 branch。在线观测精确 patch
facts；显式声明的精确版本必须匹配。显式 allowlist 之外的 branch 会被拒绝。添加或移除 branch，
或者提升 aarch64 目标的支持等级，必须有相应的真实 VM gate 和 support-matrix 更新。

## 变更审查

发布前，应对 DSL、CLI、address identity、state、plan JSON、provider 行为、installer 和
artifact 的变更进行分类。破坏性 alpha 变更必须出现在 `Breaking Changes` 和 `Migration Notes`
下。如果回滚不能安全复用先前 state，release 必须在创建 tag 前说明这一点。Schema-v3 release
notes 必须要求写入前备份 v1 或 v2，说明旧 binary 会拒绝 v3，并记录使用匹配配置和 binary 恢复
精确备份这一降级边界。

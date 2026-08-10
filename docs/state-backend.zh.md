<p align="right"><a href="state-backend.md">English</a> | <strong>简体中文</strong></p>

# 远端 State Backend

默认远端 state 路径是 `/var/lib/alpineform/state.json`。State decoder 会拒绝缺少 product marker、
外部 product、不支持或更新的 schema，以及 host identity 不匹配的读取。

State schema v2 引入 `component_identities`，它是从逻辑 component root 到保留的物理 component
name 的有界 mapping。只有当受跟踪资源需要与当前逻辑名称不同、由地址派生的 provider identity 时
才会写入，并在这些资源消失后进行 pruning。这样可在 component 重命名期间保持 source-build owner
ID、virtual APK package、marker path、workspace、cache 和 recorded output 稳定。

当前 state schema 是 v3。它保留 v2 component identity map，并添加每个资源的 authored dependency
metadata。Schema-v3 binary 读取 schema v1 和 v2，并在内存中将任一版本规范化为 v3。读取、在线
plan 和 check 不会持久化该转换。下一次 state 写入会写入 schema v3，包括必须协调 metadata 的
no-op apply 或 moved apply。Schema-v1 和 schema-v2 binary 会拒绝这一更新 state，因此在首次使用
schema-v3 binary 执行会写 state 的 apply 前，必须备份每台 host 当前的 v1 或 v2 文件。降级需要
匹配的旧配置和 binary，并恢复该精确备份；更改 `schema_version`、删除 dependency metadata 或删除
`component_identities` 都不是受支持的降级方式。请参阅
[operations runbook](operations-runbook.zh.md#state-备份和恢复)。

Component identity 和资源 dependency address 都不包含 input value 或 provider payload。

## 作者声明的资源依赖

每个受跟踪资源可以存储排序并去重后的 `depends_on` array。它只包含在 `packages.package`、
`files.file` 或运行时 `services.service` declaration 上编写的 dependency，并且仅在两个地址都仍
表示于 `resources` 中时保存。结构性 graph parent、推断的 package/account/init/conf prerequisite、
APK refresh 顺序和 `triggered_by` relationship 不会持久化到该字段。

即使每个 provider action 都是 no-op，apply 也会针对最终受跟踪资源集合协调该 metadata。Component
move 会同时 rebase 资源 key 和 dependency address。移除资源还会从每个保留资源中 prune 其地址，
因此不会重新引入陈旧 relationship。

对于当前显式远端 removal，authored edge 会先安排 dependent，再安排它所指向的 dependency。当
declaration 已经成为 orphan 时，engine 使用先前 state 中保留的 authored relationship 维持相同的
dependent-first 顺序。默认 declaration-removal policy 是 forget，它移除 state ownership 而不执行
provider deletion，因此不需要远端 teardown。Dependency 不会选择 `ensure`、`on_remove` 或
`prevent_destroy` 行为。地址是 metadata，不得包含受保护数据。

Schema v3 是必需的，因为 lifecycle-critical dependency metadata 无法由 schema-v2 binary 写入。
复用 v2 会让旧 writer 静默丢弃 authored edge，使之后的 orphan teardown 丧失已审查的顺序。

写入会准备规范化 copy，增加其 serial，并在调用 backend runner 前完成 encode。远端 script 创建
mode `0700` 的 state directory，把 stdin 写入同目录中 mode `0600` 的临时文件，再以原子方式将
该文件重命名覆盖 state path。Trap 会在失败或取消时删除未完成的临时文件。

State command stdin 和远端 error output 会标记为需要脱敏。Sensitive 和 ephemeral 资源省略其
desired 与 observed value，并持久化 protected marker。Ephemeral 资源通常省略 desired digest；
`DigestSafe` 资源契约可以保留一个仅从安全 desired metadata 计算出的 digest。安全 cleanup 和
status metadata 可以继续提供。

每个 instance 的预构建 artifact source 求值不会添加超出当前 schema v3 的字段。
已解析的受保护 URL 和 checksum 保留在内存中，绝不会写入 state；从任一值派生的 cache key 或
持久化 digest 也不会写入。State 可以保留安全 verification status、protection metadata、owner 和
mode、deletion policy、安全 metadata 的 desired digest，以及由保留的物理 component identity 和
规范化 source label 形成的稳定 cache 与 delete path。已有 literal source 保留以 checksum 为键的
desired 和 state 表示，资源地址不变。

## 声明式 Component Move

顶层 `moved` block 会 rebase 同一 host 上一个 component root 下的所有 state key，同时保留每个
地址 suffix。Segment-boundary matching 意味着 `component.worker` 不能匹配
`component.worker_old`。资源 ownership、lifecycle、deletion policy、protected status、observed
provider result 和 remote-object identity 保持不变。由地址派生的 desired metadata 和 relationship
会针对目标 graph 协调，而不是盲目改写已存储 string。

解析过程是确定且幂等的：

- Source 存在且 destination 不存在：移动所有匹配条目。
- Source 不存在且 destination 存在：该 host 已经完成迁移。
- 两者都不存在：不伪造 state；正常 plan desired resource。
- 两者都存在：失败，不合并或丢弃 state。

`plan` 和 `check` 针对内存 copy 解析，绝不写入 state。在 `apply` 期间，AlpineForm 在每台 host
租约内重新计算 mapping，将其包含在 locked-plan approval 中，并在该 host 的任何 provider mutation
前写入完整 moved state。仅 move 的 apply 执行一次原子写入，serial 只前进一次。如果该写入失败，
先前 state 文件保持有效，并且不会开始 provider mutation。

如果后续 provider action 失败，address move 可以保持已提交；保留 block 并重试。多 host apply
在每台 host 内具有原子性，但并非跨 host 原子，因此重试时可能同时遇到已完成和待处理 host。保留
block 可确保这种混合状态安全：已迁移 host 不实现 move，待处理 host 仍有该 instruction。

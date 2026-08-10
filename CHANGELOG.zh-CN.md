# 变更日志

<p align="right"><a href="CHANGELOG.md">English</a> | <strong>简体中文</strong></p>

AlpineForm 所有值得关注的用户可见变更都记录在此。

## [Unreleased]

### 新增

- 新增对 Alpine 3.21、3.22、3.23 和 3.24 被管理目标的显式支持，包括感知分支的 APK 和
  Docker 仓库、固定的官方 cloud image、包含 12 个 case 和 48 个 job 的阻塞式 x86_64
  VM 矩阵，以及覆盖四个分支的已发布 release quickstart 验证。
- 新增 Preview 级 Alpine 原生 Docker Engine 和 Compose domain，包括官方或显式指定 tag
  的 APK source、OpenRC 收敛、组成员关系、经过验证的原子 daemon 配置、去重 restart、
  Compose preflight、running/stopped/absent intent、稳定的 observed 分类、受保护的 env
  内容，以及限定 scope 的 forget/destroy 行为。
- 新增第九个阻塞式 Alpine 3.24.1 x86_64 VM case，覆盖全新安装、软件包版本、no-op、无效
  daemon/Compose 隔离、崩溃恢复、partial/degraded 漂移修复、重启、全新的 stopped project、
  project forget/adopt、限定 scope 的 destroy、absent，以及完整移除 Docker。
- 新增 Preview 级、具备回滚安全性的 nftables 管理，包括显式 named-table 所有权、Alpine
  软件包和 non-flushing OpenRC 持久化、受保护的分阶段激活、分离式 rollback watchdog、
  有界管理路径确认、限定 scope 的 delete/forget 行为，以及单独的
  `--allow-network-disruption` 授权。
- 新增第十个阻塞式 Alpine 3.24.1 x86_64 VM case，覆盖无效语法、create/update/no-op、
  active/persistent/marker 漂移修复、重启、保留外部 table、会阻断 SSH 的激活、本地进程
  终止、分离式回滚、过期制品清理、限定 scope 的删除，以及经过清理的诊断信息。
- 新增 Preview 级目标侧组件 source build，支持带校验和的 local、inline、remote 和 archive
  输入；argv 命令；确定性的受保护 environment 和 stdin；网络/文件系统隔离；APK 虚拟
  软件包所有权；经过验证的原子安装；rebuild/repair plan；安全的 forget/destroy 行为；
  以及受保护的 state。
- 新增 profile/host `staging.root` 默认值和 source-component 实例级 `staging_root` 覆盖，
  用于将目标侧构建 workspace 放在默认 `/var/tmp/alpineform/builds` 之外。放置位置只影响
  runtime，且不影响 identity；私有且受所有权管理的 workspace、受保护的 `/run` 输入、
  有 guard 的旧 root 清理、受 generation lock 保护的 supervisor 取消恢复、容量诊断，
  以及与目标相邻的预构建 archive staging，均保留原有安全和事务边界。
- 新增第十一个 Alpine 3.24.1 x86_64 VM case 和专用 Preview 门禁，覆盖 musl 编译、no-op、
  source/build/output 漂移、重启、校验和、编译器、缺失或 symlink output、取消、ENOSPC、
  secret 脱敏、共享依赖保留，以及中断构建恢复。
- 将四分支 source-build Preview 门禁扩展为每个 Alpine 版本 48 项显式断言，包括旧版默认值、
  profile/host 优先级候选值和实际执行的实例级覆盖；受限 `/var/tmp` 下的运行；仅 root
  变化时利用缓存实现 no-op；下一次 rebuild 的放置位置；以及 workspace/受保护输入清理。
- 新增静态顶层 component-root `moved` block，具备确定性的 chain 和 collision 验证、
  host scope 内的原子 state 迁移、保留的 source-build 物理所有权、独立的 text/JSON/HTML
  move 渲染，并在 state、plan、debug、diagnostics 和 error 中对受保护组件值进行脱敏。
- 新增覆盖 Alpine 3.21、3.22、3.23 和 3.24 的第十二个阻塞式 VM case，以及专用的
  component-moved Preview 门禁。该 case 覆盖只读的 rename-only review，其中包含 18 个精确
  move、18 个 no-op resource、零 mutation action，以及保持不变的 state 和 physical
  identity。随后，编号 lifecycle 会在应用 move 的同时只执行一次合法文件更新和 change
  trigger，干净地保留和移除 block，通过保留的 physical identity 为后续 source-input
  变化重新构建，拒绝重复所有权，并完成精确清理。
- 新增对预构建组件 `source.url` 和 `source.sha256` 的逐挂载实例求值；求值发生在类型化输入
  规范化并验证之后，涵盖未挂载时的静态 shape 验证、离线声明架构选择、在线观测架构选择，
  以及受保护的内存内 provider payload。稳定 cache identity 基于保留的物理 component
  identity 和规范化 source label。
- 将现有阻塞式 `components` case 扩展到 Alpine 3.21-3.24，覆盖 binary、file、archive 和
  CA-certificate 的 literal 与 protected source、校验和失败、no-op、漂移修复、清理和重启。
  它仍属于现有 12-case、48-job 矩阵，而不是增加第十三个 case。
- 为 `packages.package`、`files.file` 和运行时 `services.service` 声明新增静态、同 scope、
  带类型的 `depends_on` 引用。作者声明的 edge 会在 profile/component 组合后解析，在 plan
  中与推断出的 ordering 合并，与 `triggered_by` 保持独立，为 orphan teardown 持久化；
  当显式远程删除原本会先删除 dependency、后删除 dependent 时，其顺序会反转。
- 扩展现有四分支 `openrc` case，证明首次 apply、no-op、漂移修复、反向显式清理和默认
  forget 过程中的 package -> managed configuration -> OpenRC service 顺序。它仍是现有
  12 个 case 之一，因此阻塞矩阵仍为 48 个 job。
- 为除 `AGENTS.md` 外的每份维护中 Markdown 文档发布完整的简体中文对应版本，并提供双向
  语言选择器、同语言导航、独立文档索引、验证结构和技术一致性的 `make docs-check` 门禁，
  以及包含双语资料的 GoReleaser、curl 安装程序和 `make install` 数据。

### 变更

- 使 `apf fmt` 在格式化所选文件前只检查 HCL 语法。它不再加载变量输入，也不再要求完整且
  语义有效的 AlpineForm model；语义验证请使用 `apf validate`。

### 修复

- 使用可移植的 `apk info -e` 软件包存在性查询，使 Alpine 3.21 的 `apk-tools 2.14` 能在
  apply 后正确观测软件包和 source-build dependency 的收敛状态。
- 将 service reload advertisement 与已安装 OpenRC framework 的隐式 baseline 比较，使
  raw reload hook 可在 OpenRC 0.55 和 0.63 上工作，同时不接受未声明的 fallback。

### 兼容性

- 将持久化 Alpine 3.21 至 3.24 x86_64 目标提升至 v0.1 Beta 支持集合。显式 allowlist 外的
  分支仍会在可写执行前被拒绝；aarch64 在没有真实 VM 门禁的情况下仍为 Preview。
- 资源级 `depends_on` 是增量 alpha DSL 接口。它只接受对同一 resolved host 或 mounted-
  component scope 中 `packages.package`、`files.file` 或运行时 `services.service` 声明的
  静态引用。`alpineform.plan.alpha1` 仍是 plan format：当前 graph resource 会在 plan
  `depends_on` 中公开 structural、inferred 和 authored ordering，而 `triggered_by` 仍是
  独立的 change-activation 关系。State schema v3 新增 authored dependency metadata；v3
  binary 可读取 v1 和 v2，并在下一次 state write 时持久化为 v3，而旧 binary 会拒绝 v3。
- Docker DSL 和 `host.<name>.docker.*` 资源地址是增量 alpha 接口。Docker 仍为 Preview，
  不属于 v0.1 core/Beta 承诺。
- nftables DSL、`host.<name>.nftables.*` 资源地址和增量 `network_disruption` plan risk
  均为 alpha 接口。尽管具备阻塞式 rollback 门禁，named-table nftables 仍为 Preview。
- source-build DSL 和 `host.<name>.component.<instance>.build.*` 资源地址是增量 alpha
  接口。目标侧构建仍为 Preview，需要持久化 Alpine 上的 root 和 Bubblewrap，且不支持
  构建命令联网或未经检查的输入。
- `staging.root` 和 source-component `staging_root` 是增量 alpha DSL 接口。它们不会改变
  build identity、资源地址、state schema v3 或 `alpineform.plan.alpha1`；解析后的路径
  不会进入序列化 IR、graph、plan、state、HTML 和常规 debug event。有界 workspace 失败
  诊断会指出所选 root 和派生 work path。Source build 仍为 Preview。
- `moved` DSL 以及 `alpineform.plan.alpha1` 中的 `moves`/`summary.move` 字段是增量 alpha
  接口。Move 属于 state migration，不改变资源 action 数。State schema v2 引入了保留的
  物理 component identity；当前 schema v3 保留该 map 并新增 authored resource
  dependency。尽管有四分支阻塞 VM case 和专用 aggregate gate，component-root move 仍为
  Preview。
- 逐实例预构建 `source.url` 和 `source.sha256` 表达式是增量 alpha 接口。现有 literal
  行为、资源地址、以校验和为 key 的公共 cache、state schema v3 和
  `alpineform.plan.alpha1` 保持兼容；目标侧 source-build 语义不变。
- Binary 和 archive 组件仍为 Beta。通过阻塞式四分支 `components` case，将 file 和
  CA-certificate 组件从 Preview 提升至 Beta。受保护 URL/checksum 值只保留在内存中，
  并使用基于保留的物理 component identity 和规范化 source label 的稳定 cache identity，
  而不使用序列化的受保护材料。

### 迁移说明

- 在 schema-v3 binary 首次执行会写 state 的 apply 前，请备份每台 host 的 schema-v1 或
  schema-v2 `/var/lib/alpineform/state.json`，并保留匹配的旧配置和 binary。在线
  plan/check 只会在内存中规范化旧 state。v3 写入后的 downgrade 要求恢复确切备份；不支持
  编辑 schema marker、dependency metadata 或保留的 identity map。
- 重命名 component instance 时，请添加包含该 rename 的 `moved` block，并保留它，直到
  每台 host 都已完成迁移且 plan/check 干净。只迁移一台 host 或一个 rollout batch 后就
  移除 block，会使其余 source state 无法迁移。所有 host 完成后，移除 block 是 no-op。
- 逐实例 artifact source 不要求手动、schema 或资源地址迁移。现有 literal 声明保持兼容。
  当 literal source 变为 protected 时，AlpineForm 会自动将旧版 checksum cache 和 CA
  marker 迁移到稳定的 protected identity，并在 backend lease 下预写已经 scrub 的 state。
  Adoption 期间应保持 component 已挂载、保留其 physical identity（若重命名则使用
  `moved`），并保留规范化 source label。
- 添加或更改 source-build workspace root 不需要 state migration。有效且已经验证的 output
  cache 会使只改变 root 的操作保持 no-op；下一次必需 rebuild 会使用新 root，并且只有在
  所有权和路径验证通过后才删除已记录的旧 workspace。

## [v0.1.0-alpha.5] - 2026-07-13

### 修复

- 为每个 macOS 验证结果提供唯一的架构专用文件名，使 `download-artifact` 可以安全地
  flatten 多个 artifact，而不会覆盖其中一个结果。
- 在 summary diagnostics 中包含失败的 matrix，同时继续拒绝不完整的 release 验证。

## [v0.1.0-alpha.4] - 2026-07-13

### 修复

- 递归发现 verification result artifact，只解析已知的 `key=yes` record，并在发布 release
  matrix 前拒绝缺失或未知结果。
- alpha.3 的 publisher 和所有 platform verification job 均通过、但最终 matrix aggregate
  失败后，将 alpha.3 保留为可审计的不完整 release。

### 已知问题

- Publisher、supply-chain、两个 macOS installer 和 fresh-Alpine verification 均已通过。
  最终 summary 将两个都名为 `macos.env` 的文件 flatten，导致其中一个架构结果被覆盖，
  matrix 因而正确地保持失败。该 release 不完整。

## [v0.1.0-alpha.3] - 2026-07-13

### 变更

- 将 `v0.1.0-alpha.2` 标记为不完整，因为用户所有的私有仓库不支持持久化 GitHub
  provenance，后续 release 验证因此被跳过。
- 在创建或上传任何 release asset 之前，以 GitHub artifact-attestation eligibility 为
  release dry-run 和 tag 发布设置门禁。
- 从公开仓库发布修正候选版本；当前 plan 在公开仓库中支持 GitHub artifact attestation。

### 修复

- 将 alpha.1 和 alpha.2 保留为可审计的不完整 release，同时使用新的不可变 prerelease tag
  推进完整 preview。

### 已知问题

- Publisher、GitHub attestation、Linux supply-chain verification、两个 macOS installer 和
  fresh-Alpine quickstart 均已通过。最终 summary 失败，原因是下载的 result file 位于
  artifact 目录下的嵌套路径中，而 workflow 只扫描顶层目录。该 release 不完整。

## [v0.1.0-alpha.2] - 2026-07-13

### 修复

- 使用 release workflow 调用的 command name 安装已验证的 Cosign binary，使 checksum
  signing、SBOM upload、provenance 和已发布 artifact verification 可以运行。
- 在不移动已签名 tag 或替换部分 asset 的前提下，由新版本取代不完整的
  `v0.1.0-alpha.1` prerelease。

### 已知问题

- 该 release 发布了 archive、checksum、Sigstore bundle 和四个 SBOM；随后 GitHub 拒绝为
  用户所有的私有仓库持久化 artifact attestation。后续 installer 和 fresh-VM verification
  被跳过。该 prerelease 不完整，且不得使用。

## [v0.1.0-alpha.1] - 2026-07-13

### 新增

- 面向 AlpineForm 配置的 `apf validate`、`plan`、`apply`、`check`、`fmt`、inspection 和
  version 工作流。
- Alpine 3.24 facts discovery、root SSH transport、原子远程 state 和可续期的逐 host
  runtime lease。
- 原生 file、directory、account、authorized key、APK、package、OpenRC、hostname、
  timezone、kernel module 和 sysctl 收敛。
- 经过验证的 binary、file、archive 和 CA component artifact，以及去重 change script。
- 阻塞式 Alpine 3.24.1 x86_64 libvirt matrix，包含 no-op、drift、repair、lifecycle、
  secret、lock 和 reboot 断言。
- 可复现的 Linux 和 macOS amd64/arm64 archive、经过 checksum 验证的安装、SBOM、Sigstore
  签名和 provenance attestation 的 release 自动化。

### 兼容性

- 这是首个 alpha release，不对从更早 AlpineForm release 升级作出兼容性承诺。
- DSL、CLI、资源地址、state schema 和 plan JSON 可能在后续 prerelease 中发生变更，届时
  会提供明确的 release note 和迁移指引。

### 已知问题

- Release workflow 在发布 archive 和 checksum 后、执行 checksum signing、SBOM generation、
  provenance 和 post-release verification 前失败。该 prerelease 不完整，且不得使用。

[Unreleased]: https://github.com/mofelee/alpineform/compare/v0.1.0-alpha.5...HEAD
[v0.1.0-alpha.5]: https://github.com/mofelee/alpineform/compare/v0.1.0-alpha.4...v0.1.0-alpha.5
[v0.1.0-alpha.4]: https://github.com/mofelee/alpineform/compare/v0.1.0-alpha.3...v0.1.0-alpha.4
[v0.1.0-alpha.3]: https://github.com/mofelee/alpineform/compare/v0.1.0-alpha.2...v0.1.0-alpha.3
[v0.1.0-alpha.2]: https://github.com/mofelee/alpineform/compare/v0.1.0-alpha.1...v0.1.0-alpha.2
[v0.1.0-alpha.1]: https://github.com/mofelee/alpineform/releases/tag/v0.1.0-alpha.1

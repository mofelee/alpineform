<p align="right"><a href="support-matrix.md">English</a> | <strong>简体中文</strong></p>

# 支持矩阵

状态含义：

- **Beta**：属于 v0.1 核心，并由真实 Alpine VM 覆盖阻塞。
- **Preview**：具有静态、unit、cross-build 或较窄的运行时证据；尚未提升到阻塞性目标承诺。
- **Unsupported**：被拒绝、不存在于公开 DSL 中，或有意不提供。

## 受管目标

| 目标 | 状态 | 证据和边界 |
| --- | --- | --- |
| Alpine 3.21-3.24 x86_64、persistent install、OpenRC | Beta | 四个 branch 通过 [12-case VM matrix](../test/integration/libvirt/cases)，共 48 个 job，以及聚合 [CI gate](../.github/workflows/ci.yml) |
| Alpine 3.21-3.24 aarch64 | Preview | [Fact normalization test](../internal/core/engine/facts_test.go) 和 [Linux arm64 cross-build](../.github/workflows/ci.yml)；没有真实 VM gate |
| Alpine 3.20 及更早版本，或 3.25 及更高版本 | Unsupported | [Fact rejection test](../internal/core/engine/facts_test.go) 会在可写执行前拒绝显式 allowlist 之外的 branch |
| Alpine edge | Unsupported | [Fact rejection test](../internal/core/engine/facts_test.go) 会在可写执行前拒绝 rolling version |
| Diskless/data mode 和 `lbu commit` | Unsupported | 记录的 [state backend](state-backend.zh.md) 假设 persistent root filesystem；[v0.1 DSL](dsl-reference.zh.md) 中没有 mode selector |
| 非 Alpine 系统 | Unsupported | [Fact gate](../test/integration/libvirt/cases/facts-state-lock/negative.sh) 会在 state 或资源写入前拒绝 |
| root SSH | Beta | [SSH 契约](ssh.zh.md)、[backend test](../internal/core/backend/ssh_test.go) 和真实 VM matrix |
| 非 root SSH、sudo 或 doas 提权 | Unsupported | [Parser 和 backend test](../internal/core/backend/ssh_test.go) 拒绝非 root user；不存在提权路径 |

## 核心 Domain

| 表面 | 状态 | 自动化证据 |
| --- | --- | --- |
| Facts、platform mismatch、state 和运行时租约 | Beta | [`facts-state-lock`](../test/integration/libvirt/cases/facts-state-lock) |
| File、directory、sensitive 和 ephemeral content | Beta | [`files-directories-secrets`](../test/integration/libvirt/cases/files-directories-secrets) |
| Group、user、membership 和 authorized key | Beta | [`accounts`](../test/integration/libvirt/cases/accounts) |
| Managed 和 authoritative APK repository | Beta | [`apk`](../test/integration/libvirt/cases/apk) |
| Package present、显式 absent 和 declaration forget | Beta | [`apk`](../test/integration/libvirt/cases/apk) |
| Package -> managed configuration -> OpenRC dependency lifecycle | Beta | 四 branch [`openrc`](../test/integration/libvirt/cases/openrc)，加 [parser](../internal/core/parser/resource_dependencies_test.go)、[merge](../internal/core/merge/resource_dependencies_test.go)、[graph](../internal/core/graph/resource_dependencies_test.go) 和 [engine](../internal/core/engine/dependency_order_test.go) 契约 |
| 自定义 APK signing key | Preview | [Graph test](../internal/core/graph/apk_test.go) 和 [provider test](../internal/core/provider/apk_test.go)；v0.1 中没有真实 VM fixture |
| Generated 和 raw OpenRC service | Beta | [`openrc`](../test/integration/libvirt/cases/openrc) |
| Hostname、timezone、module 和 sysctl | Beta | [`system-kernel`](../test/integration/libvirt/cases/system-kernel) |
| Binary 和 archive component、共享 `on_change` script | Beta | [`components`](../test/integration/libvirt/cases/components) |
| File 和 CA-certificate component | Beta | 四 branch [`components`](../test/integration/libvirt/cases/components)，加 [compiler](../internal/core/merge/components_test.go)、[graph](../internal/core/graph/components_test.go)、[file/source provider](../internal/core/provider/component_test.go) 和 [archive/CA provider](../internal/core/provider/component_archive_test.go) 契约 |
| Component-root `moved` state migration | Preview | 四 branch [`component-moved`](../test/integration/libvirt/cases/component-moved)、[engine](../internal/core/engine/moved_test.go) 和 [plan](../internal/core/plan/plan_test.go) 契约测试，以及专用 [component-moved Preview gate](../.github/workflows/ci.yml)；增量 alpha DSL、由 state v3 保留的 v2-origin identity map 和 plan 字段仍在 Beta 承诺之外 |
| 目标端 component source build 和可配置 workspace root | Preview | 四 branch [`source-build`](../test/integration/libvirt/cases/source-build)，每个 Alpine 版本有 48 条显式 assertion，加 [workspace compiler 契约](../internal/core/merge/workspace_root_test.go)、[provider ownership/transaction test](../internal/core/provider/component_build_test.go) 和专用 [source-build Preview gate](../.github/workflows/ci.yml)；启用网络的 build 仍不受支持 |
| `prevent_destroy`、forget 和 recorded destroy | Beta | [`lifecycle`](../test/integration/libvirt/cases/lifecycle)、[`accounts`](../test/integration/libvirt/cases/accounts) 和 [`apk`](../test/integration/libvirt/cases/apk) |
| Docker Engine、OpenRC、daemon configuration 和 Compose | Preview | 四 branch [`docker`](../test/integration/libvirt/cases/docker)、[compiler/graph test](../internal/core/merge/docker_test.go) 和 [provider test](../internal/core/provider/docker_test.go)；Alpine `community` 安全支持周期短于 `main`，且没有 aarch64 VM gate |
| Named-table nftables、non-flushing OpenRC persistence 和 rollback watchdog | Preview | 四 branch [`nftables`](../test/integration/libvirt/cases/nftables)、[compiler/graph test](../internal/core/merge/nftables_test.go)、[provider test](../internal/core/provider/nftables_test.go) 和专用 [nftables Preview gate](../.github/workflows/ci.yml)；不支持 whole-ruleset ownership，live change 需要单独的 network-disruption approval |

所有 VM case 都会执行 validate、构建离线 plan、构建 observed plan、apply、断言 JSON no-op plan、
运行干净 `check`、在适用时引入 drift、要求非零 `check`、repair、recheck、reboot，并验证 persistence。

每个挂载 instance 的预构建 `source.url` 和 `source.sha256` 表达式是上述四种 component type 的
增量 alpha DSL 接口。该兼容性阶段与运行时支持分开：binary 和 archive 保持 Beta；file 和
CA-certificate component 为 Beta，仅因为现有 `components` case 现在阻塞所有四个 Alpine branch。
suite 仍然恰好有 12 个 case 和 48 个 job。Source-build input 保持独立的 Preview capability。

Source-build workspace placement 也是该 Preview capability 中的增量 alpha 语法。四 branch case
验证 legacy default、instance root 优先于 profile/host candidate、受限 `/var/tmp` 下运行、cached
root-only no-op、下一次 rebuild placement 和 cleanup，且不削弱 Bubblewrap 或 `/run` protected-input
isolation。Compiler 契约覆盖 profile-only 和 host-default branch。Workspace root 仅用于运行时，
不会改变 resource/build identity 或序列化 plan/state 契约。预构建 archive staging 仍与 destination
相邻，不属于该 workspace selector。

资源级 `depends_on` 语法是增量 alpha 接口。可移植 package -> file -> service 运行时行为位于 Beta
四 branch gate 中：正向顺序、no-op、drift repair、反向显式 cleanup 和默认 forget 都在现有
`openrc` case 内运行，不改变 matrix cardinality。Authored ordering 不隐含 service operation
trigger。

## CLI 和分发

| 表面 | 状态 | 自动化证据 |
| --- | --- | --- |
| Linux amd64 CLI archive 和 checksum installer | Beta | [Installer test](../scripts/test-install.sh)、[snapshot gate](../.github/workflows/release-dry-run.yml) 和 [已发布 installer/VM verification](../.github/workflows/release.yml) |
| Linux arm64 CLI archive | Preview | [Cross-build](../.github/workflows/ci.yml) 和 [snapshot archive gate](../.github/workflows/release-dry-run.yml)；没有 native installer runner |
| macOS amd64 和 arm64 CLI archive | Preview | [Snapshot archive gate](../.github/workflows/release-dry-run.yml) 和 [已发布 installer job](../.github/workflows/release.yml) |
| Homebrew | Unsupported | 在 install/test/upgrade 证据存在前，有意从 [release configuration](../.goreleaser.yaml) 中排除 |
| Windows | Unsupported | 被 [installer platform selector](../scripts/install.sh) 拒绝，并且不存在于[固定 release target](../.goreleaser.yaml) 中 |

CLI platform 与受管目标 platform 相互独立。macOS arm64 control host 可以管理 Beta Alpine
3.21-3.24 x86_64 目标，但这不会提升 Alpine aarch64 目标的支持等级。

Docker/Compose、nftables、目标端 source build 和 component-root move 是已实现的 Preview
capability，仍在 v0.1 core/Beta 承诺之外。

# Support Matrix

Status meanings:

- **Beta**: part of the v0.1 core and blocked by real Alpine VM coverage.
- **Preview**: implemented with static, unit, cross-build, or narrower runtime
  evidence; not promoted to the blocking target promise.
- **Unsupported**: rejected, absent from the public DSL, or deliberately not
  shipped.

## Managed Targets

| Target | Status | Evidence and boundary |
| --- | --- | --- |
| Alpine 3.24 x86_64, persistent install, OpenRC | Beta | [Ten-case VM matrix](../test/integration/libvirt/cases) and aggregate [CI gate](../.github/workflows/ci.yml) |
| Alpine 3.24 aarch64 | Preview | [Fact normalization test](../internal/core/engine/facts_test.go) and [Linux arm64 cross-build](../.github/workflows/ci.yml); no real-VM gate |
| Alpine 3.23 and earlier | Unsupported | [Fact rejection tests](../internal/core/engine/facts_test.go) reject other branches before write-capable execution |
| Alpine edge | Unsupported | [Fact rejection tests](../internal/core/engine/facts_test.go) reject a rolling version before write-capable execution |
| Diskless/data mode and `lbu commit` | Unsupported | The documented [state backend](state-backend.md) assumes a persistent root filesystem; no mode selector exists in the [v0.1 DSL](dsl-reference.md) |
| Non-Alpine systems | Unsupported | [Fact gate](../test/integration/libvirt/cases/facts-state-lock/negative.sh) rejects before state or resource writes |
| root SSH | Beta | [SSH contract](ssh.md), [backend tests](../internal/core/backend/ssh_test.go), and the real-VM matrix |
| non-root SSH, sudo, or doas escalation | Unsupported | [Parser and backend tests](../internal/core/backend/ssh_test.go) reject non-root users; no escalation path exists |

## Core Domains

| Surface | Status | Automated evidence |
| --- | --- | --- |
| Facts, platform mismatch, state, and runtime lease | Beta | [`facts-state-lock`](../test/integration/libvirt/cases/facts-state-lock) |
| Files, directories, sensitive and ephemeral content | Beta | [`files-directories-secrets`](../test/integration/libvirt/cases/files-directories-secrets) |
| Groups, users, memberships, and authorized keys | Beta | [`accounts`](../test/integration/libvirt/cases/accounts) |
| Managed and authoritative APK repositories | Beta | [`apk`](../test/integration/libvirt/cases/apk) |
| Package present, explicit absent, and declaration forget | Beta | [`apk`](../test/integration/libvirt/cases/apk) |
| Custom APK signing keys | Preview | [Graph tests](../internal/core/graph/apk_test.go) and [provider tests](../internal/core/provider/apk_test.go); no real-VM fixture in v0.1 |
| Generated and raw OpenRC services | Beta | [`openrc`](../test/integration/libvirt/cases/openrc) |
| Hostname, timezone, modules, and sysctls | Beta | [`system-kernel`](../test/integration/libvirt/cases/system-kernel) |
| Binary and archive components, shared `on_change` scripts | Beta | [`components`](../test/integration/libvirt/cases/components) |
| File and CA-certificate components | Preview | [Compiler tests](../internal/core/merge/components_test.go), [graph tests](../internal/core/graph/components_test.go), and [provider tests](../internal/core/provider/component_archive_test.go); no blocking VM fixture |
| Target-side component source builds | Preview | [Compiler contract tests](../internal/core/merge/component_build_test.go), [phase graph tests](../internal/core/graph/components_test.go), [provider transaction tests](../internal/core/provider/component_build_test.go), and the [source-build threat model](source-build-security.md); destructive Alpine VM coverage is not complete |
| `prevent_destroy`, forget, and recorded destroy | Beta | [`lifecycle`](../test/integration/libvirt/cases/lifecycle), [`accounts`](../test/integration/libvirt/cases/accounts), and [`apk`](../test/integration/libvirt/cases/apk) |
| Docker Engine, OpenRC, daemon configuration, and Compose | Preview | [`docker`](../test/integration/libvirt/cases/docker), [compiler/graph tests](../internal/core/merge/docker_test.go), and [provider tests](../internal/core/provider/docker_test.go); Alpine `community` lifecycle and no aarch64 VM gate keep this outside Beta |
| Named-table nftables, non-flushing OpenRC persistence, and rollback watchdog | Preview | [`nftables`](../test/integration/libvirt/cases/nftables), [compiler/graph tests](../internal/core/merge/nftables_test.go), [provider tests](../internal/core/provider/nftables_test.go), and the dedicated [nftables Preview gate](../.github/workflows/ci.yml); whole-ruleset ownership is unsupported and live changes require separate network-disruption approval |

All VM cases validate, build an offline plan, build an observed plan, apply,
assert a JSON no-op plan, run clean `check`, introduce drift where applicable,
require nonzero `check`, repair, recheck, reboot, and verify persistence.

## CLI And Distribution

| Surface | Status | Automated evidence |
| --- | --- | --- |
| Linux amd64 CLI archive and checksum installer | Beta | [Installer test](../scripts/test-install.sh), [snapshot gate](../.github/workflows/release-dry-run.yml), and [published installer/VM verification](../.github/workflows/release.yml) |
| Linux arm64 CLI archive | Preview | [Cross-build](../.github/workflows/ci.yml) and [snapshot archive gate](../.github/workflows/release-dry-run.yml); no native installer runner |
| macOS amd64 and arm64 CLI archives | Preview | [Snapshot archive gate](../.github/workflows/release-dry-run.yml) and [published installer jobs](../.github/workflows/release.yml) |
| Homebrew | Unsupported | Deliberately absent from the [release configuration](../.goreleaser.yaml) until install/test/upgrade evidence exists |
| Windows | Unsupported | Rejected by the [installer platform selector](../scripts/install.sh) and absent from the [fixed release targets](../.goreleaser.yaml) |

The CLI platform is independent of the managed target platform. A macOS arm64
control host can manage the Beta Alpine 3.24 x86_64 target, but that does not
promote Alpine aarch64 target support.

Docker/Compose, nftables, and target-side source builds are implemented Preview
schema and remain outside the v0.1 core/Beta promise.

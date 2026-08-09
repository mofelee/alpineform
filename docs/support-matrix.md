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
| Alpine 3.21-3.24 x86_64, persistent install, OpenRC | Beta | Four branches by [12-case VM matrix](../test/integration/libvirt/cases), 48 jobs, and aggregate [CI gate](../.github/workflows/ci.yml) |
| Alpine 3.21-3.24 aarch64 | Preview | [Fact normalization tests](../internal/core/engine/facts_test.go) and [Linux arm64 cross-build](../.github/workflows/ci.yml); no real-VM gate |
| Alpine 3.20 and earlier, or 3.25 and later | Unsupported | [Fact rejection tests](../internal/core/engine/facts_test.go) reject branches outside the explicit allowlist before write-capable execution |
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
| Package -> managed configuration -> OpenRC dependency lifecycle | Beta | Four-branch [`openrc`](../test/integration/libvirt/cases/openrc), plus [parser](../internal/core/parser/resource_dependencies_test.go), [merge](../internal/core/merge/resource_dependencies_test.go), [graph](../internal/core/graph/resource_dependencies_test.go), and [engine](../internal/core/engine/dependency_order_test.go) contracts |
| Custom APK signing keys | Preview | [Graph tests](../internal/core/graph/apk_test.go) and [provider tests](../internal/core/provider/apk_test.go); no real-VM fixture in v0.1 |
| Generated and raw OpenRC services | Beta | [`openrc`](../test/integration/libvirt/cases/openrc) |
| Hostname, timezone, modules, and sysctls | Beta | [`system-kernel`](../test/integration/libvirt/cases/system-kernel) |
| Binary and archive components, shared `on_change` scripts | Beta | [`components`](../test/integration/libvirt/cases/components) |
| File and CA-certificate components | Beta | Four-branch [`components`](../test/integration/libvirt/cases/components), plus [compiler](../internal/core/merge/components_test.go), [graph](../internal/core/graph/components_test.go), [file/source provider](../internal/core/provider/component_test.go), and [archive/CA provider](../internal/core/provider/component_archive_test.go) contracts |
| Component-root `moved` state migrations | Preview | Four-branch [`component-moved`](../test/integration/libvirt/cases/component-moved), [engine](../internal/core/engine/moved_test.go) and [plan](../internal/core/plan/plan_test.go) contract tests, and the dedicated [component-moved Preview gate](../.github/workflows/ci.yml); the additive alpha DSL, v2-origin identity map retained by state v3, and plan fields remain outside the Beta promise |
| Target-side component source builds and configurable workspace roots | Preview | Four-branch [`source-build`](../test/integration/libvirt/cases/source-build) with 48 explicit assertions per Alpine version, [workspace compiler contracts](../internal/core/merge/workspace_root_test.go), [provider ownership/transaction tests](../internal/core/provider/component_build_test.go), and the dedicated [source-build Preview gate](../.github/workflows/ci.yml); network-enabled builds remain unsupported |
| `prevent_destroy`, forget, and recorded destroy | Beta | [`lifecycle`](../test/integration/libvirt/cases/lifecycle), [`accounts`](../test/integration/libvirt/cases/accounts), and [`apk`](../test/integration/libvirt/cases/apk) |
| Docker Engine, OpenRC, daemon configuration, and Compose | Preview | Four-branch [`docker`](../test/integration/libvirt/cases/docker), [compiler/graph tests](../internal/core/merge/docker_test.go), and [provider tests](../internal/core/provider/docker_test.go); Alpine `community` security support is shorter than `main`, and no aarch64 VM gate exists |
| Named-table nftables, non-flushing OpenRC persistence, and rollback watchdog | Preview | Four-branch [`nftables`](../test/integration/libvirt/cases/nftables), [compiler/graph tests](../internal/core/merge/nftables_test.go), [provider tests](../internal/core/provider/nftables_test.go), and the dedicated [nftables Preview gate](../.github/workflows/ci.yml); whole-ruleset ownership is unsupported and live changes require separate network-disruption approval |

All VM cases validate, build an offline plan, build an observed plan, apply,
assert a JSON no-op plan, run clean `check`, introduce drift where applicable,
require nonzero `check`, repair, recheck, reboot, and verify persistence.

Per-mounted-instance prebuilt `source.url` and `source.sha256` expressions are
an additive alpha DSL interface across the four component types above. That
compatibility phase is separate from runtime support: binary and archive remain
Beta, and file and CA-certificate components are Beta only because the existing
`components` case now blocks all four Alpine branches. The suite remains exactly
12 cases and 48 jobs. Source-build inputs remain a separate Preview capability.

Source-build workspace placement is also additive alpha syntax within that
Preview capability. The four-branch case proves the legacy default, an instance
root winning over profile/host candidates, constrained-`/var/tmp` operation,
cached root-only no-op, next-rebuild placement, and cleanup without weakening
Bubblewrap or `/run` protected-input isolation. Compiler contracts cover the
profile-only and host-default branches. Workspace roots are runtime-only and do
not change resource/build identity or serialized plan/state contracts. Prebuilt
archive staging remains destination-adjacent and is not part of this workspace
selector.

Resource-level `depends_on` syntax is an additive alpha interface. The portable
package -> file -> service runtime behavior is in the Beta four-branch gate:
forward ordering, no-op, drift repair, reverse explicit cleanup, and default
forget all run inside the existing `openrc` case, without changing matrix
cardinality. Authored ordering does not imply a service operation trigger.

## CLI And Distribution

| Surface | Status | Automated evidence |
| --- | --- | --- |
| Linux amd64 CLI archive and checksum installer | Beta | [Installer test](../scripts/test-install.sh), [snapshot gate](../.github/workflows/release-dry-run.yml), and [published installer/VM verification](../.github/workflows/release.yml) |
| Linux arm64 CLI archive | Preview | [Cross-build](../.github/workflows/ci.yml) and [snapshot archive gate](../.github/workflows/release-dry-run.yml); no native installer runner |
| macOS amd64 and arm64 CLI archives | Preview | [Snapshot archive gate](../.github/workflows/release-dry-run.yml) and [published installer jobs](../.github/workflows/release.yml) |
| Homebrew | Unsupported | Deliberately absent from the [release configuration](../.goreleaser.yaml) until install/test/upgrade evidence exists |
| Windows | Unsupported | Rejected by the [installer platform selector](../scripts/install.sh) and absent from the [fixed release targets](../.goreleaser.yaml) |

The CLI platform is independent of the managed target platform. A macOS arm64
control host can manage the Beta Alpine 3.21-3.24 x86_64 targets, but that does not
promote Alpine aarch64 target support.

Docker/Compose, nftables, target-side source builds, and component-root moves
are implemented Preview capabilities and remain outside the v0.1 core/Beta
promise.

<p align="right"><strong>English</strong> | <a href="README.zh.md">简体中文</a></p>

# Alpine 3.21-3.24 libvirt integration

The blocking managed-target gate runs 12 cases on each supported branch,
booting 48 fresh persistent x86_64 VMs. The runner pins these immutable
official images:

| Branch | Image | SHA-512 prefix |
| --- | --- | --- |
| v3.21 | `generic_alpine-3.21.7-x86_64-uefi-cloudinit-r0.qcow2` | `612691a05c8e` |
| v3.22 | `generic_alpine-3.22.5-x86_64-uefi-cloudinit-r0.qcow2` | `132c8f0f3926` |
| v3.23 | `generic_alpine-3.23.5-x86_64-uefi-cloudinit-r0.qcow2` | `7f8818009bb8` |
| v3.24 | `generic_alpine-3.24.1-x86_64-uefi-cloudinit-r0.qcow2` | `ed976ef40de1` |

The versions, architecture, image names, source URLs, and full checksums are
fixed in `alpine-target.sh`. The runner checks Alpine's published sidecar
against the pinned checksum before accepting either a download or cached image.

## Lifecycle

Each case gets an overlay disk, NoCloud seed, generated root SSH key, isolated
NAT network, and a domain whose name starts with `dbf-test-alpineform-`.
Cloud-init installs only that run's public key and writes a completion marker.
The runner verifies `ID=alpine`, the selected exact patch version, APK
architecture `x86_64`, and kernel architecture `x86_64` before invoking
AlpineForm. It rewrites only the temporary case copy from the 3.24 fixture
baseline to the selected branch.

Each numbered configuration runs the applicable blocking phases below in order.
Drift injection and repair run only when that configuration defines a drift
hook:

1. validate and offline plan;
2. online plan and reviewed `apply --auto-approve`;
3. asserted JSON no-op plan and clean `check`;
4. case-specific remote assertions;
5. when defined, out-of-band drift, nonzero `check`, repair, no-op, and clean
   `check`;
6. the configured VM reboot, clean `check`, and persistence assertions.

Later numbered configurations cover removal semantics. The APK case proves
declaration removal is forget-only before an explicit `ensure = "absent"`.
The OpenRC case uses an authored package -> managed configuration -> service
chain. Across all four branches it proves dependency-first initial convergence,
JSON no-op and clean check, drift detection and repair, and dependent-first
explicit cleanup. It then proves that removing declarations under the default
forget policy performs no remote deletion. Package-only changes never activate
the service operation; restart/reload remains tied to actual matching managed
init/conf changes. Plan assertions distinguish effective ordering, structural
triggers, and active triggers; state assertions require authored-only v3
metadata, reference pruning, and no fabricated relationships for forget-only
orphans. This extends the existing case rather than adding a thirteenth case.
The Docker case proves package-version evidence, candidate preflight, protected
values, invalid-daemon isolation, daemon crash recovery, partial/degraded drift
repair, fresh running/stopped reboot persistence, project forget/adopt, scoped
destroy with retained volumes, explicit absence, and service/package removal
ordering.
The components case uses three numbered configurations while remaining one of
the 12 blocking cases. It retains literal-source compatibility and supplies
protected binary, file, archive, and CA-certificate source expressions through
mounted inputs. The single-host sequence uses mirror A and then byte-equivalent
mirror B across configurations. Simultaneous two-host resolution remains
covered by [compiler](../../../internal/core/merge/components_test.go) and
[graph](../../../internal/core/graph/artifact_source_workflow_test.go) contract
tests. Across all four branches the VM case proves wrong-checksum preservation
with redacted debug output, no-op, four-install drift and repair, conservative
cache-loss repair, reboot persistence, exact teardown, and absence after reboot.
The nftables case is the tenth blocking case. Its
`.allow-network-disruption` marker lets only that case add the separate apply
authorization; the layout validator rejects the marker anywhere else. The case
passes 41 explicit assertions covering safe create/update/delete, JSON no-op,
drift and repair, three reboots, invalid syntax and approval refusal without
mutation, external ownership, real SSH loss, local `SIGKILL`, detached and
synchronous confirmed rollback, state preservation, stale-artifact cleanup,
and protected-log scanning.
The source-build case is the eleventh case and the dedicated Preview gate. It
passes exactly 48 explicit assertions per Alpine branch. Its four numbered
configurations compile a checksummed C fixture against musl under the legacy
default root, prove that profile/host/instance root precedence is a complete
no-op, rebuild a workspace larger than a deliberately constrained 2 MiB
`/var/tmp` through the instance root, and retain the existing source and
build-definition drift coverage. The failure loop exercises checksum,
compiler, missing-output, symlink-output, cancellation, selected-root ENOSPC,
and legacy owned-leftover recovery while requiring the prior installation and
protected state to survive. It also checks private ownership, bounded capacity
diagnostics, and cleanup across every candidate root and `/run` input path.
The component-moved case is the twelfth case and has its own Preview gate. It
starts from old worker and source-builder component instances. Together they
cover files, accounts, packages, OpenRC, prebuilt artifacts, scripts, and a
source build. A separate read-only rename-only plan/check requires 18 exact
mappings, 18 no-op resources, zero mutation actions, and byte-identical state
and remote identity snapshots. The numbered rename/update plan has `move=18`,
`update=2`, and `no_op=16`: one file update and its script trigger, with no
service restart or source rebuild. Later phases require a retained-block no-op,
a source-input rebuild with `move=0` through the legacy owner, a removed-block
no-op with normal drift repair, and final component cleanup. The case rejects
duplicate artifact caches, script markers, owner packages, dependency/install
markers, workspaces, or output ownership.
The account and lifecycle cases prove recorded destroy ordering. The layout
validator requires contiguous configs, a check hook for every step, at least
one drift hook per case, pinned offline facts, shell syntax, the nftables-only
risk marker, and no committed keys or state. A case can commit an
`expected-assertions` file to make its exact runtime assertion count blocking.

CI discovers exactly 12 cases and crosses them with four Alpine branches.
The aggregate `Alpine 3.21-3.24 core gate` requires the full 48-job matrix. The
separate nftables, source-build, and component-moved Preview gates prevent those
Preview schemas from passing without their four-branch runtime coverage.

## Run locally

Validate layout without booting a VM:

```sh
make test-integration-layout
```

Run all cases or one case against local `qemu:///system`:

```sh
make test-integration
make test-integration-case CASE=files-directories-secrets
make test-integration-case CASE=nftables
make test-integration-case CASE=component-moved
make ALPINE_BRANCH=v3.21 test-integration-case CASE=facts-state-lock
```

The runner also supports remote libvirt. VM files must live on the hypervisor
storage pool, so the verified image is synchronized there before overlays are
created:

```sh
APF_LIBVIRT_URI=qemu+ssh://ks/system \
APF_INTEGRATION_HYPERVISOR=ks \
APF_INTEGRATION_POOL=vm \
make ALPINE_BRANCH=v3.21 test-integration-case CASE=facts-state-lock
```

Useful environment variables:

| Variable | Purpose |
| --- | --- |
| `APF_INTEGRATION_ALPINE_BRANCH` | Select `v3.21`, `v3.22`, `v3.23`, or `v3.24`; defaults to `v3.24`. |
| `APF_INTEGRATION_CASE` | Run one discovered case. |
| `APF_INTEGRATION_IMAGE_CACHE` | Cache the checksum-verified official image. |
| `APF_INTEGRATION_ARTIFACT_DIR` | Store redacted failure diagnostics. |
| `APF_INTEGRATION_KEEP_WORKDIR=1` | Preserve controller work files for debugging. |
| `APF_INTEGRATION_DISABLE_KVM=1` | Force QEMU software emulation. |
| `APF_LIBVIRT_URI` | Select local or remote libvirt. |
| `APF_INTEGRATION_HYPERVISOR` | SSH host owning remote libvirt files. |
| `APF_INTEGRATION_POOL` | Remote storage pool, default `vm`. |
| `APF_INTEGRATION_REMOTE_BASE_IMAGE` | Hypervisor-side verified base image path. |

## Diagnostics and cleanup

On failure, the runner saves the domain XML, serial console, guest status, and
AlpineForm command logs. Public-key material is redacted, and the scenario
copy containing the private key is never uploaded. Sensitive fixture values
are scanned out of logs before a case can pass.

Exit, failure, interruption, and cancellation all run the same cleanup trap.
It destroys and undefines only the exact generated domain and network, removes
the exact overlay, seed, console log, and helper directory, and removes the
controller work directory unless preservation was requested. The shared
checksum-verified base image is retained as a cache. The component-moved case
also removes its managed remote objects, state, locks, build workspaces,
artifact caches, source-build markers, and script markers before the disposable
VM itself is torn down.

# Alpine 3.24 libvirt integration

The blocking eleven-case managed-target gate boots a fresh persistent Alpine
3.24.1 x86_64 VM for every case. The runner downloads this immutable official
image:

- URL: `https://dl-cdn.alpinelinux.org/alpine/v3.24/releases/cloud/generic_alpine-3.24.1-x86_64-uefi-cloudinit-r0.qcow2`
- SHA-512: `ed976ef40de1f73adcb0a3b253ec9e73e43c408208fcc3c30dcdf7a69b91a387a4777f88c6b72345123edf3832d7cb49403ecce28ec84d496d4b3bad6fbd0923`

The version, architecture, image name, source URL, and checksum are fixed in
`alpine-target.sh`. The runner checks Alpine's published sidecar against the
pinned checksum before accepting either a download or cached image.

## Lifecycle

Each case gets an overlay disk, NoCloud seed, generated root SSH key, isolated
NAT network, and a domain whose name starts with `dbf-test-alpineform-`.
Cloud-init installs only that run's public key and writes a completion marker.
The runner verifies `ID=alpine`, version `3.24.1`, APK architecture `x86_64`,
and kernel architecture `x86_64` before invoking AlpineForm.

Every numbered configuration runs these blocking phases:

1. validate and offline plan;
2. online plan and reviewed `apply --auto-approve`;
3. asserted JSON no-op plan and clean `check`;
4. case-specific remote assertions;
5. out-of-band drift, nonzero `check`, repair, no-op, and clean `check`;
6. VM reboot, clean `check`, and persistence assertions.

Later numbered configurations cover removal semantics. The APK case proves
declaration removal is forget-only before an explicit `ensure = "absent"`.
The Docker case proves package-version evidence, candidate preflight, protected
values, invalid-daemon isolation, daemon crash recovery, partial/degraded drift
repair, fresh running/stopped reboot persistence, project forget/adopt, scoped
destroy with retained volumes, explicit absence, and service/package removal
ordering.
The nftables case is the tenth blocking case. Its
`.allow-network-disruption` marker lets only that case add the separate apply
authorization; the layout validator rejects the marker anywhere else. The case
passes 41 explicit assertions covering safe create/update/delete, JSON no-op,
drift and repair, three reboots, invalid syntax and approval refusal without
mutation, external ownership, real SSH loss, local `SIGKILL`, detached and
synchronous confirmed rollback, state preservation, stale-artifact cleanup,
and protected-log scanning.
The source-build case is the eleventh case and the dedicated Preview gate. It
compiles a checksummed C fixture against musl, proves no-op, source drift,
build-definition drift, installed drift, repair, cleanup, and reboot, then
exercises checksum, compiler, missing-output, symlink-output, cancellation,
ENOSPC, and owned-leftover recovery paths while requiring the prior installation
and protected state to survive.
The account and lifecycle cases prove recorded destroy ordering. The layout
validator requires contiguous configs, a check hook for every step, at least
one drift hook per case, pinned offline facts, shell syntax, the nftables-only
risk marker, and no committed keys or state.

CI discovers exactly eleven cases. The aggregate `Alpine 3.24 core gate` requires
the full matrix, and the separate `Alpine 3.24 nftables Preview gate` prevents
the Preview schema from passing without the rollback case. The separate
`Alpine 3.24 source-build Preview gate` names the source-build failure/recovery
contract independently of prebuilt components.

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
```

The runner also supports remote libvirt. VM files must live on the hypervisor
storage pool, so the verified image is synchronized there before overlays are
created:

```sh
APF_LIBVIRT_URI=qemu+ssh://ks/system \
APF_INTEGRATION_HYPERVISOR=ks \
APF_INTEGRATION_POOL=vm \
APF_INTEGRATION_REMOTE_BASE_IMAGE=/var/lib/libvirt/images/vm/alpine-3.24.1-x86_64-uefi-cloudinit.qcow2 \
make test-integration-case CASE=facts-state-lock
```

Useful environment variables:

| Variable | Purpose |
| --- | --- |
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
checksum-verified base image is retained as a cache.

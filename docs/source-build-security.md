# Target-side source-build security

Target-side component builds are a Preview capability. They execute as root on
the managed Alpine host, so a reviewed build definition and every declared
input are inside the same trust boundary as an AlpineForm configuration.

## Contract and identity

A source component uses `type = "source"`, one `build` block, and one `install`
block. Every build has at least one named input with an exact SHA-256 and a
fixed workspace-relative destination. Inputs come from a controller-local
regular file, inline content, or an HTTP(S) transport locator with no embedded
credentials. The checksum, not a URL or branch name, is the content identity.
An optional `extract` block accepts only `format = "tar.gz"` and a bounded
`strip_components`. Archive listing and extracted output both reject absolute
or parent paths, links, special files, unsafe names, empty results, excessive
entry counts, and paths that collide after stripping.

Commands are repeated `command` blocks whose `argv` value is a non-empty string
array. AlpineForm never accepts a shell command string and never interpolates
argv into remote shell source. Working directory, input destinations, and the
single declared output are clean relative paths. The first Preview contract
fixes `network = "none"`; undeclared downloads and network-enabled builds are
unsupported.

`bubblewrap` is an automatic, visible member of the owned build-dependency
virtual package. Each command gets a new PID, IPC, UTS, cgroup, mount, and
network namespace with all capabilities dropped. The workspace is the only
persistent writable bind; `/tmp` is a private tmpfs. `/bin`, `/sbin`, `/lib`,
and `/usr` are read-only toolchain mounts. Host `/etc`, `/run`, `/var`, state,
SSH material, caches, and install destinations are not mounted. Command output
is discarded, core dumps are disabled, and the shell kills the owned process
group on cancellation.

The build identity covers the resolved component instance, input identities,
argv, protected-value versions, deterministic environment policy, target
platform, APK dependencies, output policy, and install metadata. The graph
uses separate stable addresses for input staging, dependency ownership,
workspace execution, output verification, cleanup, and installation.

Workspace placement is deliberately absent from that identity. Profile/host
`staging.root` and instance `staging_root` select only where the next required
build executes, with `/var/tmp/alpineform/builds` as the fallback. The resolved
path is not serialized into IR, graph, plan, state, HTML, or routine debug
events. A bounded workspace-failure diagnostic may identify the selected root
and work path. A root-only change otherwise remains a no-op while a valid
verified output cache exists; it cannot reinstall the output or activate
`on_change`.

The output policy can bound bytes, require an exact SHA-256, and require the
declared output to be executable. Owner, group, and mode come from `install`.
AlpineForm rejects missing, ambiguous/globbed, linked, parent-linked, special,
oversized, checksum-invalid, or non-executable output before installation.

## Workspace placement and ownership

Configuration accepts only a clean, absolute, non-root path without control
characters, and rejects sensitive or ephemeral roots. Before creating or
removing anything, the target provider validates every existing boundary as a
root-owned non-symlink. The selected root cannot be group- or world-writable.
A writable ancestor is accepted only when it is root-owned and sticky, which
allows conventional paths below `/var/tmp` without accepting an unsafe root.

Missing root directories are created with `umask 077`. A safe existing root is
not needlessly re-moded, so modes such as `0755` remain intact. The actual
workspace is exactly `<root>/<64-hex-build-identity>`; it and its `build` child
must be root-owned directories with mode `0700`. The workspace contains a
root-owned mode-`0600` marker that binds its owner ID, build identity, selected
root, and exact workspace path. A symlink, changed owner or mode, malformed
marker, or mismatched tuple is an ownership failure, not permission to delete.

The dependency marker remains under `/var/lib/alpineform/builds` with mode
`0600`. New markers contain five lines: virtual package, owner ID, build
identity, selected root, and exact workspace. A legacy three-line marker is
interpreted only as the matching workspace below
`/var/tmp/alpineform/builds`; it cannot authorize cleanup under another root.
When a required rebuild changes roots, AlpineForm removes the recorded old
workspace only after all path, ownership, mode, and marker checks pass. Marker
removal is last so a cleanup failure remains retryable.

Protected inline input files remain under `/run/alpineform/build-inputs`;
per-command protected environment/stdin manifests remain under
`/run/alpineform/build-runtime/<owner-id>`. That private mode-`0700` directory
contains a mode-`0600` process marker binding the owner, build identity,
workspace, runtime generation, process group, and Linux process start time. A
supervisor remains the authenticated group/session leader while Bubblewrap or
any live group member remains. Cancellation and retry serialize through a
mode-`0600` per-owner lock in
`/run/alpineform/build-runtime-locks`, validate the record, and use bounded
TERM/KILL teardown. They refuse leaderless groups, PID reuse, marker tampering,
and a changed runtime generation. Lock files contain no protected values and
remain only on reboot-ephemeral storage. Neither runtime path moves below the
configurable workspace root. Persistent dependency markers and verified output
caches also keep their fixed state/cache locations. Prebuilt archives remain a
separate provider path and continue to stage beside their install destination
for same-filesystem atomic replacement.

## Protected values

Protected inline inputs, environment values, and command stdin require a
public version string. Their bytes stay in provider payloads and redacted SSH
stdin; they are absent from graph, plan, state, HTML, debug events, errors, and
command output. Protected values are never placed in remote shell source or
remote command arguments. Build stdout and stderr are omitted rather than
treated as a safe diagnostic channel.

## Ownership and failure behavior

Declared APK build dependencies belong to one address-derived
`.alpineform-build-*` virtual package and a root-only ownership marker. Cleanup
may remove only that exact owned virtual package. APK retains packages that
remain in world or are required by another package.

The stable owner ID is derived from the component resource address and is
separate from the changing build identity. Before mutation AlpineForm checks
the virtual package, `/etc/apk/world`, every declared installed package, and
the ownership marker. A matching leftover marker/package is recoverable after
interruption; a virtual package or marker belonging to another owner is a hard
collision. Failed or cancelled dependency installation removes only the owned
virtual package and marker.

Inputs are verified before dependency installation. Commands run in a
deterministic environment and a network namespace. Output verification stages
one regular, non-symlink file in AlpineForm's cache. The prior installation is
not modified by download, dependency, command, missing-output, checksum,
oversize, or cleanup failure. Only the final provider phase copies the verified
cache into the destination filesystem and atomically replaces the target.

Success, primary failure, cancellation, and interrupted-build recovery all run
owned cleanup. Cleanup-only failure fails apply; when both the primary operation
and cleanup fail, the reported error preserves both causes. Workspace failures
add only bounded placement diagnostics in the form
`staging_root=<path> work_path=<path> available_kib=<number|unknown>` and never
include protected input content.

Declaration removal defaults to `on_remove = "forget"`. Explicit
`on_remove = "destroy"` records only AlpineForm-owned cache, marker, virtual
package, and installation identities. `lifecycle.prevent_destroy` blocks those
recorded destructive actions before provider execution. A target with matching
AlpineForm build and output markers can be adopted; an unmarked target is never
silently claimed.

The verified output is copied to a temporary file in the destination
filesystem, checked again, assigned its final owner/group/mode, and replaced
with no-follow `mv -T`. Build-definition changes are reported as rebuilds;
installed digest or metadata drift is reported as repair. Explicit destroy
rechecks the recorded marker and content digest and refuses to delete a linked,
unowned, or drifted target.

## Threat boundaries

- Untrusted source can exploit the compiler, linker, build tools, or kernel.
  Network isolation reduces reach but does not make root compilation safe.
- Path traversal, symlinks, special files, duplicate extracted paths, and
  archive expansion require provider validation before use.
- Cancellation must terminate the owned process group and run bounded cleanup.
  Deterministic ownership markers allow the next apply to recover leftovers.
- Output capture, disk use, output size, and workspace lifetime are bounded;
  resource exhaustion can still make the host unavailable.
- The verified output is still untrusted executable content. AlpineForm proves
  provenance from declared inputs and commands, not semantic safety.

Operators should use dedicated build hosts when the source or toolchain is not
fully trusted. Do not use Preview source builds as a replacement for a
reproducible, isolated release pipeline.

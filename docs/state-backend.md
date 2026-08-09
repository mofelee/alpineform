# Remote state backend

The default remote state path is `/var/lib/alpineform/state.json`. Reads reject
missing product markers, foreign products, unsupported/newer schemas, and
wrong host identities through the state decoder.

State schema v2 adds `component_identities`, a bounded mapping from a logical
component root to its retained physical component name. It is written only
while tracked resources need an address-derived provider identity that differs
from the current logical name, then pruned after those resources are gone. This
keeps source-build owner IDs, virtual APK packages, marker paths, workspaces,
caches, and recorded outputs stable across a component rename. It does not
contain component input values or provider payloads.

A schema-v2 binary can read schema v1 and normalizes it to v2 in memory. Reads,
online plans, and checks do not persist that conversion. The next state write,
including a moved apply, writes schema v2. A schema-v1 binary rejects that newer
state, so back up every host's schema-v1 file before the first apply with a
schema-v2 binary. Downgrade requires the matching old configuration and binary
plus restoration of that backup; changing `schema_version` or deleting
`component_identities` is not a supported downgrade. See the
[operations runbook](operations-runbook.md#state-backup-and-restore).

Writes prepare a normalized copy, increment its serial, and encode it before
the backend runner is invoked. The remote script creates a mode `0700` state
directory, writes stdin to a mode `0600` temporary file in the same directory,
and atomically renames that file over the state path. Traps remove incomplete
temporary files on failure or cancellation.

State command stdin and remote error output are marked for redaction. Sensitive
resources persist only a protected marker; ephemeral resources persist neither
values nor their desired digest.

## Declarative Component Moves

A top-level `moved` block rebases all state keys below one same-host component
root while preserving each address suffix. Segment-boundary matching means
`component.worker` cannot match `component.worker_old`. Resource ownership,
lifecycle, deletion policy, protected status, observed provider results, and
remote-object identity remain intact. Address-derived desired metadata and
relationships are reconciled against the destination graph rather than blindly
rewriting stored strings.

Resolution is deterministic and idempotent:

- Source present and destination absent: move every matching entry.
- Source absent and destination present: the host is already migrated.
- Both absent: do not fabricate state; plan desired resources normally.
- Both present: fail instead of merging or discarding state.

`plan` and `check` resolve against an in-memory copy and never write state.
During `apply`, AlpineForm recomputes the mapping under the per-host lease,
includes it in locked-plan approval, and writes the complete moved state before
any provider mutation for that host. A move-only apply performs one atomic
write and advances the serial once. If that write fails, the prior state file
remains valid and no provider mutation starts.

If a later provider action fails, the address move can remain committed; keep
the block and retry. Multi-host apply is atomic per host, not across hosts, so a
retry may encounter both completed and pending hosts. Retaining the block makes
that mixture safe: migrated hosts realize no move and pending hosts still have
the instruction.

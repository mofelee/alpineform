<p align="right"><strong>English</strong> | <a href="state-backend.zh.md">简体中文</a></p>

# Remote state backend

The default remote state path is `/var/lib/alpineform/state.json`. Reads reject
missing product markers, foreign products, unsupported/newer schemas, and
wrong host identities through the state decoder.

State schema v2 introduced `component_identities`, a bounded mapping from a
logical component root to its retained physical component name. It is written
only while tracked resources need an address-derived provider identity that
differs from the current logical name, then pruned after those resources are
gone. This keeps source-build owner IDs, virtual APK packages, marker paths,
workspaces, caches, and recorded outputs stable across a component rename.

The current state schema is v3. It retains the v2 component identity map and
adds per-resource authored dependency metadata. A schema-v3 binary reads schema
v1 and v2 and normalizes either to v3 in memory. Reads, online plans, and checks
do not persist that conversion. The next state write, including a no-op apply
that must reconcile metadata or a moved apply, writes schema v3. Schema-v1 and
schema-v2 binaries reject that newer state, so back up every host's current v1
or v2 file before the first state-writing apply with a schema-v3 binary.
Downgrade requires the matching old configuration and binary plus restoration
of that exact backup; changing `schema_version`, deleting dependency metadata,
or deleting `component_identities` is not a supported downgrade. See the
[operations runbook](operations-runbook.md#state-backup-and-restore).

Neither component identities nor resource dependency addresses contain input
values or provider payloads.

## Authored resource dependencies

Each tracked resource can store a sorted, deduplicated `depends_on` array. It
contains only dependencies authored on `packages.package`, `files.file`, or
runtime `services.service` declarations and only while both addresses remain
represented in `resources`. Structural graph parents, inferred
package/account/init/conf prerequisites, APK refresh
ordering, and `triggered_by` relationships are not persisted in this field.

Apply reconciles this metadata against the final tracked resource set even when
every provider action is a no-op. Component moves rebase both resource keys and
dependency addresses. Removing a resource also prunes its address from every
surviving resource, so stale relationships cannot be reintroduced.

For current explicit remote removals, authored edges order a dependent before
the dependency it names. When declarations have already become orphans, the
engine uses the authored relationships retained in prior state to preserve the
same dependent-first order. The default declaration-removal policy is forget,
which removes state ownership without provider deletion and therefore requires
no remote teardown. Dependencies do not select `ensure`, `on_remove`, or
`prevent_destroy` behavior. Addresses are metadata and must not contain
protected data.

Schema v3 is required because lifecycle-critical dependency metadata cannot be
written by a schema-v2 binary. Reusing v2 would let an older writer silently
drop authored edges and make later orphan teardown lose its reviewed order.

Writes prepare a normalized copy, increment its serial, and encode it before
the backend runner is invoked. The remote script creates a mode `0700` state
directory, writes stdin to a mode `0600` temporary file in the same directory,
and atomically renames that file over the state path. Traps remove incomplete
temporary files on failure or cancellation.

State command stdin and remote error output are marked for redaction. Sensitive
and ephemeral resources omit their desired and observed values and persist a
protected marker. Ephemeral resources normally omit their desired digest; a
`DigestSafe` resource contract may retain one computed only from safe desired
metadata. Safe cleanup and status metadata can remain available.

Per-instance prebuilt artifact source evaluation does not add fields beyond the
current schema v3.
Resolved protected URLs and checksums remain in memory and are never written to
state, nor is a cache key or persisted digest derived from either value. State
may retain safe verification status, protection metadata, owner and mode,
deletion policy, a desired digest of safe metadata, and stable cache and delete
paths formed from the retained physical component identity and normalized
source label. Existing literal sources keep their checksum-keyed desired and
state representation, and resource addresses are unchanged.

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

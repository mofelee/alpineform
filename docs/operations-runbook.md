# Operations Runbook

## Before Apply

1. Run `apf validate` and an offline plan in review or CI.
2. Confirm root SSH host-key and identity policy independently with `ssh`.
3. Run online `apf plan` and review destructive, authoritative, and adoption
   actions carefully.
4. Back up `/var/lib/alpineform/state.json` before an upgrade or risky change.
5. Keep the prior `apf` binary and configuration available for alpha rollback.

## State Backup And Restore

On the target, while no apply is active:

```sh
install -m 0600 /var/lib/alpineform/state.json \
  /var/lib/alpineform/state.json.backup
```

Restore only a state file from the same host and a schema the selected binary
understands. Stop concurrent automation first, preserve mode `0600`, and use an
atomic replacement. State restoration does not undo target-side mutations; run
`apf plan` immediately afterward.

The current state schema is v3. Its binary reads schema v1 and v2 and normalizes
either in memory, but the next state write persists v3, which schema-v1 and
schema-v2 binaries reject. Before the first state-writing apply with a schema-v3
binary, back up every selected host's current v1 or v2 state and retain the
matching prior configuration and binary. An online plan/check is read-only and
does not cross this boundary.

To downgrade after v3 has been written, stop apply automation, restore the
host's exact v1 or v2 backup atomically, and use the matching old configuration
and binary. Restoring state does not reverse remote actions performed after the
backup, so review an online plan before approving reconciliation. If no
compatible backup exists, remain on a schema-v3-capable binary; hand-editing
`schema_version`, resource addresses, `depends_on`, or `component_identities`
is unsupported. Schema v2 historically introduced `component_identities`; v3
retains them and adds authored resource dependency metadata.

## Rename A Component Instance

Use a declarative move when a mounted component instance label changes but its
remote objects do not:

```hcl
moved {
  from = component.legacy_worker
  to   = component.worker
}
```

Use this rollout sequence:

1. Rename the mounted component and add the `moved` block in the same
   configuration change. Run `apf validate` and an offline plan to catch static
   endpoint, duplicate, chain, and mount errors without reading remote state.
2. Back up state on every rollout target before its first schema-v3-writing
   apply.
3. Run an online plan for the selected host. Confirm the complete old and new
   addresses under `moves`, verify `summary.move`, and review any real resource
   action or trigger separately.
4. Apply the reviewed locked plan. AlpineForm atomically commits the move for
   that host before any provider mutation.
5. With the block retained, run online plan and `apf check` again. Require no
   pending moves or unintended resource actions.
6. Repeat for every relevant host. Hosts can be migrated in separate batches,
   and a host that still mounts only the source remains unchanged.
7. Remove the block only after all host states have the source prefix absent
   and destination prefix present. A final online plan and check must remain
   clean after removal.

Do not remove the block because the first host or batch is clean. Hosts still
carrying source state would lose their migration instruction and could instead
plan destination creation plus source forget/destroy behavior. This is
especially important when host selection or separate configuration branches
stage the rollout.

If the move prewrite fails, the previous atomic state file remains valid and no
provider mutation starts; keep the block and retry after correcting the state
backend problem. If a later provider action fails, the move may already be
committed. Re-run the online plan and normal apply with the block retained; an
already-migrated host realizes no move. A multi-host failure can leave a safe
mixture of migrated and pending hosts because writes are atomic per host. Do not
hand-edit state or add a reverse move as failure recovery.

## Lock Recovery

The lease is `/run/lock/alpineform/lock`. A normal exit, error, or cancellation
releases it; reboot removes it. If acquisition reports busy, identify and stop
the competing apply instead of deleting a live lease. An expired lease is taken
over atomically by the next contender. Manually remove the directory only after
confirming no owner process or automation is active.

## Failed Apply

AlpineForm persists successful resource state only after the provider sequence
finishes. On failure:

1. Keep the error and structural debug events, but do not publish raw target
   state or secrets.
2. Re-run `apf plan` to inspect the actual partial target state.
3. Fix the target dependency, configuration, transport, or permission issue.
4. Re-run `apf apply`; providers are designed to converge idempotently.
5. Require a JSON no-op plan and clean `apf check` before closing the incident.

Use `apf apply --debug` for structural fact, state, lock, inspect, operation,
apply, and cleanup events. Debug does not include command stdin/output or
protected values.

## Change Or Remove A Dependency Chain

Use resource `depends_on` only among same-scope `packages.package`,
`files.file`, and runtime `services.service` declarations. Review plan
`depends_on` and `triggered_by` separately: a dependency change does not
activate an OpenRC restart/reload or an `on_change` script.

For a package -> managed configuration -> OpenRC service workflow:

1. Add or change the typed relationships, run `apf validate`, and review an
   online plan. On forward apply, require package before file before service.
2. Confirm an OpenRC `operation` is active only when a matching managed init or
   conf file actually changes. A package-only change must leave
   `changes[].triggered_by` empty for the service operation.
3. For deliberate remote cleanup, keep the declarations and relationships in
   place while expressing supported remote intent: stop/disable the service,
   mark the managed file absent, and mark the package absent. The locked plan
   must order the dependent service work before file deletion and file deletion
   before package deletion.
4. Apply, then require a no-op plan and clean check before removing declarations.
   Later declaration removal is default forget and must perform no remote work.

If resource types that support recorded destroy behavior have already become
orphans, state v3 uses their persisted authored relationships for the same
dependent-first teardown. `prevent_destroy` still blocks protected resources.
Do not hand-edit state to add, remove, or reorder dependencies. A failed cleanup
is retried through normal plan/apply after the target-side cause is corrected.

## Rotate A Per-Instance Artifact Source

Prebuilt component mounts can supply `source.url` and `source.sha256` through
typed inputs. Preserve the retained physical component identity (using `moved`
if the logical mount label changes) and normalized source label across a
rotation. Protected caches are keyed by that stable identity, not by a protected
URL or checksum.

1. Change the mounted input values in the protected configuration source. Do
   not place tokens, checksums, or their digests in component labels, source
   labels, paths, or other public identity fields.
2. Run `apf validate`. An unmounted template validates only static shape;
   mounted inputs are normalized and validated before source expressions are
   evaluated.
3. Run an offline plan with declared platform facts to review source selection,
   then an online plan to review selection from observed facts. Protected
   changes remain redacted, so review the affected stable addresses and actions.
4. Apply the locked plan. Download or checksum failure preserves the previous
   verified cache and installation; correct the source and retry normally.
5. Require a JSON no-op plan and clean `apf check` after the rotation.

Do not publish configuration copies, plans, state, debug logs, remote cache
metadata, or failure artifacts merely to diagnose a protected source. After
evaluation, AlpineForm carries the resolved URL and checksum only in controller
memory and redacted provider stdin; serialized surfaces contain only safe
structural, status, and lifecycle metadata.

## Source-Build Failure Recovery

Preview source builds keep the prior installation until input staging,
compilation, output verification, dependency cleanup, and destination staging
all succeed. After a failed or cancelled build, re-run `apf plan`; the owned
`.alpineform-build-*` virtual package, dependency marker, workspace, and
verified-output marker are deterministic and the next apply can reconcile
them.

Do not run `apk del` on compiler/header packages individually. Confirm the
virtual package and marker belong together before manual intervention:

```sh
virtual=.alpineform-build-0123456789abcdef01234567
marker=/var/lib/alpineform/builds/0123456789abcdef0123456789abcdef.dependencies
test -f "$marker"
test "$(sed -n '1p' "$marker")" = "$virtual"
apk info --exists "$virtual"
```

Prefer a normal `apf apply`, which removes only that virtual package and lets
APK retain packages still present in world or required elsewhere. If the
marker/virtual owner does not match the plan, stop: it is an ownership
collision, not stale data to delete. Never publish the marker, workspace,
output cache, state, build stdin/environment, or failure diagnostics before
redaction. Network-enabled builds and unchecked replacement inputs are not a
recovery option.

## nftables Approval And Recovery

Every live nftables create, update, repair, or recorded delete is marked
`risk: network disruption`. Review the exact `(family, name)` table, confirm
out-of-band target access, and pass `--allow-network-disruption` deliberately.
Interactive plan approval and `--auto-approve` are separate decisions and do
not imply this authorization.

The CLI reports only bounded outcomes: confirmed, activation failure with no
rollback required, rollback confirmed, rollback pending, or rollback failed.
To inspect the durable outcome on the target without printing its protected
token digest, read only line two of the fixed table status file:

```sh
family=inet
table=alpineform_filter
status=/var/lib/alpineform/nftables/recovery/$family-$table.status
test -f "$status" && sed -n '2p' "$status"
```

For `pending` or `rollback_pending`, stop new automation and wait at least the
declared `rollback_timeout`. The detached watchdog may still own the live
transaction. Do not delete, rename, copy, or modify anything below
`/run/alpineform/nftables`, and do not restart nftables services. Reconnect
through the original management path, read line two again, and require
`rollback_confirmed` before running `apf plan` and `apf check`.

For `rollback_failed`, use an out-of-band console, keep automation stopped, and
preserve the root-only transaction directory and recovery file. Correct the
reported target-side cause first, such as a full filesystem, unsafe target
type, or failing `nft` command. If exactly one failed transaction remains and
its recorded watchdog process is no longer live, the validated watchdog can
retry the same scoped snapshot restoration without revealing its token:

```sh
family=inet
table=alpineform_filter
status=/var/lib/alpineform/nftables/recovery/$family-$table.status
set -- /run/alpineform/nftables/*
[ "$#" -eq 1 ] && [ -d "$1" ] || exit 1
transaction=$1
[ "$(stat -c '%u:%g:%a' "$transaction")" = 0:0:700 ] || exit 1
[ "$(stat -c '%u:%g:%a' "$transaction/watchdog.sh")" = 0:0:700 ] || exit 1
[ "$(sed -n '1p' "$transaction/status")" = rollback_failed ] || exit 1
pid=$(sed -n '1p' "$transaction/watchdog.pid")
start=$(sed -n '1p' "$transaction/watchdog.start")
[ -n "$pid" ] && [ -n "$start" ] || exit 1
case "$pid:$start" in *[!0-9:]*) exit 1 ;; esac
if [ -r "/proc/$pid/stat" ] &&
  [ "$(awk '{print $22}' "/proc/$pid/stat")" = "$start" ]; then
  exit 1
fi
(cd "$transaction" && sh ./watchdog.sh)
test "$(sed -n '2p' "$status")" = rollback_confirmed
```

The retry revalidates the token-scoped path, family/name identity, snapshot
metadata, and action lock before touching the declared table. If it still
fails, leave all artifacts in place for protected incident analysis. Never use
`nft flush ruleset`, never publish the transaction directory or recovery file,
and never remove failed artifacts merely to make a later apply proceed. After
confirmed recovery, verify the named table, its dedicated persistence, external
tables, `apf plan`, and `apf check` before resuming automation.

## Drift And External Managers

`apf check` exits nonzero for drift and unapplied intent. Do not run competing
managers against the same paths, accounts, packages, services, or sysctls.
Managed APK ownership preserves external lines; authoritative ownership replaces
the entire repository file and must be reviewed as such.

For Docker drift, inspect `rc-service docker status`, `docker info`, and the
declared project's `docker compose ps --all` output directly on the target.
Do not publish `.env`, Compose content, daemon configuration, or container
environment. A Docker-invalid daemon candidate never replaces the current file;
a Compose-invalid candidate never invokes `up`, `stop`, or `down`. Correct the
candidate and re-run the normal plan/apply/check sequence. If a declaration was
forgotten, reintroduce it to adopt/repair the observed project before requesting
explicit `state = "absent"` or `on_remove = "destroy"`. A forgotten project
with write-only content is repaired rather than blindly adopted because its
remote secret cannot be compared after state loss.

## Uninstall

Removing the control-host binary does not change a target. Remove it with:

```sh
rm -f /usr/local/bin/apf
rm -rf /usr/local/share/alpineform
```

Before deleting target state, explicitly converge desired stop, disable,
absence, or recorded destroy behavior. Removing a declaration normally forgets
ownership and deliberately leaves the target object. After reviewing a final
plan, remove target metadata manually only when AlpineForm no longer manages
the host:

```sh
rm -rf /var/lib/alpineform /run/lock/alpineform
```

## Verify A Release

Download one archive, `checksums.txt`, and the Sigstore bundle, then run:

```sh
sha256sum --check --ignore-missing checksums.txt
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp \
    'https://github.com/mofelee/alpineform/.github/workflows/release.yml@refs/tags/v.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
gh attestation verify apf_<tag>_linux_amd64.tar.gz \
  --repo mofelee/alpineform
```

Each archive also has a matching `.sbom.spdx.json` asset.

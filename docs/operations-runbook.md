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

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

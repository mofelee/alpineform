# Security Model

AlpineForm is a root configuration manager. A successful apply can modify the
entire target; configuration, release artifacts, the control host, SSH keys,
and reviewed plans are all part of the trust boundary.

## Transport And Privilege

- v0.1 uses the system OpenSSH client and always connects as root.
- Host-key checks, aliases, proxy jumps, and identity selection remain OpenSSH
  policy. `APF_SSH_CONFIG` can isolate an explicit configuration file.
- AlpineForm enables batch mode, disables forwarding, and bounds connection
  time. It does not implement sudo, doas, password login, or agent policy.
- Remote scripts have fixed source. User-controlled identities and values are
  passed as positional arguments or redacted stdin, not interpolated shell.

## Plan, Lock, And State

Online compilation discovers fixed read-only facts before reading or writing
state. Non-Alpine and platform-mismatched targets fail before state, lock, or
resource mutation. `apply` shows a preview, acquires a renewable exclusive
lease, replans, and requires approval of the locked execution plan.
Nftables mutations add a distinct `network_disruption` plan risk and require
`--allow-network-disruption` at both preview and locked review; ordinary plan
approval and `--auto-approve` cannot silently grant firewall authorization.

State is written atomically to `/var/lib/alpineform/state.json` with directory
mode `0700` and file mode `0600`. The runtime lease lives below `/run/lock` and
does not survive reboot. State is not a secret vault: protect target root access
and do not put plaintext secrets in non-sensitive resource fields.

## Protected Values

Sensitive values are replaced before graph, plan text, plan JSON, HTML, debug,
state, and error serialization. Ephemeral values persist neither their value
nor a content-derived digest. Protected SSH stdin and remote stderr are omitted
from errors. Integration failure artifacts scrub public key material, key blobs,
and the sensitive sentinel; private keys, seed images, state, and scenario
copies are never uploaded.

## Downloads And Components

Component downloads require a declared SHA-256 and are reverified before
installation. Archive extraction rejects traversal, absolute paths, links,
special files, unsafe names, and post-strip collisions. APK repositories accept
HTTPS URLs without embedded credentials, queries, or fragments. AlpineForm does
not invoke distribution upgrades.

Preview target-side builds have a separate
[threat model and ownership contract](source-build-security.md). They require
checksummed inputs and argv commands, disable build-command networking, omit
build logs, and replace an installation only after output verification and
owned cleanup succeed.

## Docker And Compose

Docker packages come only from the supported Alpine repository set or an
explicit tagged APK resource; AlpineForm never adds Docker's upstream or a
Debian repository. Daemon JSON is canonicalized, staged, validated by
`dockerd --validate`, and atomically replaced before the single graph-triggered
OpenRC restart.

Compose and env content travel through protected SSH stdin into a temporary
mode-`0700` directory. `docker compose config --quiet` must accept the complete
candidate before persistent files or runtime state change. Persistent project
files use mode `0600`. Project names and paths are validated provider arguments,
not shell source. Explicit project deletion is label- and path-scoped and never
removes volumes or images. Sensitive or ephemeral payloads, remote stderr, and
content-derived ephemeral digests are omitted from serialized and diagnostic
surfaces.

## nftables

The Preview nftables domain owns only declared `(family, name)` table
identities. It cannot express includes, nested tables, top-level commands, or a
whole-ruleset flush. Existing tables require explicit adoption, and external
tables, stock configuration, and the stock OpenRC service remain outside
AlpineForm ownership.

Rule bodies, active and persistent snapshots, observed fingerprints, runtime
tokens, and token digests stay behind the protected provider boundary. A
root-only detached watchdog snapshots the prior named table and persistence
before activation, then restores them unless a fresh SSH process confirms the
candidate through the configured management path. State is written only after
confirmation. Pending or failed recovery artifacts remain root-only for the
documented [operator recovery procedure](operations-runbook.md); they must not
be published or deleted while a transaction may still be live.

## Release Supply Chain

Release binaries use `CGO_ENABLED=0`, pinned GoReleaser tooling, and four fixed
OS/architecture targets. Releases include SHA-256 checksums, a per-archive SPDX
JSON SBOM, a keyless Sigstore bundle for `checksums.txt`, and GitHub provenance
attestations. The installer verifies the archive checksum before extraction or
replacement. Verification commands are in [the operations runbook](operations-runbook.md).

## Reporting

Report suspected vulnerabilities through GitHub private security advisories as
described in [SECURITY.md](../SECURITY.md). Do not put target details, secrets,
keys, state, plans, debug logs, or failure artifacts in a public issue.

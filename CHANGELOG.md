# Changelog

All notable user-visible changes to AlpineForm are recorded here.

## [Unreleased]

### Added

- Add explicit Alpine 3.21, 3.22, 3.23, and 3.24 managed-target support with
  branch-aware APK and Docker repositories, pinned official cloud images, a
  12-case, 48-job blocking x86_64 VM matrix, and four-branch published-release
  quickstart verification.
- Add a Preview Alpine-native Docker Engine and Compose domain with official or
  explicitly tagged APK sources, OpenRC convergence, group membership,
  validated atomic daemon configuration, deduplicated restart, Compose
  preflight, running/stopped/absent intent, stable observed classification,
  protected env content, and scoped forget/destroy behavior.
- Add a blocking ninth Alpine 3.24.1 x86_64 VM case covering fresh install,
  package versions, no-op, invalid daemon/Compose isolation, crash recovery,
  partial/degraded drift repair, reboot, fresh stopped projects, project
  forget/adopt, scoped destroy, absence, and complete Docker removal.
- Add Preview rollback-safe nftables management with explicit named-table
  ownership, Alpine package and non-flushing OpenRC persistence, protected
  staged activation, detached rollback watchdogs, bounded management-path
  confirmation, scoped delete/forget behavior, and separate
  `--allow-network-disruption` authorization.
- Add a blocking tenth Alpine 3.24.1 x86_64 VM case covering invalid syntax,
  create/update/no-op, active/persistent/marker drift repair, reboot, external
  table preservation, SSH-blocking activation, local process termination,
  detached rollback, stale-artifact cleanup, scoped delete, and scrubbed
  diagnostics.
- Add Preview target-side component source builds with checksummed local,
  inline, remote, and archive inputs; argv commands; deterministic protected
  environments and stdin; network/filesystem isolation; APK virtual-package
  ownership; verified atomic installation; rebuild/repair plans; safe
  forget/destroy behavior; and protected state.
- Add an eleventh Alpine 3.24.1 x86_64 VM case and dedicated Preview gate for
  musl compilation, no-op, source/build/output drift, reboot, checksum,
  compiler, missing/symlink output, cancellation, ENOSPC, secret redaction,
  shared dependency retention, and interrupted-build recovery.
- Add static top-level component-root `moved` blocks with deterministic chain
  and collision validation, host-scoped atomic state migration, retained
  source-build physical ownership, separate text/JSON/HTML move rendering, and
  redaction of protected component values across state, plans, debug,
  diagnostics, and errors.
- Add a twelfth blocking VM case across Alpine 3.21, 3.22, 3.23, and 3.24 plus a
  dedicated component-moved Preview gate. The case covers a read-only
  rename-only review with 18 exact moves, 18 no-op resources, zero mutation
  actions, and unchanged state and physical identities. The numbered lifecycle
  then applies the moves alongside only one legitimate file update and change
  trigger, retains and removes the blocks cleanly, rebuilds a later source-input
  change through the retained physical identity, rejects duplicate ownership,
  and completes exact cleanup.

### Fixed

- Use the portable `apk info -e` package-existence query so Alpine 3.21's
  `apk-tools 2.14` correctly observes package and source-build dependency
  convergence after apply.
- Compare service reload advertisements with the installed OpenRC framework's
  implicit baseline so raw reload hooks work on both OpenRC 0.55 and 0.63
  without accepting an undeclared fallback.

### Compatibility

- Promote persistent Alpine 3.21 through 3.24 x86_64 targets to the v0.1 Beta
  support set. Branches outside this explicit allowlist remain rejected before
  write-capable execution; aarch64 remains Preview without a real-VM gate.
- The Docker DSL and `host.<name>.docker.*` resource addresses are additive
  alpha interfaces. Docker remains Preview and outside the v0.1 core/Beta
  promise.
- The nftables DSL, `host.<name>.nftables.*` resource addresses, and additive
  `network_disruption` plan risk are alpha interfaces. Named-table nftables
  remains Preview despite its blocking rollback gate.
- The source-build DSL and `host.<name>.component.<instance>.build.*` resource
  addresses are additive alpha interfaces. Target-side builds remain Preview,
  require root plus Bubblewrap on persistent Alpine, and do not support build
  command networking or unchecked inputs.
- The `moved` DSL and the `moves`/`summary.move` fields in
  `alpineform.plan.alpha1` are additive alpha interfaces. Moves are state
  migrations and do not change resource action counts. State schema v2 retains
  physical component identities; v2 binaries read v1, but v1 binaries reject
  state after it is written as v2. Component-root moves remain Preview despite
  their four-branch blocking VM case and dedicated aggregate gate.

### Migration Notes

- Before the first apply with schema-v2 moved support, back up each host's
  schema-v1 `/var/lib/alpineform/state.json` and retain the matching prior
  configuration and binary. Downgrade requires restoring that backup; editing
  the schema marker or retained identity map is unsupported.
- For a component instance rename, add the `moved` block with the rename and
  retain it until every host is migrated and plan/check is clean. Removing the
  block after only one host or rollout batch prevents remaining source states
  from migrating. After all hosts are complete, removing the block is a no-op.

## [v0.1.0-alpha.5] - 2026-07-13

### Fixed

- Give each macOS verification result a unique architecture-specific filename
  so `download-artifact` can safely flatten multiple artifacts without
  overwriting one result.
- Include the failed matrix in summary diagnostics while continuing to reject
  incomplete release verification.

## [v0.1.0-alpha.4] - 2026-07-13

### Fixed

- Recursively discover verification result artifacts, parse only known
  `key=yes` records, and reject missing or unknown results before publishing the
  release matrix.
- Preserve alpha.3 as an auditable incomplete release after its publisher and
  all platform verification jobs passed but its final matrix aggregation failed.

### Known Issues

- Publisher, supply-chain, both macOS installers, and fresh-Alpine verification
  passed. The final summary flattened two files both named `macos.env`, so one
  architecture result was overwritten and the matrix correctly remained
  failed. The release is incomplete.

## [v0.1.0-alpha.3] - 2026-07-13

### Changed

- Mark `v0.1.0-alpha.2` incomplete because GitHub provenance persistence is not
  available to a user-owned private repository and downstream release
  verification was skipped.
- Gate release dry-runs and tag publication on GitHub artifact-attestation
  eligibility before creating or uploading any release assets.
- Publish the corrective candidate from a public repository, where GitHub
  artifact attestations are available on the current plan.

### Fixed

- Preserve alpha.1 and alpha.2 as auditable incomplete releases while advancing
  the complete preview to a new immutable prerelease tag.

### Known Issues

- The publisher, GitHub attestation, Linux supply-chain verification, both
  macOS installers, and fresh-Alpine quickstart all passed. The final summary
  failed because downloaded result files were nested below artifact directories
  while the workflow scanned only the top level. The release is incomplete.

## [v0.1.0-alpha.2] - 2026-07-13

### Fixed

- Install the verified Cosign binary under the command name used by the release
  workflow, allowing checksum signing, SBOM upload, provenance, and published
  artifact verification to run.
- Supersede the incomplete `v0.1.0-alpha.1` prerelease without moving its signed
  tag or replacing its partial assets.

### Known Issues

- The release published archives, checksums, a Sigstore bundle, and four SBOMs,
  then GitHub rejected artifact-attestation persistence for the user-owned
  private repository. Downstream installer and fresh-VM verification was
  skipped. This prerelease is incomplete and must not be used.

## [v0.1.0-alpha.1] - 2026-07-13

### Added

- The `apf validate`, `plan`, `apply`, `check`, `fmt`, inspection, and version
  workflows for AlpineForm configuration.
- Alpine 3.24 fact discovery, root SSH transport, atomic remote state, and
  renewable per-host runtime leases.
- Native files, directories, accounts, authorized keys, APK, package, OpenRC,
  hostname, timezone, kernel module, and sysctl convergence.
- Verified binary, file, archive, and CA component artifacts plus deduplicated
  change scripts.
- A blocking Alpine 3.24.1 x86_64 libvirt matrix with no-op, drift, repair,
  lifecycle, secret, lock, and reboot assertions.
- Release automation for reproducible Linux and macOS archives on amd64 and
  arm64, checksum-verified installation, SBOMs, Sigstore signing, and provenance
  attestations.

### Compatibility

- This is the first alpha release. There is no upgrade compatibility promise
  from an older AlpineForm release.
- DSL, CLI, resource addresses, state schema, and plan JSON may change in a
  later prerelease with explicit release notes and migration guidance.

### Known Issues

- The release workflow published archives and checksums, then failed before
  checksum signing, SBOM generation, provenance, and post-release verification.
  This prerelease is incomplete and must not be used.

[Unreleased]: https://github.com/mofelee/alpineform/compare/v0.1.0-alpha.5...HEAD
[v0.1.0-alpha.5]: https://github.com/mofelee/alpineform/compare/v0.1.0-alpha.4...v0.1.0-alpha.5
[v0.1.0-alpha.4]: https://github.com/mofelee/alpineform/compare/v0.1.0-alpha.3...v0.1.0-alpha.4
[v0.1.0-alpha.3]: https://github.com/mofelee/alpineform/compare/v0.1.0-alpha.2...v0.1.0-alpha.3
[v0.1.0-alpha.2]: https://github.com/mofelee/alpineform/compare/v0.1.0-alpha.1...v0.1.0-alpha.2
[v0.1.0-alpha.1]: https://github.com/mofelee/alpineform/releases/tag/v0.1.0-alpha.1

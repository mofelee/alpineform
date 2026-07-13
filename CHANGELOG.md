# Changelog

All notable user-visible changes to AlpineForm are recorded here.

## [Unreleased]

None.

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

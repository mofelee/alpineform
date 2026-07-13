# Changelog

All notable user-visible changes to AlpineForm are recorded here.

## [Unreleased]

None.

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
- Reproducible Linux and macOS archives for amd64 and arm64, checksum-verified
  installation, SBOMs, Sigstore signing, and provenance attestations.

### Compatibility

- This is the first alpha release. There is no upgrade compatibility promise
  from an older AlpineForm release.
- DSL, CLI, resource addresses, state schema, and plan JSON may change in a
  later prerelease with explicit release notes and migration guidance.

[Unreleased]: https://github.com/mofelee/alpineform/compare/v0.1.0-alpha.1...HEAD
[v0.1.0-alpha.1]: https://github.com/mofelee/alpineform/releases/tag/v0.1.0-alpha.1

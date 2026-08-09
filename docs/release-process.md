# Release Process

The first complete public contract is `v0.1.0-alpha.5`. Alpha.1 through alpha.4
are retained as incomplete prereleases for auditability. Releases are built
from a commit whose core CI and release dry-run both passed.

## Artifacts

GoReleaser builds with `CGO_ENABLED=0` and `-trimpath`:

| Platform | Archive |
| --- | --- |
| Linux amd64 | `apf_<tag>_linux_amd64.tar.gz` |
| Linux arm64 | `apf_<tag>_linux_arm64.tar.gz` |
| macOS amd64 | `apf_<tag>_darwin_amd64.tar.gz` |
| macOS arm64 | `apf_<tag>_darwin_arm64.tar.gz` |

Every release includes `checksums.txt`, `checksums.txt.sigstore.json`, and one
`<archive>.sbom.spdx.json` per archive. GitHub provenance attestations cover the
archives listed in the checksum file. Archives contain `apf`, README, license,
notice, changelog, docs, and examples.

Homebrew is deliberately omitted from this release. It cannot be published
until install, test, and upgrade have blocking evidence.

## Pre-Tag Gate

1. Classify DSL, CLI, address, state, plan JSON, installer, and artifact changes
   under [the compatibility policy](compatibility-policy.md).
2. Update `CHANGELOG.md` and the versioned release notes.
3. Run:

   ```sh
   make build
   make check
   make vulncheck
   go mod verify
   goreleaser check
   goreleaser release --snapshot --clean --skip publish
   git diff --check
   ```

4. Run the full Alpine 3.21-3.24 VM matrix and verify exact cleanup. A file or
   CA-certificate component Beta claim requires the blocking `components` case
   on all four branches; expanding that case must not change the expected
   12-case, 48-job matrix cardinality. Resource dependency changes also require
   the existing `openrc` case on all four branches to prove forward ordering,
   no-op, drift repair, reverse explicit cleanup, and default forget without
   adding a case.
5. Confirm GitHub artifact attestations are available. Public repositories pass
   directly; a private Enterprise Cloud repository must explicitly set the
   repository variable `APF_PRIVATE_ATTESTATIONS_ENABLED=true` after confirming
   entitlement.
6. Push the release commit and require its exact-SHA core CI and release dry-run.
7. Test the installer against the snapshot artifacts in an isolated prefix.
8. Create an SSH- or GPG-signed annotated tag and push only that tag.

For per-instance prebuilt artifact source expressions, release review must call
out the additive alpha `source.url`/`source.sha256` boundary and confirm that
literal behavior, resource addresses, state schema v3,
`alpineform.plan.alpha1`, and source-build semantics remain unchanged. Release
notes must also state that protected resolved values remain in-memory-only and
that protected cache identity is based on retained physical component identity
plus the normalized source label rather than protected material.

For resource dependencies, release review must distinguish authored
`depends_on`, inferred ordering, structural and active `triggered_by`, OpenRC
operations, and forget/destroy behavior. State schema v3 reads v1 and v2 and
persists only authored dependencies still represented by tracked resources.
Release notes must require a per-host v1/v2 backup before the first v3-writing
apply and exact backup restoration with the matching configuration and binary
for downgrade; changing the schema marker is unsupported.

## Publish And Verify

The tag workflow reruns unit, race, vet, vulnerability, and release checks,
publishes the four archives, signs checksums keylessly, creates SBOMs and
attestations, then tests installers. Its Linux verification installs the
published binary in a fresh prefix and runs the promoted quickstart against
fresh Alpine 3.21, 3.22, 3.23, and 3.24 VMs.

After workflow success:

1. Verify all expected asset names and nonzero sizes.
2. Verify archive checksums, the Sigstore bundle, and GitHub attestation.
3. Confirm `apf version` reports the tag, release commit, build time, Go version,
   and selected platform.
4. Confirm release notes contain the final verification matrix and known alpha
   limits.
5. Close the release tracker only after fresh-install and VM evidence exists.

Never replace assets under an existing tag. If publishing or verification
fails, correct the workflow or code and issue a new prerelease tag; document any
bad release rather than silently mutating it.

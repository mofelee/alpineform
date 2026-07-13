# Release Process

No complete public contract is currently published. Alpha.1 and alpha.2 are
retained as incomplete prereleases for auditability. Releases are built from a
commit whose core CI and release dry-run both passed.

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

4. Run the full Alpine 3.24 VM matrix and verify exact cleanup.
5. Confirm GitHub artifact attestations are available. Public repositories pass
   directly; a private Enterprise Cloud repository must explicitly set the
   repository variable `APF_PRIVATE_ATTESTATIONS_ENABLED=true` after confirming
   entitlement.
6. Push the release commit and require its exact-SHA core CI and release dry-run.
7. Test the installer against the snapshot artifacts in an isolated prefix.
8. Create an SSH- or GPG-signed annotated tag and push only that tag.

## Publish And Verify

The tag workflow reruns unit, race, vet, vulnerability, and release checks,
publishes the four archives, signs checksums keylessly, creates SBOMs and
attestations, then tests installers. Its Linux verification installs the
published binary in a fresh prefix and runs the promoted quickstart against a
fresh Alpine 3.24.1 VM.

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

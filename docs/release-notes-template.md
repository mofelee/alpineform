# Release Notes Template

Keep every section for each AlpineForm release.

```markdown
## Summary

- <User-visible purpose.>

## Compatibility

- Release phase: <alpha | beta | stable>.
- CLI platforms: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64.
- Beta managed target: Alpine 3.24 x86_64.
- Preview managed target: Alpine 3.24 aarch64.
- Preview capability: rollback-safe named-table nftables on Alpine 3.24 x86_64.
- DSL/state/plan JSON: <compatible | breaking alpha change>.

## Breaking Changes

- <None, or old behavior, new behavior, affected users.>

## Migration Notes

- <None, or exact upgrade and rollback steps.>

## Added

- <Capabilities.>

## Changed

- <Non-breaking behavior changes.>

## Fixed

- <Fixes.>

## Security

- <Security and dependency notes.>

## Known Issues

- <Alpha limits and unsupported paths.>

## Verification

- Commit: `<full SHA>`.
- Local build/check/vulnerability/release snapshot: <result>.
- Alpine 3.24 x86_64 ten-case matrix and aggregate gate: <run URL>.
- Alpine 3.24 x86_64 nftables Preview gate: <run URL>.
- Release dry-run: <run URL>.
- Release workflow: <run URL>.
- Assets, checksums, SBOMs, Sigstore bundle, attestation: <result>.
- Fresh installer and Alpine quickstart VM: <result>.

## Verification Matrix

<Filled or replaced by the release workflow.>
```

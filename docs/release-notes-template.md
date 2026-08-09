# Release Notes Template

Keep every section for each AlpineForm release.

```markdown
## Summary

- <User-visible purpose.>

## Compatibility

- Release phase: <alpha | beta | stable>.
- CLI platforms: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64.
- Beta managed targets: Alpine 3.21-3.24 x86_64.
- Preview managed targets: Alpine 3.21-3.24 aarch64.
- Beta capability: binary and archive components.
- Beta capability: file and CA-certificate components, gated by the four-branch
  `components` case.
- Additive alpha interface: per-instance prebuilt
  `source.url`/`source.sha256` expressions.
- Preview capability: rollback-safe named-table nftables on Alpine 3.21-3.24 x86_64.
- Preview capability: target-side component source builds on Alpine 3.21-3.24 x86_64.
- Preview capability: component-root moved state migrations on Alpine 3.21-3.24 x86_64.
- DSL/state/plan JSON: <compatible | breaking alpha change>; current state
  schema is v2 and plan format is `alpineform.plan.alpha1`.

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
- Alpine 3.21-3.24 x86_64 12-case, 48-job matrix and core gate: <run URL>.
- Blocking `components` case for binary, file, archive, and CA-certificate
  behavior: <result>.
- Alpine 3.21-3.24 x86_64 nftables Preview gate: <run URL>.
- Alpine 3.21-3.24 x86_64 source-build Preview gate: <run URL>.
- Alpine 3.21-3.24 x86_64 component-moved Preview gate: <run URL>.
- Release dry-run: <run URL>.
- Release workflow: <run URL>.
- Assets, checksums, SBOMs, Sigstore bundle, attestation: <result>.
- Fresh installer and Alpine quickstart VM: <result>.

## Verification Matrix

<Filled or replaced by the release workflow.>
```

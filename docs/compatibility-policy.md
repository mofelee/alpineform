# Compatibility Policy

AlpineForm `v0.1.0-alpha.2` is a prerelease. This policy defines what users can
rely on without presenting alpha behavior as stable.

## Versioning

- Tags use semantic versions with a leading `v`.
- Alpha releases may make breaking changes in a later prerelease, but release
  notes must identify the affected CLI, DSL, resource address, state, plan JSON,
  installer, or artifact contract and provide migration or rollback guidance.
- Published tags and their release artifacts are immutable. A correction uses
  a new prerelease tag.
- Stable compatibility is not promised until a non-prerelease release states
  that promise explicitly.

## Configuration And CLI

Accepted block names, attributes, defaults, file discovery, variable
precedence, command names, flags, exit behavior, and human output are alpha
interfaces. Removing or changing them requires a release-note entry.
Automation should prefer plan JSON over parsing text output.

AlpineForm is independently versioned. It does not accept `.dbf.hcl`,
DebianForm variables, DebianForm state, or DebianForm resource addresses.

## Resource Addresses And State

Resource addresses are persisted identities. A change that would reinterpret
an existing address must either provide an explicit migration or reject the
old state. Silent reassignment is forbidden.

State has an AlpineForm product marker, host identity, schema version, serial,
facts, and managed resources. The decoder rejects foreign products, unknown
newer schemas, and wrong-host state. `v0.1.0-alpha.2` has no state migration
command; back up state before upgrading and use the prior binary for rollback.
Never hand-edit state while an apply may be running.

## Plan JSON

The current format is `alpineform.plan.alpha1`. Within a release, identical
offline inputs produce deterministic JSON. A breaking shape or semantic change
must use a new `format_version`; additive fields may be introduced during the
alpha series and consumers must ignore unknown fields.

Sensitive and ephemeral values are never compatibility-visible content. Their
redacted representation may gain metadata but must never reveal a value.

## Managed Target Compatibility

The v0.1 Beta target is the Alpine 3.24 branch on x86_64. Exact patch facts are
observed online; an explicitly declared exact version must match. Alpine branch
promotion or aarch64 target promotion requires a corresponding real-VM gate
and support-matrix update.

## Change Review

Before release, classify changes across DSL, CLI, address identity, state,
plan JSON, provider behavior, installer, and artifacts. Breaking alpha changes
must appear under `Breaking Changes` and `Migration Notes`. If rollback cannot
reuse the prior state safely, the release must say so before the tag is made.

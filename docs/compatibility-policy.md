# Compatibility Policy

AlpineForm `v0.1.0-alpha.5` is a prerelease. This policy defines what users can
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

Top-level `moved` blocks are the explicit migration for a mounted component
instance rename. They are an additive alpha DSL feature, and their endpoints
remain limited to static component roots on the same host. A compatible move
preserves ownership and remote-object identity, is atomic per host and
idempotent on retry, and never turns a move-only rename into a remote resource
action. Weakening endpoint/collision validation, changing payload rebasing, or
changing apply ordering in a way that violates those properties is a breaking
safety change.

State has an AlpineForm product marker, host identity, schema version, serial,
facts, and managed resources. The decoder rejects foreign products, unknown
newer schemas, and wrong-host state. State schema v2 adds bounded retained
component physical identities. A schema-v2 binary reads v1 and writes v2 on its
next state write; a schema-v1 binary rejects v2. Back up every host's v1 state
before the first v2 apply and retain the matching prior configuration and
binary. Downgrade requires restoring that backup. There is no imperative state
migration command or supported hand conversion, and state must never be edited
while an apply may be running.

## Plan JSON

The current format is `alpineform.plan.alpha1`. Within a release, identical
offline inputs produce deterministic JSON. A breaking shape or semantic change
must use a new `format_version`; additive fields may be introduced during the
alpha series and consumers must ignore unknown fields.

The top-level `moves` array and `summary.move` are additive alpha1 fields.
Consumers must treat them as state-address migrations, separate from
create/update/adopt/delete/destroy/forget actions, and continue to ignore
unknown fields.

Sensitive and ephemeral values are never compatibility-visible content. Their
redacted representation may gain metadata but must never reveal a value.

## Managed Target Compatibility

The v0.1 Beta targets are the Alpine 3.21, 3.22, 3.23, and 3.24 branches on
x86_64. Exact patch facts are observed online; an explicitly declared exact
version must match. Branches outside that explicit allowlist are rejected.
Adding or removing a branch, or promoting an aarch64 target, requires a
corresponding real-VM gate and support-matrix update.

## Change Review

Before release, classify changes across DSL, CLI, address identity, state,
plan JSON, provider behavior, installer, and artifacts. Breaking alpha changes
must appear under `Breaking Changes` and `Migration Notes`. If rollback cannot
reuse the prior state safely, the release must say so before the tag is made.
The schema-v2 release notes must require a pre-apply v1 backup, explain that
schema-v1 binaries reject v2, and document backup restoration as the downgrade
boundary.

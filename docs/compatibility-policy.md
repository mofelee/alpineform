<p align="right"><strong>English</strong> | <a href="compatibility-policy.zh.md">简体中文</a></p>

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

## Component Artifact Sources

Allowing mounted component inputs in prebuilt `source.url` and `source.sha256`
is an additive alpha DSL change. Inputs are normalized and validated per mounted
instance before source evaluation and architecture selection. An unmounted
input-dependent template still validates static shape without fabricated input
values.

The expression boundary does not include component `type`, `version`, source
labels, `extract`, `build`, or `install`; target-side source builds keep their
existing separate semantics. Existing literal source behavior, checksum-keyed
caches, resource addresses, desired/state representation, state schema v3, and
`alpineform.plan.alpha1` remain compatible.

Protected resolved URLs and checksums are in-memory payloads, not serialized
compatibility-visible content. Their stable cache identity is based on retained
physical component identity plus normalized source label, never raw or derived
protected material. Changing that identity rule or exposing a protected value
through graph, plan, state, diagnostics, debug, errors, or remote command
logging is a breaking security change.

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
newer schemas, and wrong-host state. Schema v2 introduced bounded retained
component physical identities. Current schema v3 retains them and adds authored
resource dependency metadata. A schema-v3 binary reads v1 and v2, normalizes
them in memory, and writes v3 on its next state write; schema-v1 and schema-v2
binaries reject v3. Back up every host's current v1 or v2 state before its first
v3-writing apply and retain the matching prior configuration and binary.
Downgrade requires restoring that exact backup. There is no imperative state
migration command or supported hand conversion, and state must never be edited
while an apply may be running.

## Resource Relationships

Resource-level `depends_on` is an additive alpha DSL interface limited to static
same-scope references among `packages.package`, `files.file`, and runtime
`services.service` declarations. Generated `openrc.service` declarations remain
outside that authored relationship surface. It adds ordering only and
cannot activate an OpenRC operation or shared `on_change` script. The plan
format remains `alpineform.plan.alpha1`: plan `depends_on` exposes the complete
effective ordering set for current graph resources, while `triggered_by`
exposes the separate structural or active change-trigger set. State-only
orphans do not fabricate a current relationship field. Consumers must not infer
triggering from ordering.

State v3 persists only authored dependency addresses whose targets remain
tracked. Authored graph edges make current explicit remote deletion
dependent-first; persisted v3 metadata preserves that order for later orphan
teardown. Default declaration removal remains state-only forget and does not
delete remote objects. Relationships never choose `ensure`, `on_remove`, or
`prevent_destroy` behavior. Changing the allowed reference scope, trigger
separation, or teardown guarantees requires compatibility and migration review.

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
The schema-v3 release notes must require a pre-write v1 or v2 backup, explain
that older binaries reject v3, and document exact backup restoration with the
matching configuration and binary as the downgrade boundary.

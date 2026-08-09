# DSL And CLI Reference

This page is the v0.1 index. Domain pages define detailed attributes, defaults,
validation, observation, and deletion behavior.

## Commands

| Command | Purpose |
| --- | --- |
| `apf validate` | Parse, type-check, resolve, and validate configuration. |
| `apf plan [--offline]` | Render text or JSON changes; optionally write HTML. |
| `apf apply` | Review, lock, replan, approve, converge, and persist state. |
| `apf check` | Exit nonzero when the observed online plan is not a no-op. |
| `apf fmt` | Validate selected files, then format them atomically. |
| `apf component inspect` | Emit resolved component information. |
| `apf variable inspect` | Emit stable JSON with protected defaults redacted. |
| `apf version` | Print version, commit, build time, Go version, and platform. |

Configuration inputs use repeatable `-f`; variable inputs use `-var-file` and
`-var`. Online commands accept bounded parallelism. `apply` also accepts
`--auto-approve`, `--allow-network-disruption`, `--debug`, and a lock timeout.
The network option is a separate required authorization for live nftables
activation/deletion and is never implied by `--auto-approve`. Use command help
for the exact flag spelling shipped by the installed binary.

## Reusable Model

- `variable` supports typed, validated, sensitive, and ephemeral inputs.
- `locals` contains HCL expressions evaluated after input precedence resolves.
- `assert` rejects a false condition with a declared message.
- `profile` groups host configuration for deterministic imports.
- `component` defines typed reusable native resources, one prebuilt artifact,
  or an independently Preview checksummed source build.
- `moved` preserves component-instance state identity across a reviewed rename.
- `script` defines argv-safe commands or redacted interpreter content and
  optional observed outputs.
- `host` selects SSH, optional offline platform facts, imports, components, and
  native resource domains.

`platform.architecture` and `platform.version` are optional offline assertions.
Online branch, libc, native APK architecture, and kernel architecture are
read-only detected facts.

## Prebuilt Artifact Source Expressions

For `binary`, `file`, `archive`, and `ca_certificate` components, only
`source.url` and `source.sha256` within a prebuilt `source` block may use the
mounted component's `input.*` context. This does not restrict existing input
uses elsewhere in a component. AlpineForm normalizes and validates one mount's
typed inputs, evaluates all of that template's source URL and checksum
expressions, then selects the unlabelled or architecture-labelled source before
graph compilation. Offline selection uses declared `platform.architecture`;
online selection uses observed facts.

An input-dependent component that is not mounted still validates its static
source shape without inventing values for required inputs. Resolved URL and
checksum validation is deferred until a mount supplies normalized values.
Diagnostics identify both the template field and mounted instance.

This is an additive alpha boundary. `type`, `version`, source labels, `extract`,
`build`, `install`, resource addresses, state schema v2, and
`alpineform.plan.alpha1` do not change. Existing literal sources retain their
current behavior and identities. Target-side source builds retain their
separate Preview input model. See [components](components.md#per-instance-source-expressions)
for the complete cache and protected-value contract.

## Component Address Moves

A top-level, unlabeled `moved` block declares that a mounted component instance
was renamed:

```hcl
component "worker_template" {}

moved {
  from = component.legacy_worker
  to   = component.worker
}

host "edge" {
  component "worker" {
    source = component.worker_template
  }
}
```

Both endpoints must be static `component.<instance>` traversals. Strings,
variables, locals, interpolation, function calls, host-qualified addresses,
resource leaves, labels, nested blocks, and attributes other than `from` and
`to` are rejected. Both attributes are required. Validation also rejects
self-moves, duplicate or divergent sources, many-to-one destinations, cycles,
overlapping mappings, and ambiguous chains with more than one mounted instance.
Acyclic historical chains such as `old -> middle -> current` are supported.

The declaration is projected independently onto each host. A host that mounts
the destination can migrate every tracked address below the source root while
preserving its suffix:

```text
host.edge.component.legacy_worker.*
  -> host.edge.component.worker.*
```

A host that still mounts only the source remains unchanged, which supports a
staged rollout. For an applicable host, tracked source state requires the final
destination in that host's desired graph; tracked source and destination roots
cannot be merged. Moves never cross hosts, state backends, or products and do
not rename remote files, packages, accounts, services, interfaces, or other
provider objects.

Online `plan` and `check` resolve moves in memory and remain read-only. `apply`
recomputes the move while holding the host lease, includes it in locked-plan
review, and atomically persists it before provider mutation. A move alone is
not a create, update, adopt, delete, destroy, forget, or change-script trigger.

Treat the block as a temporary migration instruction, not an alias. Keep it in
configuration until every relevant host has been applied and a retained-block
online plan/check is clean. Removing it after only some hosts migrated leaves
the remaining source state without a migration instruction. After every source
prefix is absent and destination prefix is present, remove the block and verify
another online plan/check; completed removal is a no-op. See the
[component-move example](../examples/component-moved.apf.hcl) and
[operations runbook](operations-runbook.md#rename-a-component-instance).

## Native Domains

- `files.file`: [files](files.md)
- `directories.directory`: [directories](directories.md)
- `groups.group`: [groups](groups.md)
- `users.user`, memberships, and keys: [users](users.md)
- `apk.repository`, `apk.key`, and `packages.package`: [APK](apk.md)
- bounded `openrc.service` and runtime `services.service`: [OpenRC](openrc.md)
- `system.hostname` and `system.timezone`: [system](system.md)
- `kernel.module` and `kernel.sysctl`: [kernel](kernel.md)
- prebuilt artifacts and `on_change`: [components](components.md)
- Preview checksummed target-side builds: [components](components.md#preview-source-builds)
- Preview Docker Engine and Compose projects: [Docker](docker.md)
- Preview rollback-safe named tables: [nftables](nftables.md)

Managed resources support explicit presence or absence where documented.
Declaration removal defaults to state-only forget. Resources that support
`on_remove = "destroy"` record provider-safe deletion identity for later
removal. `lifecycle.prevent_destroy` blocks explicit deletion and recorded
destroy before provider execution.

## Output Contracts

Offline plans contain structural and managed graph nodes. Online plans contain
`create`, `update`, `adopt`, `delete`, `destroy`, `forget`, and `no-op` actions.
The machine format is documented in [plan-format.md](plan-format.md). Protected
values never appear in graph, plan, state, HTML, debug, or errors.

<p align="right"><strong>English</strong> | <a href="dsl-reference.zh.md">简体中文</a></p>

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
| `apf fmt` | Check selected files for HCL syntax, then format them. |
| `apf component inspect` | Emit resolved component information. |
| `apf variable inspect` | Emit stable JSON with protected defaults redacted. |
| `apf version` | Print version, commit, build time, Go version, and platform. |

Configuration inputs use repeatable `-f`; variable inputs use `-var-file` and
`-var`. Online commands accept bounded parallelism. `apply` also accepts
`--auto-approve`, `--allow-network-disruption`, `--debug`, and a lock timeout.
The network option is a separate required authorization for live nftables
activation/deletion and is never implied by `--auto-approve`. Use command help
for the exact flag spelling shipped by the installed binary.

`apf fmt` reads and syntax-checks every selected configuration file before it
writes formatted content. It does not load variable inputs or evaluate the
AlpineForm model. Use `apf validate` to parse, resolve, type-check, and
semantically validate configuration.

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

## Source-Build Workspace Roots

Profiles and hosts may set a default target-side source-build workspace root;
one mounted source component may override it:

```hcl
profile "source_build_defaults" {
  staging {
    root = "/srv/alpineform-builds"
  }
}

host "builder" {
  imports = [profile.source_build_defaults]

  staging {
    root = "/mnt/alpineform-host-builds"
  }

  component "tool" {
    source       = component.tool
    staging_root = "/mnt/alpineform-tool-builds"
  }
}
```

Precedence is the component instance's `staging_root`, then the effective host
`staging.root` after normal profile import and host override composition, then
`/var/tmp/alpineform/builds`. A later component-instance declaration replaces
the whole earlier instance: omitting `staging_root` from that replacement falls
back to the effective host default instead of inheriting the replaced value.
A profile or host default may be declared even when no mounted source component
currently uses it. `staging_root` is rejected on non-source components.

Each root must resolve to a non-sensitive, non-ephemeral string. It must be a
clean absolute POSIX path other than `/`, with no control characters; spaces
are allowed. Diagnostics retain the declaring source location. Target-side
ownership, symbolic-link, and mode checks are applied before a build uses the
path; see [source-build security](source-build-security.md#workspace-placement-and-ownership).

The selected root is execution placement, not build content identity. It is
excluded from serialized IR, graph, plan, state, HTML, and routine debug events
and does not change resource addresses, rebuild identity, installation identity,
or `on_change` behavior. A bounded workspace-failure diagnostic may identify
the selected root and derived work path. Changing only the root remains a no-op
while the verified output cache is valid; the next independently required
rebuild uses the newly selected root.

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
`build`, `install`, resource addresses, state schema v3, and
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

## Resource Dependencies

`packages.package`, `files.file`, and runtime `services.service` declarations
accept an additive alpha `depends_on` attribute. Its value is a list of static
typed references:

```hcl
host "edge" {
  packages {
    package "bird" {}
  }

  files {
    file "/etc/conf.d/bird" {
      content    = "BIRD_ARGS=\"-f\"\n"
      depends_on = [package.bird]
    }
  }

  services {
    service "bird" {
      package    = "bird"
      operation  = "restarted"
      depends_on = [file["/etc/conf.d/bird"]]
    }
  }
}
```

Only `package.<label>`, `file.<label>`, and `service.<label>` are accepted, and
they target those three declaration families respectively. A generated
`openrc.service` block neither accepts resource `depends_on` nor becomes a typed
`service.<label>` target by itself. Bracket notation such as
`package["bird-tools"]` is required when a label is not suitable for traversal
syntax. Strings, raw expanded graph addresses,
variables, interpolation, computed indexes, sensitive or ephemeral expressions,
host-qualified addresses, and other resource types are rejected.
References resolve after profile imports and overrides within the same effective
host scope. Inside a component template they resolve only to resources in that
mounted component instance, never to host resources or a sibling component.
Unknown, duplicate, self-referential, and cyclic relationships fail with source
diagnostics.

Authored dependencies add ordering only. Forward apply places the dependency
before its dependent; when both are explicitly removed from the remote host,
the dependent is removed first. A dependency changing never adds
`triggered_by`, so it cannot by itself restart/reload an OpenRC service or run
an `on_change` script. Inferred package, account, init/conf, APK-refresh, and
other prerequisites remain separate. In the example, only an actual change to
the matching managed `/etc/conf.d/bird` file activates the service operation.

Authored relationships are retained in state for safe orphan teardown and
reconciled during no-op applies. Dependencies never select a resource action or
removal policy: `ensure = "absent"`, supported `on_remove = "destroy"`, and
`lifecycle.prevent_destroy` keep their documented meanings. Default declaration
removal forgets state and performs no remote deletion, so it does not turn the
relationship into teardown work. Current graph resources show the complete
effective ordering set in plans; see
[plan relationships](plan-format.md#relationships) and [state dependency
metadata](state-backend.md#authored-resource-dependencies).

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

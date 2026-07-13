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
`--auto-approve`, `--debug`, and a lock timeout. Use command help for the exact
flag spelling shipped by the installed binary.

## Reusable Model

- `variable` supports typed, validated, sensitive, and ephemeral inputs.
- `locals` contains HCL expressions evaluated after input precedence resolves.
- `assert` rejects a false condition with a declared message.
- `profile` groups host configuration for deterministic imports.
- `component` defines typed reusable native resources or one prebuilt artifact.
- `script` defines argv-safe commands or redacted interpreter content and
  optional observed outputs.
- `host` selects SSH, optional offline platform facts, imports, components, and
  native resource domains.

`platform.architecture` and `platform.version` are optional offline assertions.
Online branch, libc, native APK architecture, and kernel architecture are
read-only detected facts.

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

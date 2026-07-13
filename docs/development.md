# Development baseline

AlpineForm's core follows one-way package boundaries:

```text
parser -> merge -> IR -> graph -> plan -> engine -> provider -> backend
                                      |                    |
                                      +------ state -------+
```

- `parser` discovers and decodes AlpineForm configuration and variable inputs.
- `merge` resolves reusable declarations into the intermediate representation.
- `ir` contains resolved, provider-independent desired state.
- `graph` creates resource identities and dependency ordering.
- `plan` compares desired, prior, and observed state without side effects.
- `engine` schedules planning, apply, and check workflows.
- `provider` owns Alpine resource observation and convergence.
- `backend` owns transport, remote state persistence, and runtime locking.
- `state` validates the AlpineForm state envelope and schema compatibility.

The bootstrap implements source discovery, typed variables, locals, input
precedence, product constants, version metadata, and state envelope validation.
`apf validate` checks the implemented language subset. Resource commands return
an explicit bootstrap error. `apf variable inspect` emits stable JSON and
redacts sensitive and ephemeral defaults. `apf fmt` validates every selected
file before writing any formatted content and is idempotent. No Alpine or
Debian resource schema is public yet.

## Implemented language subset

- `variable`, `locals`, root and nested `assert`
- `profile` imports with deterministic component-instance override order
- typed `component` inputs and local instance dependency validation
- metadata-only `script` declarations; execution is intentionally unavailable
- `host` imports and optional offline `platform.architecture` / `version`
- `lifecycle.prevent_destroy` metadata on component instances

Platform architecture is normalized to `amd64` or `arm64`. Alpine branch,
`libc=musl`, and native APK architecture are derived read-only facts. Offline
compilation requires architecture or version only when an expression actually
references the corresponding platform fact.

## Product names

| Surface | Value |
| --- | --- |
| executable | `apf` |
| configuration | `*.apf.hcl` |
| default variables | `alpineform.apfvars[.json]` |
| automatic variables | `*.auto.apfvars[.json]` |
| environment variables | `APF_VAR_<name>` |
| remote state | `/var/lib/alpineform/state.json` |
| runtime lock | `/run/lock/alpineform/lock` |

Variable precedence, from lowest to highest, is declaration default,
`APF_VAR_`, default/automatic variable files, explicit variable files, then
command-line `-var` values. Within one source class, later inputs win.

## Checks

```sh
make build
make check
make vulncheck
git diff --check
```

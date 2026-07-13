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

The bootstrap implements only source discovery, product constants, version
metadata, and state envelope validation. Resource commands return an explicit
bootstrap error. No Alpine or Debian resource schema is public yet.

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

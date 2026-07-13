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

The current core implements source discovery, typed variables, locals, input
precedence, product constants, version metadata, deterministic offline plans,
Alpine facts, root SSH, remote state, runtime leases, online plan/apply/check,
and provider-backed host files, directories, groups, users, supplementary
memberships, authorized keys, APK repositories, APK signing keys, and explicit
APK package world intent.
`apf variable inspect` emits stable JSON and redacts sensitive and ephemeral
defaults. `apf fmt` validates every selected file before writing any formatted
content and is idempotent. No Debian resource schema is exposed.

## Implemented language subset

- `variable`, `locals`, root and nested `assert`
- `profile` imports with deterministic component-instance override order
- typed `component` inputs and local instance dependency validation
- metadata-only `script` declarations; execution is intentionally unavailable
- `host` imports and optional offline `platform.architecture` / `version`
- `lifecycle.prevent_destroy` metadata on component instances
- host-level file, directory, group, user, membership, authorized-key, APK repository, APK key, aggregated APK update, and package resources

Platform architecture is normalized to `amd64` or `arm64`. Alpine branch,
`libc=musl`, and native APK architecture are derived read-only facts. Offline
compilation requires architecture or version only when an expression actually
references the corresponding platform fact.

## Offline plan

`apf plan --offline` renders text or JSON and can atomically write a standalone
HTML report. The `alpineform.plan.alpha1` JSON contract has no generation
timestamp, so identical inputs and argument order produce identical output.
Its graph contains structural `managed=false` nodes for hosts, platform facts,
and component instances. Only future provider-backed `managed=true` nodes
become changes in the plan summary.

Protected desired values are replaced before graph, plan, JSON, or HTML
serialization. `--color auto` honors `NO_COLOR` and non-terminal output;
`--color always` affects text only.

## Online workflow

Online plan/check/apply use a two-phase compile. The first phase extracts only
validated SSH identities, then fixed read-only commands discover target facts.
The second phase recompiles all assertions and resource graphs with those facts
before reading remote state. Unsupported targets and platform mismatches
therefore fail before state, lock, or resource writes.

`apply` reviews the preview before locking. Each host is rebuilt and re-planned
inside its renewable lease, then the actual locked plan is displayed and
approved before provider or state writes. `--parallel` bounds host work while
preserving deterministic result order. Cancellation stops sibling work and the
lease cleanup path still attempts release. `check` returns an error for any
non-no-op action and succeeds for a clean plan.

`apply --debug` emits only structural facts/state/lock/inspect/apply/operation/
cleanup events. Command output, stdin, desired/observed values, and raw
protected errors are never included.

Target facts use a read-only engine capability that is separate from state and
lock backends. See [facts.md](facts.md).

Remote state persistence is described in [state-backend.md](state-backend.md).
Runtime lock behavior is described in [locking.md](locking.md).
Root SSH transport behavior is described in [ssh.md](ssh.md).
Managed file behavior is described in [files.md](files.md).
Managed directory behavior is described in [directories.md](directories.md).
Managed group behavior is described in [groups.md](groups.md).
Managed user behavior is described in [users.md](users.md).
Managed APK repository and key behavior is described in [apk.md](apk.md).
Bounded OpenRC init generation and runtime convergence are described in
[openrc.md](openrc.md).
Alpine hostname and timezone behavior is described in [system.md](system.md).
Alpine kernel module and sysctl behavior is described in [kernel.md](kernel.md).

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

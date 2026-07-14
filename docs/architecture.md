# Architecture

AlpineForm uses one-way core boundaries:

```text
parser -> merge -> IR -> graph -> plan -> engine -> provider -> backend
                                      |                    |
                                      +------ state -------+
```

- `parser` discovers HCL and variable inputs and validates public syntax.
- `merge` resolves profiles, components, expressions, and defaults into IR.
- `ir` holds provider-independent desired state and source locations.
- `graph` assigns stable addresses, dependencies, and change triggers.
- `plan` compares desired, prior, and observed state without side effects.
- `engine` schedules inspect, apply, check, cancellation, and lease workflows.
- `provider` owns Alpine and BusyBox observation and convergence.
- `backend` owns OpenSSH transport, atomic remote state, and runtime leases.
- `state` validates the AlpineForm envelope and schema compatibility.

Offline planning ends after graph compilation. Online planning first compiles
only validated SSH identities, discovers fixed Alpine facts, recompiles the
complete program with those facts, reads state, and inspects managed resources.
The plan is therefore derived from observed state rather than the last state
snapshot alone.

Docker is compiled as a host domain rather than a compatibility component. It
reuses native APK, group, membership, and OpenRC providers while dedicated
daemon-config and Compose providers preserve validation-before-mutation and
runtime observation. The graph orders package/repository readiness before the
daemon, aggregates daemon changes into one service restart, and reverses project,
service, configuration, and package dependencies for explicit absence.

An apply is a two-review transaction: preview, lease acquisition, locked
replan, approval, provider operations, and atomic state persistence. The graph
orders dependencies and aggregates `on_change` or service triggers so one
resolved declaration runs once per host even when several resources changed.

Resource addresses and state schema are compatibility surfaces; see
[the compatibility policy](compatibility-policy.md). Target-side safety and
redaction boundaries are described in [the security model](security-model.md).

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
APK package world intent. Static resource dependencies are resolved after
composition, compiled into the graph, enforced by the engine, and retained in
state as authored-only metadata for potential orphan teardown.
`apf variable inspect` emits stable JSON and redacts sensitive and ephemeral
defaults. `apf fmt` syntax-checks every selected file before writing any
formatted content, reads no variable or runtime inputs, and is idempotent.
`apf validate` owns AlpineForm parsing, resolution, type checking, and semantic
validation. No Debian resource schema is exposed.

## Implemented language subset

- `variable`, `locals`, root and nested `assert`
- `profile` imports with deterministic component-instance and `staging.root`
  override order
- typed `component` inputs, per-instance prebuilt source expressions, composed
  native domains, source-build `staging_root`, and local instance dependency
  validation
- top-level and component-local scripts with reference-identity `on_change`
  aggregation and output observation
- `host` imports and optional offline `platform.architecture` / `version`
- `lifecycle.prevent_destroy` metadata on component instances
- host-level file, directory, group, user, membership, authorized-key, APK
  repository, APK key, aggregated APK update, package, and service resources
- static same-scope `depends_on` references among `packages.package`,
  `files.file`, and runtime `services.service` declarations

Platform architecture is normalized to `amd64` or `arm64`. Alpine branch,
`libc=musl`, and native APK architecture are derived read-only facts. Offline
compilation requires architecture or version only when an expression actually
references the corresponding platform fact.

The parser retains prebuilt `source.url` and `source.sha256` expressions and
their source locations. Merge normalizes and validates each mounted instance's
inputs before evaluating those expressions and selecting the unlabelled or
architecture-specific source. The mounted IR holds protected resolved values
transiently in controller memory; graph compilation keeps them out of serialized
desired data and carries them in an in-memory provider payload. This boundary
does not extend to component `type`, `version`, `extract`, `build`, or `install`,
and it does not change the existing source-build input model.

For source builds, the parser also retains profile/host `staging.root` and
instance `staging_root` values, protection marks, and source locations. Merge
resolves instance, effective host/profile, then product-default precedence and
stores the selected path only in runtime IR fields excluded from JSON. The
build identity document deliberately omits placement. Graph compilation keeps
its stable desired/resource identity and carries the root only in an in-memory
provider payload plus a nonserialized runtime-intent digest. Engine plan-safe
copies clear the host/build placement fields, while graph JSON omits the
runtime payload and digest. Together those boundaries keep the root out of
graph, plan, HTML, debug, and state serialization.

Resource `depends_on` is parsed as typed syntax rather than an ordinary HCL
value. Merge resolves references after profile precedence and separately inside
each mounted component scope. Graph compilation combines those authored edges
with structural and inferred prerequisites but never with `TriggeredBy`.
Execution preserves forward ordering, reverses authored edges for explicit
remote removal, and uses authored-only state metadata for orphan teardown.

## Offline plan

`apf plan --offline` renders text or JSON and can atomically write a standalone
HTML report. The `alpineform.plan.alpha1` JSON contract has no generation
timestamp, so identical inputs and argument order produce identical output.
Its graph contains structural `managed=false` nodes for hosts, platform facts,
and component instances. Provider-backed host and component resources are
`managed=true` nodes and become changes in the plan summary.

For current graph resources, plan `depends_on` is the sorted, deduplicated
effective set of structural, inferred, and authored ordering.
`graph[].triggered_by` is the structural trigger set; online
`changes[].triggered_by` contains only triggers activated by planned changes.
State-only orphans do not fabricate current plan relationships. There is no
public authored-only plan field; state v3 owns that narrow metadata contract.

Protected desired values are replaced before graph, plan, JSON, or HTML
serialization. `--color auto` honors `NO_COLOR` and non-terminal output;
`--color always` affects text only.

Source-build workspace placement follows the same public-output boundary even
though the root itself is not protected configuration. A root-only change does
not alter desired digests or actions when the verified output cache satisfies
the build; runtime provider nodes still receive the current root when a later
independent change requires execution.

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

After execution, apply reconciles authored dependency metadata against the
final tracked state even when provider actions were no-ops. Current explicit
remote removal and state-only orphan removal use dependent-first ordering;
default forget performs no remote deletion.

Nftables activation and deletion are marked `network_disruption` in text and
JSON plans. `apf apply` refuses those steps unless
`--allow-network-disruption` is present; ordinary interactive approval and
`--auto-approve` do not imply that authorization. The preview and every locked
replan are checked independently, so a risk introduced while acquiring the
lease is rejected before provider or state mutation.

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
Component artifacts, composition, and change scripts are described in
[components.md](components.md).

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

`make check` includes the static layout gate for the Alpine 3.21-3.24 libvirt
matrix. The matrix remains 12 cases crossed with four branches (48 jobs). Its
blocking `components` case covers binary, file, archive, and CA-certificate
artifacts; this is the runtime evidence boundary for their Beta status, while
the per-instance source-expression syntax remains additive alpha. The existing
four-branch `openrc` case also proves the package -> managed configuration ->
service dependency lifecycle without increasing matrix cardinality. The
dedicated four-branch `source-build` Preview case carries 48 explicit assertions
per Alpine version for the legacy default, instance precedence over profile/host
candidates, constrained `/var/tmp`, cached no-op, next-rebuild selection,
cleanup, failure preservation, and sandbox/protected-input boundaries. Compiler
tests cover the remaining precedence branches. Source builds remain Preview. Run
`make ALPINE_BRANCH=v3.21 test-integration` for all real-VM cases on one branch
or `make ALPINE_BRANCH=v3.21 test-integration-case CASE=<name>` for one. The
pinned images, lifecycle, case contract, remote-libvirt settings, diagnostics,
and cleanup behavior are documented in
[the integration runbook](../test/integration/libvirt/README.md).

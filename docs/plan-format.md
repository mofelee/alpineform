# Plan format

AlpineForm offline and online plans use
`format_version = "alpineform.plan.alpha1"`.

The JSON document contains:

- `mode`: `offline` for a structural desired-state plan or `online` for an
  observed action plan.
- `command.files`: configuration sources in effective input order.
- `hosts`: sorted compiled host names.
- `summary`: move/create/update/adopt/delete/destroy/forget/no-op counts,
  managed resource count, graph node count, and an additive
  `network_disruption` count when live firewall activation or deletion is
  planned. Actions and risks unused by a mode remain zero or are omitted.
- `moves`: sorted, realized state-address mappings for online plans. Offline
  plans have no state to migrate and emit an empty array.
- `graph`: stable addresses, kinds, managed status, dependency and trigger
  relationships, and source locations. It never contains desired values.
- `changes`: provider-backed managed changes. Online documents include the
  complete action model: `create`, `update`, `adopt`, `delete`, `destroy`,
  `forget`, and `no-op`. Protected desired content is represented only as
  `{ "protected": true }`; observed values and internal fingerprints are not
  serialized. The additive `risks` array contains `network_disruption` for
  nftables create, update, delete, or destroy actions; adopt, forget, and no-op
  do not carry that risk.

Host, platform, and component metadata are structural graph nodes with
`managed = false`; they are auditable but do not imply target-side actions.
The format intentionally omits wall-clock timestamps. Repeated offline plans
are byte-stable when inputs and argument order are unchanged; online plan
identity ignores fact detection time while retaining all semantic facts.

## Moves

Each online move has three required strings:

```json
{
  "host": "edge",
  "from": "host.edge.component.legacy_worker.files.file[\"/etc/worker.conf\"]",
  "to": "host.edge.component.worker.files.file[\"/etc/worker.conf\"]"
}
```

`host` identifies the state owner. `from` and `to` are complete graph addresses
before and after migration. Entries are sorted by `host`, then `from`, then
`to`; `summary.move` is exactly the number of entries. One component-root block
can realize several entries because each persisted resource below that root is
moved separately. An already-migrated host realizes none.

Moves change state identity and are not remote resource actions. A move entry
does not itself increment create, update, adopt, delete, destroy, forget, or
no-op counts, and cannot imply a remote rename or trigger. A real desired-state
change during the same rollout remains a separate entry in `changes` with its
normal action and relationships.

Text output renders each mapping as `-> <from>` followed by `to: <to>`. HTML
uses a separate Moves table. JSON retains every realized leaf mapping. These
representations contain only host and resource addresses, never desired or
observed payloads, component inputs, provider results, or state metadata.
Normal JSON encoding and HTML escaping still apply.

## Relationships

Both `graph[]` nodes and `changes[]` entries can contain the additive
`depends_on` and `triggered_by` arrays. Each array contains only stable resource
addresses, sorted lexically and deduplicated. Consumers of
`alpineform.plan.alpha1` must continue to ignore unknown fields.

`depends_on` records ordering edges. A dependency changing does not by itself
mean that the dependent resource is triggered. `triggered_by` records the
separate change-trigger edges that can activate an operation such as an OpenRC
restart or a shared `on_change` script.

Offline graph nodes and changes show the complete structural relationships. In
an online plan, `graph[].triggered_by` still shows the complete structural edge
set, while `changes[].triggered_by` contains only addresses whose planned
changes activated that operation. Online `changes[].depends_on` continues to
show the complete ordering set.

Text and HTML plans project these same fields with separate `depends_on:` and
`triggered_by:` labels. Relationship rendering never expands an address into a
desired payload, command, sensitive or ephemeral value, or provider detail.
The same address-only boundary applies to `moves`; protected values cannot
appear in a move entry or its summary.

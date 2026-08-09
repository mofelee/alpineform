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

## Component Artifact Inputs

Per-mounted-instance `source.url` and `source.sha256` expressions do not add a
plan field or change `alpineform.plan.alpha1`. Inputs are normalized and the
expressions are evaluated before source selection and artifact graph
compilation. Offline plans select from declared platform facts; online plans
select from observed facts.

Public literal sources retain their existing addresses and desired rendering.
For a protected resolved URL or checksum, `graph` omits the raw payload while
retaining the address, kind, managed status, source location, and relationships.
`changes` retains the safe summary and relationships and uses the existing
`{ "protected": true }` desired representation. Raw values, protected-derived
cache keys, internal provider payloads, and observed protected material are
never serialized to text, JSON, or HTML. Hidden protected intent is not
serialized, but it contributes to the in-memory plan fingerprint used for
preview-versus-locked comparison; changing it therefore requires locked-plan
re-review.

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

For a current desired graph resource, plan `depends_on` is the complete effective
ordering set: structural graph parents, inferred provider prerequisites, and
authored resource `depends_on` edges are combined, sorted, and deduplicated. The
plan does not expose a separate `explicit_depends_on` field. A dependency
changing does not by itself mean that the dependent resource is triggered.
`triggered_by` records the separate change-trigger edges that can activate an
operation such as an OpenRC restart or a shared `on_change` script.

Offline graph nodes and changes for current desired resources show their
complete relationships. In an online plan, current-resource
`graph[].triggered_by` still shows the complete structural trigger set, while
`changes[].triggered_by` contains only addresses whose planned changes activated
that operation. Online `changes[].depends_on` for current graph resources
continues to show the complete effective ordering set.

A state-only orphan has no current desired graph relationship to render, so its
`graph[]` and `changes[]` entries do not fabricate `depends_on`. Prior-state
authored metadata still controls dependent-first orphan execution; consumers
observe that through deterministic `changes[]` ordering rather than a synthetic
relationship field.

State has a deliberately narrower contract: per-resource `depends_on` retains
only authored dependencies whose target resources are still tracked. Inferred
and structural plan ordering and all trigger relationships are recomputed from
configuration. See
[authored state dependencies](state-backend.md#authored-resource-dependencies).

Text and HTML plans project these same fields with separate `depends_on:` and
`triggered_by:` labels. Relationship rendering never expands an address into a
desired payload, command, sensitive or ephemeral value, or provider detail.
The same address-only boundary applies to `moves`; protected values cannot
appear in a move entry or its summary.

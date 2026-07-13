# Plan format

AlpineForm offline and online plans use
`format_version = "alpineform.plan.alpha1"`.

The JSON document contains:

- `mode`: `offline` for a structural desired-state plan or `online` for an
  observed action plan.
- `command.files`: configuration sources in effective input order.
- `hosts`: sorted compiled host names.
- `summary`: create/update/adopt/delete/destroy/forget/no-op counts, managed
  resource count, and graph node count. Actions unused by a mode remain zero.
- `graph`: stable addresses, kinds, managed status, dependencies, and source
  locations. It never contains desired values.
- `changes`: provider-backed managed changes. Online documents include the
  complete action model: `create`, `update`, `adopt`, `delete`, `destroy`,
  `forget`, and `no-op`. Protected desired content is represented only as
  `{ "protected": true }`; observed values and internal fingerprints are not
  serialized.

Host, platform, and component metadata are structural graph nodes with
`managed = false`; they are auditable but do not imply target-side actions.
The format intentionally omits wall-clock timestamps. Repeated offline plans
are byte-stable when inputs and argument order are unchanged; online plan
identity ignores fact detection time while retaining all semantic facts.

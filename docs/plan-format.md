# Offline plan format

AlpineForm offline plans use `format_version = "alpineform.plan.alpha1"`.

The JSON document contains:

- `mode`: always `offline` for this format version.
- `command.files`: configuration sources in effective input order.
- `hosts`: sorted compiled host names.
- `summary`: create/update/delete counts, managed resource count, and total
  structural graph node count.
- `graph`: stable addresses, kinds, managed status, dependencies, and source
  locations. It never contains desired values.
- `changes`: provider-backed managed changes. Protected desired content is
  represented only as `{ "protected": true }`.

Host, platform, and component metadata are structural graph nodes with
`managed = false`; they are auditable but do not imply target-side actions.
The format intentionally omits wall-clock timestamps so repeated offline plans
are byte-stable when inputs and argument order are unchanged.

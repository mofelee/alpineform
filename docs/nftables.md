# nftables management

The nftables domain is Preview. It manages only explicitly declared named
tables. AlpineForm does not offer whole-ruleset ownership, and it never uses a
global flush to converge or delete a table.

Development of this capability is kept off the release branch until the
rollback watchdog, reconnect confirmation, separate network-disruption
approval, and blocking Alpine VM matrix are complete.

## Named-table contract

```hcl
host "edge" {
  nftables {
    table "alpineform_filter" {
      family = "inet"
      content = <<-NFT
        chain input {
          type filter hook input priority 0; policy accept;
          ct state established,related accept
        }
      NFT

      rollback_timeout = "30s"
      adopt_existing    = false
      on_remove         = "forget"

      lifecycle {
        prevent_destroy = true
      }
    }
  }
}
```

The table label is the remote nftables table name. `family` defaults to
`inet`; supported families are `arp`, `bridge`, `inet`, `ip`, `ip6`, and
`netdev`. Identity is the pair `(family, name)`, so the same name in two
families has two distinct resource addresses.

`content` is a table body, not a complete nftables file. AlpineForm supplies
the outer `table <family> <name> { ... }` wrapper. Unbalanced braces,
`include`, nested `table` declarations, and top-level nft command verbs are
rejected before remote access. This prevents the DSL from expressing a global
flush or modifying a second table.

Rules content is a protected provider payload. It is not serialized in the
IR, graph, text plan, JSON plan, HTML plan, state, or debug events. State may
retain a desired digest, family/name identity, the dedicated persistence path,
and the delete behavior. Ephemeral content requires `content_version`; its
content hash and byte count are not retained.

## Ownership and lifecycle

Tables outside the declared `(family, name)` identities are external and must
remain unchanged across create, update, repair, rollback, and delete.

An existing declared table is not silently adopted. The first apply fails
unless the table is already recorded as AlpineForm-owned or
`adopt_existing = true` explicitly authorizes taking ownership and converging
it. Adoption never grants ownership of any other table.

Removing a declaration defaults to `on_remove = "forget"`: AlpineForm removes
only its state record. `on_remove = "delete"` asks a later declaration removal
to delete the recorded owned table. `ensure = "absent"` is an explicit delete
request. Both delete paths target only the recorded family/name and dedicated
persistence file, and both honor `lifecycle.prevent_destroy`. They do not
uninstall nftables, disable OpenRC, remove external configuration, or flush the
ruleset.

## Stable addresses

For table `inet/alpineform_filter` on host `edge`, the public resource address
is:

```text
host.edge.nftables.table["inet/alpineform_filter"]
```

The transaction protocol derives stable internal addresses by appending:

```text
.persistence
.transaction.candidate
.transaction.active
.transaction.watchdog
.transaction.confirmation
```

The package and OpenRC integration addresses are
`host.edge.packages.package["nftables"]` and
`host.edge.nftables.service`. Runtime tokens and snapshots are never part of
these addresses and must never be serialized.

## Alpine 3.24 package and OpenRC layout

AlpineForm's integration evidence uses Alpine 3.24.1 x86_64. Installing the
official `nftables` world intent installs `nftables 1.1.6-r1` together with
`nftables-openrc`. The package owns `/etc/nftables.nft`,
`/etc/conf.d/nftables`, and `/etc/init.d/nftables`.

The stock `/etc/nftables.nft` starts with `flush ruleset`, and the stock
OpenRC service loads that file on start/reload and runs `nft flush ruleset` on
stop. AlpineForm therefore never starts, reloads, stops, rewrites, or adopts
the stock service and its configuration. Existing files at those paths remain
byte-for-byte external configuration.

AlpineForm installs a separate `/etc/init.d/alpineform-nftables` service and
stores root-owned `0600` table files below the root-owned `0700` directory
`/etc/nftables.d/alpineform`. Both the directory and file targets reject
symbolic links and non-matching file types. Updates use a temporary file in the
target directory followed by atomic rename.

The dedicated service is enabled in the `default` runlevel, but it deliberately
does nothing until `/var/lib/alpineform/nftables/armed` exists. The transaction
and watchdog loops create that marker only after a live activation has passed
preflight, snapshot, watchdog, reconnect, and confirmation. Before then, start and reboot cannot
activate merely persisted content. Its stop action never flushes or deletes
any active table.

The Loop 2 Alpine VM matrix proved explicit package installation, exact-file
adoption, no-op, persistence and init drift detection/repair, recorded-table
delete, declaration forget, external configuration preservation, and three
reboots with 61 explicit assertions. The owned table stayed inactive because
the arming marker was absent; the runtime activation and rollback matrix remain
the responsibility of Loops 3 through 6.

## Activation transaction

Each create, update, repair, or recorded delete uses one token-scoped runtime
directory below `/run/alpineform/nftables`. The directory is root-owned `0700`;
candidate, active snapshot, persistent snapshot, marker snapshot, activation,
and restore files are `0600`. The random token and every snapshot remain
provider-only data and are never placed in graph, plan, state, debug events, or
errors.

For a present table, AlpineForm renders the table-body DSL into one complete
`table <family> <name> { ... }` candidate. It then:

1. runs `nft -c -f` against the complete replacement batch;
2. captures the previous stateless active table and exact persistent, observed
   marker, and arming-marker bytes without following symbolic links;
3. validates the active restore batch when an active snapshot exists;
4. starts a detached token-scoped watchdog and verifies that it is alive;
5. preflights again and atomically replaces only the named table with one nft
   batch;
6. creates a new runner and SSH process through the original configured
   management path;
7. confirms the active digest and dedicated OpenRC service, then atomically
   writes and rechecks persistence, the active/fingerprint marker, and the
   arming marker;
8. authenticates confirmation with the unpredictable token and lets the
   watchdog remove the transaction directory.

Delete uses the same protocol with a batch that names only the recorded owned
table. There is no ruleset-wide flush in create, update, repair, rollback, or
delete.

The watchdog runs under a separate session with closed standard streams, so it
does not depend on the initiating SSH process, local `apf` process, or Go
context after it reports ready. A token-scoped action lock serializes fresh
confirmation against timeout rollback. HUP, INT, TERM, preparation failure,
confirmation failure, or timeout restores the active, persistent, observed,
and arming snapshots. Successful confirmation removes the token directory.
AlpineForm state is written only after fresh confirmation and final inspection
return successfully.

Duplicate or expired confirmation is refused because its token directory is no
longer active. A new transaction reaps only completed confirmation/rollback
artifacts and refuses to overlap an active, pending, or failed transaction.
If rollback itself fails, the root-only transaction directory, validated
snapshots, action status, and bounded `rollback_failed` marker remain for
target-side recovery instead of claiming success. Runtime token names,
snapshot content, and rule content are not included in plan, state, graph,
HTML, debug, errors, or uploaded diagnostics.

The Loop 3 Alpine 3.24.1 VM matrix passed 40 assertions. It proved invalid nft
syntax and unsafe snapshot targets cause no mutation, create/no-op and combined
active/persistent drift repair converge, external tables and configuration
survive every non-reboot operation, recorded delete is scoped, reboot content
is valid when a future confirmation marker is simulated, and successful or
rolled-back transactions leave no runtime token artifacts. That matrix was the
pre-watchdog baseline for the independent Loop 4 test below.

The Loop 4 Alpine 3.24.1 VM test passed 14 explicit assertions. It established
a confirmed table, verified fresh-confirm cleanup, applied a table that dropped
SSH, observed the management path fail, killed the local `apf` process with
SIGKILL, and waited for the detached 10-second watchdog. SSH recovered without
the initiating process; the previous active table, persistence, observed and
arming markers, external table, stock configuration, and state hash were
preserved; token artifacts were removed; protected logs contained no rule
content; and the last confirmed table returned after reboot.

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

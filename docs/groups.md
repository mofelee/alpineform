# Managed groups

Host-level `groups.group` resources manage Alpine groups through root SSH:

```hcl
host "node" {
  groups {
    group "app" {
      gid    = 1500
      system = true
    }
  }
}
```

Names use Alpine account syntax. `gid` is optional and accepts an integer from
0 through 2147483647. `system = true` passes BusyBox `addgroup -S` when creating
a group; it does not impose a GID range when an explicit GID is supplied.
AlpineForm observes `/etc/group` and uses only BusyBox `addgroup` and `delgroup`
for creation and deletion. Names and IDs are passed as positional command
arguments and are never interpolated into provider scripts.

When an explicit GID drifts, AlpineForm atomically replaces `/etc/group` while
preserving its owner, group, mode, membership field, and all unrelated records.
It refuses a GID already owned by another group and refuses to change a group
used as a primary group. An omitted GID leaves the allocated ID unmanaged.
Changing a GID does not migrate ownership of unmanaged filesystem entries;
declared files and directories are repaired later in dependency order.

Files and directories owned by a declared present group depend on that group.
The compiler rejects a present path owned by a group declared absent. Declared
absent paths are removed before their absent owning group.

## Deletion

- `ensure = "absent"` explicitly deletes the group.
- Removing a declaration defaults to `on_remove = "forget"`: state ownership
  is removed and the remote group remains.
- `on_remove = "destroy"` records the group name and deletes it when the
  declaration is later removed.
- `lifecycle { prevent_destroy = true }` blocks explicit absence and recorded
  destroy behavior before provider execution.

Deletion refuses GID 0 groups, groups used as a primary group, and groups with
supplementary members. Remove those dependencies explicitly before deleting
the group.

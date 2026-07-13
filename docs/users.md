# Managed users

Host-level `users.user` resources manage Alpine accounts through root SSH:

```hcl
host "node" {
  groups {
    group "app" { gid = 1500 }
  }
  users {
    user "app" {
      uid    = 1500
      group  = "app"
      home   = "/srv/app"
      shell  = "/sbin/nologin"
      system = true
    }
  }
}
```

Names use Alpine account syntax; managing `root` is intentionally unsupported.
`uid` accepts an integer from 1 through 2147483647. Primary `group` accepts an
Alpine group name or numeric ID. `home` must be a clean absolute non-root path,
and `shell` must be a clean absolute path. AlpineForm uses BusyBox `adduser -D`
and `deluser`, with all values passed as positional command arguments.

UID, group, home, and shell are optional. An omitted field keeps the target's
allocation or default unmanaged. `system = true` passes `adduser -S` only when
creating the account. An explicit home is created if missing, must not be a
symbolic link, and receives account ownership. Existing home content is not
moved when the account's home field changes.

Explicit identity drift is repaired through an atomic `/etc/passwd`
replacement that preserves its owner, group, mode, and unrelated records. UID
collisions are rejected. UID changes do not migrate ownership of unmanaged
filesystem entries; declared files and directories are repaired later in
dependency order.

A user with a declared primary group depends on that group. Files and
directories owned by a declared user depend on the user. The compiler rejects
present users or paths that refer to an account declared absent. Explicit
absence is ordered paths, then user, then primary group.

## Deletion

- `ensure = "absent"` explicitly deletes the account.
- Removing a declaration defaults to `on_remove = "forget"`: state ownership
  is removed and the remote account remains.
- `on_remove = "destroy"` records the user name and deletes it when the
  declaration is later removed.
- `lifecycle { prevent_destroy = true }` blocks explicit absence and recorded
  destroy behavior before provider execution.

Deletion refuses UID 0 and users with supplementary group memberships. It does
not remove the user's home or other filesystem data. Memberships must be
removed explicitly before account deletion.

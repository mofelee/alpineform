# Managed directories

Host-level `directories.directory` resources manage directories through root
SSH:

```hcl
host "node" {
  directories {
    directory "/srv/example/data" {
      owner = "app"
      group = "app"
      mode  = "0750"
    }
  }
}
```

Paths must be clean, absolute, and non-root. `owner` and `group` default to
`root`; `mode` defaults to `0755`. AlpineForm creates missing parents, repairs
ownership and mode drift, and refuses to replace a non-directory path.
Account names, numeric IDs, and paths are passed as positional command
arguments and are never interpolated into provider scripts. Symbolic links are
reported as conflicting paths and are never followed or removed.

When managed directory declarations are nested, AlpineForm creates the nearest
declared parent before its child. Managed files likewise depend on their
nearest declared parent directory. Explicit absence is ordered leaf-first when
the descendant files and directories are also declared absent.

## Deletion

Directory deletion is non-recursive by default:

```hcl
directory "/srv/example/data" {
  ensure = "absent"
}
```

This removes an empty directory and fails without changing state when the
directory contains entries. Set `recursive_delete = true` only when the entire
tree may be removed. The same policy is recorded for later declaration
removal.

- `ensure = "absent"` explicitly deletes the directory using the declared
  recursive policy.
- Removing a declaration defaults to `on_remove = "forget"`: state ownership
  is removed and the remote directory remains.
- `on_remove = "destroy"` deletes the directory when the declaration is later
  removed, using the last applied `recursive_delete` value.
- `lifecycle { prevent_destroy = true }` blocks explicit absence and recorded
  destroy behavior before provider execution.

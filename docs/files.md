<p align="right"><strong>English</strong> | <a href="files.zh.md">简体中文</a></p>

# Managed files

Host-level `files.file` resources manage regular files through root SSH:

```hcl
host "node" {
  files {
    file "/etc/example/app.conf" {
      content = "enabled=true\n"
      owner   = "root"
      group   = "root"
      mode    = "0640"
    }
  }
}
```

Exactly one of `content` or `source` is required when `ensure = "present"`.
Relative source paths resolve beside the declaring configuration file. Writes
create a temporary file in the destination directory, apply ownership and
mode, and atomically rename it over the destination. A directory is never
replaced or removed by a file resource.

`owner` and `group` default to `root`; `mode` defaults to `0644`. Account names
and numeric IDs are passed as positional command arguments, never interpolated
into provider scripts. Content is sent only through redacted SSH stdin.

## Ordering and change triggers

A `files.file` declaration can name static `packages.package`, `files.file`, or
runtime `services.service` prerequisites in its own resolved host or
mounted-component scope:

```hcl
packages {
  package "worker-daemon" {}
}

files {
  file "/etc/worker/worker.conf" {
    content    = "enabled=true\n"
    depends_on = [package["worker-daemon"]]
  }
}
```

`depends_on` orders the package before the file on forward apply. It is not a
change trigger: a package update does not run the file's `on_change` script or
an OpenRC operation. Those use separate `triggered_by` relationships and run
only when their matching managed input actually changes. If an explicit remote
cleanup removes both resources, the dependent file is removed before the
package; ordinary declaration removal still defaults to forget and leaves the
remote file and package intact. See
[resource dependencies](dsl-reference.md#resource-dependencies).

## Protected and write-only content

Set `sensitive = true` for content that must be redacted. Sensitivity also
propagates from sensitive variables. Plan, graph, state, debug, and errors omit
the content and expose only protected metadata and, for non-ephemeral content,
a SHA-256/byte-count summary.

Ephemeral content is write-only and requires a public `content_version`:

```hcl
file "/etc/example/token" {
  content         = var.session_token
  content_version = "rotation-2026-07"
  sensitive       = true
  mode            = "0600"
}
```

The content-derived digest is not persisted. AlpineForm uses the public version
plus file metadata for repeatability, so it cannot detect out-of-band content
changes to a write-only file. Changing `content_version` forces a rewrite.

## Deletion

- `ensure = "absent"` explicitly deletes a regular file and must not include
  `content` or `source`.
- Removing a declaration defaults to `on_remove = "forget"`: state ownership is
  removed and the remote file remains.
- `on_remove = "destroy"` records a deletion identity in state and deletes the
  file when the declaration is later removed.
- `lifecycle { prevent_destroy = true }` blocks explicit deletion and recorded
  destroy behavior. Apply once with the guard disabled before removing a
  declaration that should be destroyed.

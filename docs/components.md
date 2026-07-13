# Components, artifacts, and change scripts

Components combine typed inputs with AlpineForm's existing files,
directories, groups, users, packages, OpenRC generation, and service
resources. Each mounted instance keeps its own graph prefix, for example
`host.edge.component.worker.files.file["/etc/worker.conf"]`.

## Prebuilt artifacts

An artifact component declares `type`, one or more verified sources, and an
install destination:

```hcl
component "tool" {
  type    = "binary"
  version = "1.2.3"

  source "amd64" {
    url    = "https://downloads.example.invalid/tool-1.2.3-linux-amd64"
    sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  }

  source "arm64" {
    url    = "https://downloads.example.invalid/tool-1.2.3-linux-arm64"
    sha256 = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
  }

  install {
    path  = "/usr/local/bin/tool"
    owner = "root"
    group = "root"
    mode  = "0755"
  }
}
```

Supported types are `binary`, `file`, `archive`, and `ca_certificate`.
Architecture labels use normalized `amd64` or `arm64` facts. A single
unlabelled `source` is architecture-independent; labelled and unlabelled
sources cannot be mixed. Offline planning needs `platform.architecture` only
when labelled sources must be selected.

Every source must be an absolute HTTP(S) URL without embedded credentials or
a fragment and must include an exact 64-character SHA-256. Downloads enter a
component cache through a temporary file, are verified, and only then replace
the prior cache. Binary and file installs verify the cache again and atomically
replace the destination. Remote checks observe the installed digest,
owner/group, and mode.

`archive` currently accepts `tar.gz` and requires an `extract` block:

```hcl
extract {
  format           = "tar.gz"
  strip_components = 1
}
```

Extraction rejects absolute and parent-traversal paths, links, special files,
unsafe names, and destinations that collide after stripping. It extracts into
an empty staging directory and swaps the destination only after validation;
failures leave the previous installation intact. The installed tree carries a
content manifest used by `check` to detect missing, added, or modified files.

CA certificates must install as `.crt` files below
`/usr/local/share/ca-certificates/`. `update-ca-certificates` and its success
marker are part of the apply transaction. A failed trust refresh is retried
and is never recorded as a successful resource state.

Removing a component destroys its installed artifact and removes its verified
cache. Archive destinations are removed recursively. Use
`lifecycle.prevent_destroy` on the component instance when removal must require
an explicit configuration change.

Target-side `build` blocks are rejected during validation. Source builds are
tracked separately in follow-up issue #14.

## Change scripts

Scripts use either command arrays or interpreter content:

```hcl
script "refresh_worker" {
  commands = [
    ["rc-service", "worker", "reload"],
  ]
  outputs = ["/run/worker.refreshed"]
}

component "worker_config" {
  script "render" {
    interpreter = ["/bin/sh", "-eu"]
    content     = "render-worker-config"
    sensitive   = true
  }

  files {
    file "/etc/worker.conf" {
      content   = "enabled=true\n"
      on_change = global.script.refresh_worker
    }
  }
}
```

`script.<name>` resolves a component-local declaration first, then a top-level
declaration. `global.script.<name>` explicitly selects the top-level
declaration. Deduplication uses the resolved declaration identity on one host,
not the label or command text. Multiple changed files or artifacts referencing
one top-level script therefore produce one operation; an unchanged plan runs
none. Component-local declarations remain distinct per mounted instance.

`outputs` are absolute regular-file paths. After successful execution their
digests and the script declaration digest are recorded in a remote marker.
Missing or changed outputs and changed script bodies rerun the script. Outputs
are observed but are not deleted when the script declaration is removed.

The provider exports `APF_SCRIPT_NAME`, `APF_TRIGGER_ADDRESS`,
`APF_TRIGGER_PATH`, `APF_TRIGGER_ADDRESSES`, and `APF_TRIGGER_PATHS` to each
execution. Commands are passed as positional arguments; content is sent on
redacted stdin. Sensitive script payloads are omitted from graph, plan, state,
HTML, debug output, and provider errors. Script failure aborts apply before a
successful state write.

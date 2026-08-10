<p align="right"><strong>English</strong> | <a href="components.zh.md">简体中文</a></p>

# Components, artifacts, and change scripts

Components combine typed inputs with AlpineForm's existing files,
directories, groups, users, packages, OpenRC generation, and service
resources. Each mounted instance keeps its own graph prefix, for example
`host.edge.component.worker.files.file["/etc/worker.conf"]`.

## Renaming A Mounted Instance

Use a top-level `moved` block when only a mounted component's instance label
changes. AlpineForm migrates every tracked address below the old component root
to the new root without renaming or recreating the remote objects:

```hcl
moved {
  from = component.legacy_worker
  to   = component.worker
}
```

The move preserves resource ownership, lifecycle and deletion policy, observed
provider results, and protected markers. Relationships and address-derived
desired metadata are reconciled from the destination graph. If desired content
also changes, that real update and any legitimate trigger remain separate from
the move in the plan.

Source builds have additional address-derived ownership. State schema v2
introduced the retained legacy physical component name; current schema v3 keeps
it so the existing owner ID, virtual APK package, dependency and installation
markers, workspace/cache/build identity, and recorded outputs remain stable
after the logical rename. A later input change rebuilds and cleans up through
that retained identity instead of creating a second ownership namespace.

Keep the block throughout a staged host rollout, then remove it only after all
hosts have migrated and plan/check is clean with the block retained. See the
[DSL validation and lifecycle](dsl-reference.md#component-address-moves),
[operator procedure](operations-runbook.md#rename-a-component-instance), and
[runnable offline example](../examples/component-moved.apf.hcl).

The four-branch
[`component-moved` VM case](../test/integration/libvirt/cases/component-moved)
starts from old worker and source-builder instances. A separate read-only
rename-only plan/check must show 18 exact moves, 18 no-op resources, zero
mutation actions, and byte-identical state and remote identity snapshots. The
numbered lifecycle then combines the renames with one legitimate file update
and its change script: `update=2`, `no_op=16`, and no create, delete, service
restart, or source rebuild. The case also requires retained-block and
removed-block no-ops, rebuilds a later source-input change through the original
physical source-build identity, and removes the components and their managed
artifacts at the end. Assertions reject duplicate artifact caches, script
markers, source-build owner packages, dependency or install markers,
workspaces, and output ownership.

This real-VM coverage runs on Alpine 3.21 through 3.24 x86_64 and has a
dedicated aggregate gate. It makes moved-state regressions blocking, but does
not promote the additive alpha DSL, v2-origin identity map retained by state v3,
or plan fields into the v0.1 Beta promise; component-root moves remain Preview.

## Resource dependencies inside components

`packages.package`, `files.file`, and runtime `services.service` declarations
inside a component template may use static typed `depends_on` references;
generated `openrc.service` declarations may not. Resolution is local to that
template and is repeated beneath each mounted instance's address prefix. A component
resource cannot reference a host-level resource or a resource in a sibling
component, even when the labels match.

This resource-level syntax is distinct from a mounted component block's
`depends_on = [component.<instance>]`, which orders component roots. Resource
dependencies add ordering only and never activate a component `on_change`
script. Plans show the complete effective ordering for current graph resources;
state v3 retains only authored resource edges whose targets remain tracked so
component moves and orphan teardown preserve them safely. See the canonical
[resource dependency contract](dsl-reference.md#resource-dependencies).

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
Binary and archive components remain Beta. File and CA-certificate components
are also Beta after passing the blocking Alpine 3.21-3.24 `components` matrix on
x86_64. The per-instance source-expression extension described below is an
additive alpha DSL interface; it does not change those runtime support levels.

Architecture labels use normalized `amd64` or `arm64` facts. A single
unlabelled `source` is architecture-independent; labelled and unlabelled
sources cannot be mixed. Offline planning needs `platform.architecture` only
when labelled sources must be selected.

### Per-instance source expressions

`source.url` and `source.sha256` may refer to component inputs. AlpineForm first
normalizes, type-checks, and validates the inputs for one mounted instance,
then evaluates every source expression for that instance, and only then selects
the architecture source and builds its artifact graph:

```hcl
component "tool" {
  input "mirror" {
    type      = string
    sensitive = true
  }

  input "checksum" {
    type      = string
    ephemeral = true
  }

  type    = "binary"
  version = "1.2.3"

  source "amd64" {
    url    = "${input.mirror}/tool-1.2.3-linux-amd64"
    sha256 = input.checksum
  }

  install {
    path = "/usr/local/bin/tool"
    mode = "0755"
  }
}

host "edge_a" {
  platform { architecture = "amd64" }
  component "tool" {
    source = component.tool
    inputs = {
      mirror   = "https://mirror-a.example.invalid"
      checksum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
    }
  }
}

host "edge_b" {
  platform { architecture = "amd64" }
  component "tool" {
    source = component.tool
    inputs = {
      mirror   = "https://mirror-b.example.invalid"
      checksum = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
    }
  }
}
```

An unmounted input-dependent template remains valid when its static shape is
complete. AlpineForm retains the expressions and their source locations without
fabricating required input values; resolved URL and checksum validation occurs
for each mount. Offline compilation selects labelled sources from declared
platform facts, while online compilation selects from observed target facts.

This evaluation boundary is limited to `source.url` and `source.sha256`.
`type`, `version`, source labels, `extract`, `build`, and `install` remain
template-time metadata. Target-side source-build inputs keep their existing
independent Preview semantics.

Literal source declarations retain their existing checksum-keyed caches,
resource addresses, desired/state representation, and provider behavior.
Protected resolved URLs and checksums remain transient controller-memory values:
first in the mounted IR during compilation, then in in-memory provider payloads.
Protection does not introduce a new resource-address scheme: the artifact source
address continues to use the logical mounted component name and normalized
source label. The protected cache path instead uses the retained physical
component identity and normalized source label (`any`, `amd64`, or `arm64`),
never raw or derived protected material. This stable cache identity does not
make every source change an action no-op: changing only the URL or mirror with
the same already verified checksum is a durable no-op, while rotating the
checksum can plan an update or repair. Hidden protected intent participates in
preview-versus-locked comparison, so changing the resolved URL or checksum
requires locked-plan re-review even though the raw values are not serialized.

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
an empty staging directory beside the destination and swaps the destination
only after validation; failures leave the previous installation intact. The
source-build workspace settings below do not relocate this destination-adjacent
staging, because archive replacement must remain on the destination filesystem.
The installed tree carries a content manifest used by `check` to detect
missing, added, or modified files.

CA certificates must install as `.crt` files below
`/usr/local/share/ca-certificates/`. `update-ca-certificates` and its success
marker are part of the apply transaction. A failed trust refresh is retried
and is never recorded as a successful resource state.

The existing `components` VM case exercises binary, file, archive, and
CA-certificate sources on each supported x86_64 branch. It remains one of the
12 blocking integration cases, so the managed-target matrix remains 48 jobs.

Removing a component destroys its installed artifact and removes its verified
cache. Archive destinations are removed recursively. Use
`lifecycle.prevent_destroy` on the component instance when removal must require
an explicit configuration change.

Target-side builds are an independent Preview capability. Their schema,
protected-value rules, ownership, failure behavior, and threat boundary are
documented in [Target-side source-build security](source-build-security.md).
They do not weaken the prebuilt artifact contract above.

## Preview source builds

A source build has fixed inputs, argv commands, one relative output, and a
normal component install destination:

```hcl
component "musl_hello" {
  type = "source"

  build {
    input "source" {
      source      = "fixtures/hello.c"
      sha256      = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
      destination = "hello.c"
    }
    command { argv = ["mkdir", "-p", "build"] }
    command { argv = ["cc", "-Os", "-static", "-o", "build/hello", "hello.c"] }

    output           = "build/hello"
    max_output_bytes = 67108864
    executable       = true
    dependencies     = ["build-base"]
    network          = "none"
    on_remove        = "forget"
  }

  install {
    path = "/usr/local/bin/musl-hello"
    mode = "0755"
  }
}
```

### Workspace placement

Target-side builds use `/var/tmp/alpineform/builds` by default. A merged
profile/host `staging.root` supplies a host default, and a mounted source
component's `staging_root` has highest precedence. See the complete syntax,
validation, and replacement behavior in [the DSL reference](dsl-reference.md#source-build-workspace-roots).

The root is runtime-only placement. It is not serialized and does not enter the
component's build identity, graph identity, state, installation decision, or
change-script trigger. Consequently, changing only the root does not rebuild or
reinstall when the verified output cache remains valid. The next rebuild caused
by an actual input, command, output-policy, platform, dependency, or install
change uses the newly selected root.

Each build gets a root-owned private `<root>/<64-hex-build-identity>` directory
and `build` child, both mode `0700`, plus a mode-`0600` ownership marker.
Persistent dependency ownership remains below `/var/lib/alpineform/builds`,
verified output caches remain below `/var/cache/alpineform/builds`, and
protected ephemeral inputs remain below `/run/alpineform/build-inputs` rather
than moving onto the configurable disk root. Workspace roots and recorded old
paths are accepted or removed only after the provider's ownership, mode,
symbolic-link, and marker checks. See [the security contract](source-build-security.md#workspace-placement-and-ownership).

An input selects exactly one of `source`, `url`, or `content`, always with an
exact `sha256` and a clean workspace-relative `destination`. `source` is a
controller-local regular file below the declaring module directory. `url` is
an HTTP(S) transport locator; its response is not trusted until the checksum
passes. `content` may use protected component inputs and then also requires a
public `content_version`. An input may add:

```hcl
extract {
  format           = "tar.gz"
  strip_components = 1
}
```

`working_directory` defaults to `.`. Every `command` requires `argv`; optional
`stdin` derived from a sensitive or ephemeral value requires `stdin_version`.
`environment` is a string map; protected entries require one public
`environment_version`. `PATH`, loader injection variables, shell startup
variables, `HOME`, and `TMPDIR` cannot be overridden.

`output_sha256` is optional. `max_output_bytes` defaults to 64 MiB and cannot
exceed 1 GiB. `executable = true` adds a pre-install execution-bit check.
Bubblewrap is added automatically to `dependencies`; all dependencies belong
to one address-derived APK virtual package and are removed after verification.
The only network policy is `none`.

Removal defaults to state-only forget. `on_remove = "destroy"` records the
verified installation/cache identity for guarded deletion, and component
`lifecycle.prevent_destroy` blocks it. See the complete runnable
[source-build example](../examples/source-build.apf.hcl).

The dedicated `source-build` VM case runs on Alpine 3.21, 3.22, 3.23, and 3.24
x86_64 with 48 explicit assertions per version. It proves the legacy default,
an instance root winning over profile/host candidates, operation with
constrained `/var/tmp`, cached root-only no-op, next-rebuild placement, guarded
cleanup, failure preservation, and the existing Bubblewrap and protected-input
guarantees. Compiler tests cover the profile-only and host-default branches of
the precedence rule. This blocking gate does not promote target-side source
builds beyond Preview.

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
none. Component-local declarations remain distinct per mounted instance. An
authored resource `depends_on` edge never activates a script; only the separate
`on_change` relationship contributes an active `triggered_by` address.

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

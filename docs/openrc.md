# OpenRC services

AlpineForm separates bounded init-script generation from runtime service
convergence. A host-level `openrc` block generates common `openrc-run` scripts
through the same atomic file provider used by `files.file`:

```hcl
host "edge" {
  openrc {
    service "worker" {
      description        = "Example background worker"
      command            = "/usr/local/bin/worker"
      command_args       = ["--listen", "127.0.0.1:9000"]
      command_user       = "worker"
      directory          = "/srv/worker"
      command_background = true
      pidfile            = "/run/worker.pid"
      need               = ["net"]
      use                = ["logger"]
      conf               = "WORKERS=2\n"
    }
  }

  services {
    service "worker" {
      enabled  = true
      runlevel = "default"
      state    = "running"
      operation = "restarted"
    }
  }
}
```

The generator owns `/etc/init.d/<name>` with executable mode `0755` and, when
`conf` is non-empty, `/etc/conf.d/<name>` with mode `0644`. Both use atomic
root-owned file replacement, repair content/mode/ownership drift, and default
to state-only forget when the declaration is removed.

## Structured boundary

The v0.1 generator accepts only:

- `command`, `command_args`, `command_user`, and `directory`
- `command_background` and `pidfile`
- `description`
- `need`, `use`, `want`, `after`, and `before`
- simple literal `conf` content

Commands, directories, and pidfiles must be clean absolute paths. Service,
account, and dependency names use restricted Alpine/OpenRC identifiers.
Background commands require a pidfile. Arguments and generated assignments
use deterministic POSIX single-quote escaping; values never become generated
shell syntax.

Arbitrary shell functions, start/stop hooks, extra commands, multi-service
expansion, custom runlevel stacking, and supervisor-specific programs are not
part of this model. Manage those complete scripts explicitly with
`files.file` at `/etc/init.d/<name>` and optional `/etc/conf.d/<name>`.

Runtime enablement, runlevel membership, and start/stop behavior are
provided by the separate `services.service` resource.

## Runtime convergence

`services.service` observes an existing executable `/etc/init.d/<name>`, uses
`rc-update` for membership in the selected runlevel, and uses `rc-service` for
runtime state:

```hcl
host "edge" {
  services {
    service "worker" {
      enabled  = true
      runlevel = "default"
      state    = "running"

      package = "worker-daemon"
      user    = "worker"
      group   = "worker"
    }
  }
}
```

`enabled` defaults to `true`, `runlevel` to `default`, and `state` to
`running`. Runtime state is either `running` or `stopped`. Missing,
inactive, started, stopped, and crashed services are classified during
inspection; a missing or crashed service cannot satisfy a running declaration.
Changing a managed runlevel removes the service from its previously managed
runlevel before applying the new membership; unrelated runlevel memberships
are not treated as authoritative.

Optional `operation = "restarted"` or `"reloaded"` runs once after one or
more matching managed init/conf files actually change. Init and conf changes in
the same apply are aggregated into one service operation, while a no-op or a
runlevel-only repair performs no restart/reload. Operations require
`state = "running"` and at least one managed `/etc/init.d/<name>` or
`/etc/conf.d/<name>` trigger. OpenRC always supports restart. Reload is allowed
only for raw init scripts; generated scripts reject it during validation, and a
raw script without a reload command fails apply with an explicit error.

The optional `package`, `user`, and `group` fields must name resources declared
present on the same host and make the service depend on them. A generated
service also depends on its init and conf files. When `command_user` names a
declared present user, that dependency is inferred. Raw scripts managed with
`files.file` at the matching init/conf paths receive the same file ordering.

Removing a service declaration only forgets its state entry. To stop or disable
a service, declare that intent before removing the declaration. Service and
runlevel names use restricted OpenRC identifiers, and the provider passes them
to fixed scripts as positional arguments rather than interpolating shell text.

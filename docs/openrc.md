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

Runtime enablement, runlevel membership, and start/stop/operation behavior are
provided by the separate `services.service` resource.

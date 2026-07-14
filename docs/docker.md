# Docker Engine And Compose

Docker and Compose are a Preview domain for persistent Alpine 3.24 x86_64
hosts. AlpineForm uses Alpine's `docker` and `docker-cli-compose` packages,
the packaged OpenRC service, and the Docker CLI plugin. It never configures a
Docker APT repository, a systemd unit, or Docker's upstream package repository.

```hcl
variable "app_env" {
  type      = string
  sensitive = true
  ephemeral = true
}

host "edge" {
  docker {
    enable  = true
    members = ["deploy"]

    daemon_config = jsonencode({
      log-driver = "json-file"
      log-opts = {
        max-size = "10m"
        max-file = "3"
      }
    })

    project "app" {
      directory = "/srv/app"
      compose = <<-YAML
        services:
          app:
            image: alpine:3.24
            restart: unless-stopped
            command: ["sleep", "infinity"]
      YAML
      env         = var.app_env
      env_version = "production-v1"
      state       = "running"
    }
  }
}
```

## Engine And Package Ownership

The `docker` block accepts:

| Attribute | Default | Meaning |
| --- | --- | --- |
| `ensure` | `present` | `present` manages the engine; `absent` explicitly removes the owned packages after stopping projects and OpenRC. |
| `enable` | `true` | Enables Docker in OpenRC's `default` runlevel and keeps it running. `false` keeps the installed engine stopped and disabled. |
| `package_source` | `alpine` | `alpine`, `custom`, or `none`. |
| `package_repository` | none | Required APK repository tag for `custom`; rejected for other sources. |
| `members` | `[]` | Existing or declared Alpine users that must be supplementary members of the `docker` group. |
| `daemon_config` | unmanaged | A JSON string for `/etc/docker/daemon.json`. |
| `daemon_config_version` | none | Public change token required when the JSON expression is ephemeral. |
| `daemon_config_sensitive` | `false` | Protect the entire daemon-config resource even when its expression is not marked sensitive. |

`package_source = "alpine"` manages the exact official
`https://dl-cdn.alpinelinux.org/alpine/v3.24/community` entry and the explicit
APK world intents `docker` and `docker-cli-compose`. An already equivalent
present repository is reused. If host APK ownership is authoritative, that
repository must be explicitly present in the `apk` block; AlpineForm refuses
to change the authoritative set implicitly.

The 2026-07-14 Alpine 3.24 x86_64 gate resolved `docker 29.5.3-r0` and
`docker-cli-compose 5.1.4-r0`. Patch revisions are not pinned because the stable
branch repository is updated in place; every VM run prints the installed
`apk info -v` results so the exact tested versions remain visible in CI evidence.

`package_source = "custom"` still installs the Alpine package names, but
appends the declared `package_repository` tag to their world intents. The tag
must reference a present tagged repository in the host `apk` block. The
repository and signing-key lifecycle remains owned by that APK declaration.

`package_source = "none"` never changes repositories, APK world intent, or
packages. Docker, its init script, and the Compose plugin must already exist.
AlpineForm can then manage the service, daemon configuration, group membership,
and projects. Missing prerequisites fail apply rather than synthesizing a
third-party source.

The domain owns the package names, the `docker` group, the Docker OpenRC
service, and `/etc/docker/daemon.json`; duplicate generic declarations are
rejected. Removing the whole block forgets these state entries and leaves the
target unchanged. Use `ensure = "absent"` for explicit removal. Package absent
uses `apk del` only for the two recorded world intents; the `docker` group is
forgotten rather than deleted.

## Daemon Configuration And Restart

The compiler requires `daemon_config` to be a JSON object and canonicalizes it
deterministically. Apply sends the content only through protected SSH stdin,
stages it beside the target, runs:

```sh
dockerd --validate --config-file <staged-file>
```

and atomically replaces `/etc/docker/daemon.json` only after validation.
Invalid JSON fails during compilation; a Docker-invalid candidate leaves the
previous file and running daemon unchanged. The provider refuses symbolic links
and non-regular targets.

The graph has one Docker service node. Any daemon-config create, update, or
drift repair triggers that node once after the file succeeds, so several
changes cannot cause several restarts in one plan. Semantically identical JSON
canonicalizes to the same content and performs no restart. Removing only the
`daemon_config` attribute forgets the file and does not delete or restart it;
`docker.ensure = "absent"` stops Docker before deleting the file.

## Compose Projects

Each `project "name"` has a stable identity and accepts:

| Attribute | Default | Meaning |
| --- | --- | --- |
| `directory` | required | Clean absolute project directory, outside `/etc/docker`. |
| `compose` | required | Compose YAML content. AlpineForm owns `<directory>/compose.yaml`. |
| `compose_version` | none | Required public change token for ephemeral Compose content. |
| `env` | absent | Optional env-file content. AlpineForm owns `<directory>/.env`. |
| `env_version` | none | Required public change token for ephemeral env content. |
| `state` | `running` | `running`, `stopped`, or `absent`. |
| `sensitive` | `false` | Protect both managed files and all serialized/diagnostic surfaces. |
| `on_remove` | `forget` | `forget` leaves files and runtime untouched; `destroy` records safe project identity for orphan cleanup. |

Project names use lowercase letters, digits, underscore, and hyphen. Directories
must be unique per host. The generated files are root-owned regular files with
mode `0600`; target symlinks and non-regular files are refused.

For every create or update, AlpineForm writes both desired payloads to a
temporary private directory and runs `docker compose config --quiet` against
that candidate. Only a valid candidate can replace persistent files or invoke
`up`, `stop`, or `down`. `running` executes `up --detach --remove-orphans`;
`stopped` stops existing containers and then runs `create --remove-orphans`, so
a fresh declaration converges to existing but never-started containers. Explicit
`absent` validates the candidate, executes `down --remove-orphans`, and removes
only the two managed files. Named volumes, images, and external resources are
not removed.

Inspection uses Compose's configured service set plus Docker's project/service
labels and reports one stable class:

- `running`: every declared service has running containers and no stopped copy.
- `partial`: declared services are missing or mixed between running and stopped.
- `stopped`: every declared service has stopped/created containers and none run.
- `absent`: no project container exists.
- `degraded`: Docker/config inspection failed, or container labels/states are
  inconsistent with the declared project.

These classes participate in normal no-op and drift repair. A Compose file's
restart policy remains application intent; use a suitable policy when a
`running` project must survive host reboot.

Sensitive values are never placed in graph JSON, plan text/JSON/HTML, state
observations, debug events, provider errors, or integration diagnostics.
Ephemeral content also omits its content-derived digest and requires a public
version token so a changed intent can be planned without persistence.

## Deletion And Recovery

Declaration removal defaults to state-only forget. A project with
`on_remove = "destroy"` records only its name and fixed managed paths. Orphan
destroy first uses the existing valid Compose files; if those are unavailable,
it removes only containers and networks carrying the exact recorded Compose
project label. It never removes volumes or images. `lifecycle.prevent_destroy`
blocks explicit project/engine absence and recorded destroy before provider
execution.

An unprotected forgotten project whose files, metadata, and runtime state still
match can be adopted without a remote write. Write-only content is intentionally
different: after its state entry is lost, AlpineForm cannot prove the remote
secret matches the public version token, so reintroduction plans an update
rather than an unsafe adopt.

For a failed apply, keep Docker running, correct the candidate, and re-run
`apf plan` and `apf apply`. Because state is written only after the full host
sequence succeeds, inspection will show any successfully changed files or
runtime state and the next apply will converge them. See the
[operations runbook](operations-runbook.md) for protected diagnostics.

## Support Boundary

The blocking `docker` libvirt case proves Alpine 3.24.1 x86_64 package install
and version reporting, OpenRC/reboot persistence, Docker-invalid daemon and
Compose-invalid candidate isolation, one-trigger daemon repair, protected env
content, fresh running/stopped projects, partial/degraded drift recovery,
forget/adopt, scoped destroy with retained named volumes, explicit absence, and
complete engine removal. The domain remains Preview because Alpine `community`
has a shorter support window than `main` and no Alpine aarch64 Docker VM gate
exists.

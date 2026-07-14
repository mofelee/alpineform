# AlpineForm

AlpineForm (`apf`) is a declarative configuration tool for Alpine Linux hosts.
It validates HCL configuration, previews changes, converges a target over root
SSH, and reports drift with the same configuration:

```text
apf validate -> apf plan -> apf apply -> apf check
```

AlpineForm is pre-release software and is not an official Alpine Linux project.
The first complete preview is `v0.1.0-alpha.5`. Alpha.1 through alpha.4 are
retained as incomplete prereleases and must not be used. Compatibility
guarantees are documented in [the compatibility policy](docs/compatibility-policy.md).

## Supported Core

The blocking target is persistent Alpine 3.24 x86_64 with OpenRC. The core
manages files, directories, groups, users, authorized keys, APK repositories
and packages, bounded or raw OpenRC services, hostname, timezone, kernel
modules, sysctls, and verified prebuilt components. Every Beta domain runs in
a fresh Alpine 3.24.1 VM through apply, no-op plan, drift and repair where
applicable, and reboot.

Alpine 3.24 aarch64 remains Preview because it has cross-build and selector
coverage but no blocking real-VM gate. Docker Engine and Compose are an
implemented Preview domain with a dedicated Alpine 3.24 x86_64 VM gate; they
remain outside the v0.1 core promise because they depend on Alpine `community`.
Rollback-safe nftables (#13) and target-side source builds (#14) remain
independent follow-ups. See the complete [support matrix](docs/support-matrix.md).

## Install

Release archives are built with `CGO_ENABLED=0` for Linux and macOS on amd64
and arm64. The installer downloads the selected archive and `checksums.txt`,
verifies SHA-256, and atomically installs `apf`:

```sh
curl -fsSL https://raw.githubusercontent.com/mofelee/alpineform/main/scripts/install.sh |
  sh -s -- --version v0.1.0-alpha.5
apf version
```

Install into a private prefix:

```sh
sh scripts/install.sh \
  --version v0.1.0-alpha.5 \
  --prefix "$HOME/.local"
```

Homebrew is not published for this release. It will only be offered after its
install, test, and upgrade paths have real automated evidence.

For a private repository or mirror, run the installer from an authenticated
checkout and export `GITHUB_TOKEN` or `GH_TOKEN`; authenticated release assets
are resolved through the GitHub API.

## Quickstart

The control host needs `apf` and OpenSSH. The managed host must be a persistent
Alpine 3.24 installation reachable as root with a key. Put the target in your
OpenSSH configuration; online fact discovery does not require platform values
in the AlpineForm file:

```sshconfig
Host alpine
  HostName 192.0.2.10
  User root
  IdentityFile ~/.ssh/alpine
  IdentitiesOnly yes
```

[`examples/quickstart.apf.hcl`](examples/quickstart.apf.hcl) creates a small
managed directory and file:

```hcl
host "alpine" {
  ssh {
    host = "alpine"
  }

  directories {
    directory "/etc/alpineform-example" {}
  }

  files {
    file "/etc/alpineform-example/managed.conf" {
      content = "managed-by=alpineform\n"
      mode    = "0644"
    }
  }
}
```

Run the complete workflow:

```sh
apf validate -f examples/quickstart.apf.hcl
apf plan --offline -f examples/quickstart.apf.hcl
apf plan -f examples/quickstart.apf.hcl
apf apply -f examples/quickstart.apf.hcl
apf check -f examples/quickstart.apf.hcl
```

`apply` previews before locking, replans inside a renewable per-host lease, and
asks for approval of the actual locked plan. A clean `check` exits zero; drift
prints the required actions and exits nonzero. Remote state is stored at
`/var/lib/alpineform/state.json` with mode `0600`.

## Configuration

Configuration uses `*.apf.hcl`. Variable inputs use
`alpineform.apfvars[.json]`, `*.auto.apfvars[.json]`, explicit `-var-file`,
`-var`, or `APF_VAR_<name>`. Reusable `profile`, `component`, `script`,
`locals`, `variable`, and `assert` declarations compile into deterministic
resource addresses and dependency order.

Start with the [DSL and CLI reference](docs/dsl-reference.md), then use the
domain guides:

- [files](docs/files.md), [directories](docs/directories.md),
  [groups](docs/groups.md), and [users](docs/users.md)
- [APK and packages](docs/apk.md)
- [OpenRC services](docs/openrc.md)
- [system settings](docs/system.md) and [kernel settings](docs/kernel.md)
- [components and change scripts](docs/components.md)
- [Docker Engine and Compose](docs/docker.md) (Preview)

Operational contracts are covered by the [architecture](docs/architecture.md),
[state backend](docs/state-backend.md), [lock model](docs/locking.md),
[security model](docs/security-model.md), and
[operations runbook](docs/operations-runbook.md).

## Development

```sh
make build
make check
make vulncheck
make test-integration-layout
```

The real-VM harness and remote-libvirt settings are documented in
[the integration runbook](test/integration/libvirt/README.md). Release work
follows [the release process](docs/release-process.md).

## Provenance And License

AlpineForm uses DebianForm v0.6.0 as an architecture and selected-code
reference. [NOTICE.md](NOTICE.md) records the exact upstream commit and major
changes. AlpineForm is independently versioned and does not accept DebianForm
configuration or state. Licensed under the MIT License; see [LICENSE](LICENSE).

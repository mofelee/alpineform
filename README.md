# AlpineForm

AlpineForm is a declarative configuration tool for Alpine Linux hosts. The
current core implements the configuration language, deterministic offline
plans, Alpine 3.24 fact discovery, root SSH, remote state, renewable locks, and
reviewed online plan/apply/check workflows. Provider-backed Alpine resource
domains are being added separately; structural declarations do not pretend to
be remote changes.

The workflow is:

```text
apf validate -> apf plan -> apf apply -> apf check
```

Configuration files use the `*.apf.hcl` suffix. Variable inputs use
`alpineform.apfvars`, `*.auto.apfvars`, or the `APF_VAR_` environment prefix.

## Development

```sh
make build
./apf version
./apf validate -f examples/variables.apf.hcl
./apf variable inspect -f examples/variables.apf.hcl
./apf validate -f examples/model.apf.hcl
./apf component inspect -f examples/model.apf.hcl web_app
./apf plan --offline -f examples/model.apf.hcl --format json --html plan.html
# Online commands use the host SSH identities declared in configuration:
./apf plan -f path/to/hosts.apf.hcl
./apf apply -f path/to/hosts.apf.hcl
./apf check -f path/to/hosts.apf.hcl
./apf fmt -f examples/variables.apf.hcl
make check
```

See [docs/development.md](docs/development.md) for the package boundaries and
current core scope. AlpineForm is derived from the architecture and
selected code patterns of DebianForm v0.6.0; see [NOTICE.md](NOTICE.md).

The current model accepts reusable profiles, typed component metadata,
component instances, assertions, lifecycle metadata, and offline Alpine
platform declarations. Host-level `files.file`, `directories.directory`, and
`groups.group` resources provide native convergence; see
[docs/files.md](docs/files.md), [docs/directories.md](docs/directories.md), and
[docs/groups.md](docs/groups.md).

Online commands first discover and validate Alpine 3.24 facts through fixed
read-only commands. `apply` shows a preview, acquires each host's runtime lease,
rebuilds the plan, and requires approval of the locked plan before persisting
facts, state, or provider changes. `--parallel` bounds concurrent hosts;
`--lock-timeout` bounds lease acquisition; `apply --debug` emits structural,
redacted lifecycle events.

Offline plans include a deterministic structural graph. Host, platform, and
component declarations are marked unmanaged until their Alpine providers are
implemented, so the plan does not misrepresent metadata as a remote change.

## License

MIT. See [LICENSE](LICENSE).

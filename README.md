# AlpineForm

AlpineForm is a declarative configuration tool for Alpine Linux hosts. The
project is currently being bootstrapped; Alpine resource management is not yet
available.

The future workflow is:

```text
apf validate -> apf plan -> apf apply -> apf check
```

Configuration files use the `*.apf.hcl` suffix. Variable inputs use
`alpineform.apfvars`, `*.auto.apfvars`, or the `APF_VAR_` environment prefix.

## Development

```sh
make build
./apf version
make check
```

See [docs/development.md](docs/development.md) for the package boundaries and
current bootstrap scope. AlpineForm is derived from the architecture and
selected code patterns of DebianForm v0.6.0; see [NOTICE.md](NOTICE.md).

## License

MIT. See [LICENSE](LICENSE).

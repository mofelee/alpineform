# Repository Guidelines

## Structure

AlpineForm is a Go CLI module with the `apf` entrypoint in `cmd/apf/`. Core
code is layered under `internal/core/`: parser, merge/IR, graph, plan, engine,
provider, backend, and state. Product-wide names and paths live in
`internal/product`; build version metadata lives in `internal/version`.

Do not add APT, systemd, Debian codename, glibc locale, Docker, nftables, or
source-build schema to the v0.1 core. Keep parsing and compilation separate
from remote execution.

## Validation

Run `make check` for race tests, vet, and formatting. Run `make vulncheck` when
dependencies or release gates change. Use `git diff --check` before committing.

## Style

Use `gofmt`, package-local tests, and concise Conventional Commit-style commit
subjects. Configuration fixtures use `*.apf.hcl`; variable fixtures use
`*.apfvars` or `*.apfvars.json`.

Never commit secrets, SSH keys, state files, VM artifacts, or generated
binaries. Sensitive and ephemeral values must not appear in plan, state,
graph, HTML, debug, or error output.

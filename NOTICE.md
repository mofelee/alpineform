# Provenance

AlpineForm uses DebianForm v0.6.0 as an architecture and selected-code
reference:

- upstream repository: <https://github.com/mofelee/debianform>
- upstream commit: `843c5e8251f36cdae426d3ba58c209e71d1da867`
- upstream license: MIT, copyright 2026 mofelee

The initial AlpineForm bootstrap reuses the high-level layering, configuration
source ordering, version metadata pattern, and state validation approach. The
typed value, input type, expression evaluation, variable file, locals, and
variable validation implementations are derived from the corresponding
`internal/core/parser` files at the upstream commit. They have been reduced and
modified for an Alpine-only product contract.

Major differences from the referenced version:

- the module and executable are `github.com/mofelee/alpineform` and `apf`;
- configuration, variables, environment variables, install paths, state, and
  locks use AlpineForm-specific names;
- state has an explicit AlpineForm product marker and an independent schema;
- Debian-only APT, systemd, codename, locale, Docker, nftables, and source-build
  schemas are not present in the bootstrap;
- no Alpine resource is advertised as implemented by the bootstrap.

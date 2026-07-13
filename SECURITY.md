# Security Policy

Report suspected vulnerabilities privately through
<https://github.com/mofelee/alpineform/security/advisories/new>.

Do not include secrets, SSH private keys, tokens, private hostnames, or
sensitive configuration, plan, state, or debug output in public issues.

AlpineForm is pre-release software. Until a version is listed as supported in
this file, no release receives security fixes under a published SLA.

| Version | Security fixes |
| --- | --- |
| `v0.1.0-alpha.1` | Best effort while this prerelease is current; no SLA |
| older or untagged builds | Unsupported |

The [security model](docs/security-model.md) documents root SSH, state, lock,
secret-redaction, component-download, and release-supply-chain boundaries.

<p align="right"><strong>English</strong> | <a href="README.zh.md">简体中文</a></p>

# AlpineForm Documentation

This index is the entry point for AlpineForm user, operator, security, and maintainer
documentation. The [repository README](../README.md) contains the shortest installation
and first-apply path.

## Product And Configuration

- [Architecture](architecture.md) explains the parser, compiler, graph, engine, provider,
  backend, and state boundaries.
- [DSL and CLI reference](dsl-reference.md) defines commands, reusable models, component
  inputs, moves, dependencies, native domains, and output contracts.
- [Plan format](plan-format.md) documents text, JSON, component inputs, moves, and resource
  relationships.
- [Remote state backend](state-backend.md) documents host binding, authored dependencies,
  component moves, persistence, and recovery.
- [Target facts](facts.md), [root SSH transport](ssh.md), and the [runtime lease](locking.md)
  define the execution prerequisites.

## Managed Domains

- [APK repositories and packages](apk.md)
- [Files](files.md) and [directories](directories.md)
- [Groups](groups.md) and [users](users.md)
- [System settings](system.md) and [kernel settings](kernel.md)
- [OpenRC services](openrc.md)
- [Components, artifacts, and change scripts](components.md)
- [Docker Engine and Compose](docker.md)
- [nftables](nftables.md)

## Operations, Security, And Support

- [Operations runbook](operations-runbook.md)
- [Security model](security-model.md)
- [Target-side source-build security](source-build-security.md)
- [Support matrix](support-matrix.md)
- [Compatibility policy](compatibility-policy.md)

## Development And Releases

- [Development baseline](development.md)
- [Release process](release-process.md)
- [Release-notes template](release-notes-template.md)
- [Documentation localization policy](localization-policy.md)
- [Libvirt integration runbook](../test/integration/libvirt/README.md)

Historical release notes:

- [v0.1.0-alpha.1](releases/v0.1.0-alpha.1.md)
- [v0.1.0-alpha.2](releases/v0.1.0-alpha.2.md)
- [v0.1.0-alpha.3](releases/v0.1.0-alpha.3.md)
- [v0.1.0-alpha.4](releases/v0.1.0-alpha.4.md)
- [v0.1.0-alpha.5](releases/v0.1.0-alpha.5.md)

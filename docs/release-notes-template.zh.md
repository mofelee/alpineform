# Release Note 模板

<p align="right"><a href="release-notes-template.md">English</a> | <strong>简体中文</strong></p>

每个 AlpineForm release 都应保留下列全部 section。

```markdown
## 摘要

- <面向用户的用途。>

## 兼容性

- 发布阶段：<alpha | beta | stable>。
- CLI 平台：linux/amd64、linux/arm64、darwin/amd64、darwin/arm64。
- Beta 托管目标：Alpine 3.21-3.24 x86_64。
- Preview 托管目标：Alpine 3.21-3.24 aarch64。
- Beta 能力：binary 和 archive component。
- Beta 能力：由四分支 `components` case 门禁覆盖的 file 和 CA-certificate component。
- 增量 alpha 接口：每个实例的预构建 `source.url`/`source.sha256` 表达式。
- 增量 alpha 接口：`packages.package`、`files.file` 和运行时 `services.service` 之间
  静态、同 scope 的 `depends_on` 排序；它仍与 `triggered_by` 分离。
- Preview 能力：Alpine 3.21-3.24 x86_64 上具备回滚安全性的 named-table nftables。
- Preview 能力：Alpine 3.21-3.24 x86_64 上的目标端 component source build。
- Preview 能力：Alpine 3.21-3.24 x86_64 上的 component-root moved state migration。
- DSL/state/plan JSON：<兼容 | 破坏性 alpha 变更>；当前 state schema 为 v3，plan 格式为
  `alpineform.plan.alpha1`。

## 破坏性变更

- <无，或说明旧行为、新行为和受影响用户。>

## 迁移说明

- State v3：在第一次写入 state 的 apply 前，备份每台主机当前的 v1 或 v2 state，并保留
  与其匹配的配置和 binary。写入 v3 后如需降级，必须恢复该精确备份；不支持编辑 schema marker。
- <其他精确的升级与回滚步骤，或无。>

## 新增

- <能力。>

## 变更

- <非破坏性行为变更。>

## 修复

- <修复内容。>

## 安全

- <安全与依赖说明。>

## 已知问题

- <Alpha 限制和不支持的路径。>

## 验证

- Commit：`<full SHA>`。
- 本地 build/check/vulnerability/release snapshot：<结果>。
- Alpine 3.21-3.24 x86_64 12-case、48-job matrix 和 core gate：<run URL>。
- 针对 binary、file、archive 和 CA-certificate 行为的阻塞式 `components` case：<结果>。
- 现有 12-case matrix 内的四分支 `openrc` package -> managed configuration -> service
  dependency lifecycle：<结果>。
- Alpine 3.21-3.24 x86_64 nftables Preview gate：<run URL>。
- Alpine 3.21-3.24 x86_64 source-build Preview gate：<run URL>。
- Alpine 3.21-3.24 x86_64 component-moved Preview gate：<run URL>。
- Release dry-run：<run URL>。
- Release workflow：<run URL>。
- Asset、checksum、SBOM、Sigstore bundle 和 attestation：<结果>。
- 全新 installer 和 Alpine quickstart VM：<结果>。

## 验证矩阵

<由 release workflow 填写或替换。>
```

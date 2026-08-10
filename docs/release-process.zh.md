# 发布流程

<p align="right"><a href="release-process.md">English</a> | <strong>简体中文</strong></p>

首个完整公开契约是 `v0.1.0-alpha.5`。Alpha.1 至 alpha.4 作为不完整的 prerelease 保留，
以便审计。Release 必须从 core CI 和 release dry-run 均已通过的 commit 构建。

## 制品

GoReleaser 使用 `CGO_ENABLED=0` 和 `-trimpath` 构建：

| 平台 | Archive |
| --- | --- |
| Linux amd64 | `apf_<tag>_linux_amd64.tar.gz` |
| Linux arm64 | `apf_<tag>_linux_arm64.tar.gz` |
| macOS amd64 | `apf_<tag>_darwin_amd64.tar.gz` |
| macOS arm64 | `apf_<tag>_darwin_arm64.tar.gz` |

每个 release 都包含 `checksums.txt`、`checksums.txt.sigstore.json`，以及每个 archive 对应的
一个 `<archive>.sbom.spdx.json`。GitHub provenance attestation 覆盖 checksum 文件中列出的
archive。Archive 包含 `apf`、README、license、notice、changelog、docs 和 examples。
其中包括 security policy；四份根 Markdown 文档和完整的 `docs/` 文档树均同时提供英文与
简体中文版本，内嵌的 package manifest 会使缺失文档成为 installer error。

此 release 有意不提供 Homebrew。在 install、test 和 upgrade 获得阻塞式证据前，不得发布
Homebrew。

## 打 Tag 前门禁

1. 按照[兼容性策略](compatibility-policy.zh.md)对 DSL、CLI、地址、state、plan JSON、
   installer 和 artifact 变更进行分类。
2. 更新 `CHANGELOG.md`、`CHANGELOG.zh-CN.md` 和对应版本 release note 的两种语言。
3. 运行：

   ```sh
   make build
   make docs-check
   make check
   make vulncheck
   go mod verify
   goreleaser check
   goreleaser release --snapshot --clean --skip publish
   git diff --check
   ```

4. 运行完整的 Alpine 3.21-3.24 VM matrix，并验证精确清理。File 或 CA-certificate 组件的
   Beta 声明要求四个分支上的阻塞式 `components` case；扩展该 case 不得改变预期的
   12-case、48-job matrix 基数。Resource dependency 变更还要求四个分支上的现有 `openrc`
   case 证明正向排序、no-op、漂移修复、反向显式清理和默认 forget，且不得新增 case。
5. 确认 GitHub artifact attestation 可用。公开仓库可直接通过；私有 Enterprise Cloud 仓库
   必须在确认 entitlement 后，显式设置 repository variable
   `APF_PRIVATE_ATTESTATIONS_ENABLED=true`。
6. 推送 release commit，并要求其 exact-SHA core CI 和 release dry-run 通过。
7. 在隔离 prefix 中针对 snapshot artifact 测试 installer。
8. 创建使用 SSH 或 GPG 签名的 annotated tag，并且只推送该 tag。

对于逐实例预构建 artifact source 表达式，release review 必须指出增量 alpha
`source.url`/`source.sha256` 边界，并确认 literal 行为、资源地址、state schema v3、
`alpineform.plan.alpha1` 和 source-build 语义保持不变。Release note 还必须说明，解析后的
protected value 只存在于内存中，protected cache identity 基于保留的物理 component
identity 加规范化 source label，而不是 protected material。

对于 resource dependency，release review 必须区分 authored `depends_on`、inferred
ordering、structural 和 active `triggered_by`、OpenRC operation，以及 forget/destroy
行为。State schema v3 可读取 v1 和 v2，并且只持久化仍由已跟踪 resource 表示的 authored
dependency。Release note 必须要求在首次写入 v3 的 apply 前制作逐 host v1/v2 备份；
downgrade 时，必须用匹配的配置和 binary 恢复确切备份；不支持更改 schema marker。

## 发布与验证

Tag workflow 会重新运行 unit、race、vet、vulnerability 和 release 检查，发布四个 archive，
以 keyless 方式签署 checksum，创建 SBOM 和 attestation，然后测试 installer。其 Linux 验证
会将已发布 binary 安装到全新 prefix，并在全新的 Alpine 3.21、3.22、3.23 和 3.24 VM 上
运行已推广的 quickstart。

Workflow 成功后：

1. 验证所有预期 asset name 及其非零大小。
2. 验证 archive checksum、Sigstore bundle 和 GitHub attestation。
3. 确认 `apf version` 报告 tag、release commit、build time、Go version 和所选 platform。
4. 确认每个 archive 和 installed-data tree 都包含完整的双语文档 package manifest。
5. 确认 release note 包含最终 verification matrix 和已知 alpha 限制。
6. 只有在 fresh-install 和 VM 证据存在后，才关闭 release tracker。

切勿替换现有 tag 下的 asset。如果发布或验证失败，请修正 workflow 或代码，并发布新的
prerelease tag；应记录任何不良 release，而不是静默修改它。

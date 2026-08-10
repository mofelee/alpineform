# 安全策略

<p align="right"><a href="SECURITY.md">English</a> | <strong>简体中文</strong></p>

请通过 <https://github.com/mofelee/alpineform/security/advisories/new> 私下报告疑似漏洞。

请勿在公开 issue 中包含 secret、SSH 私钥、token、私有主机名，或敏感的配置、plan、state
及 debug 输出。

AlpineForm 是预发布软件。在某个版本于本文件中列为受支持版本之前，任何 release 都不会
依据已发布的 SLA 获得安全修复。

| 版本 | 安全修复 |
| --- | --- |
| `v0.1.0-alpha.5` | 该预发布版本处于当前版本期间尽力提供；无 SLA |
| `v0.1.0-alpha.4` | Unsupported；release 不完整，请勿使用 |
| `v0.1.0-alpha.3` | Unsupported；release 不完整，请勿使用 |
| `v0.1.0-alpha.2` | Unsupported；release 不完整，请勿使用 |
| `v0.1.0-alpha.1` | Unsupported；release 不完整，请勿使用 |
| 更早版本或未打 tag 的构建 | Unsupported |

[安全模型](docs/security-model.zh.md)记录了 root SSH、state、lock、secret 脱敏、组件下载和
release 供应链的边界。

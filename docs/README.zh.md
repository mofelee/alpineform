<p align="right"><a href="README.md">English</a> | <strong>简体中文</strong></p>

# AlpineForm 文档

本索引是 AlpineForm 用户、运维、安全和维护者文档的入口。[仓库 README](../README.zh-CN.md)
提供最短的安装和首次应用路径。

## 产品与配置

- [架构](architecture.zh.md)解释解析器、编译器、图、引擎、提供程序、后端和状态之间的边界。
- [DSL 与 CLI 参考](dsl-reference.zh.md)定义命令、可复用模型、组件输入、移动、依赖关系、
  原生领域和输出契约。
- [计划格式](plan-format.zh.md)记录文本、JSON、组件输入、移动和资源关系。
- [远程状态后端](state-backend.zh.md)记录主机绑定、显式依赖关系、组件移动、持久化和恢复。
- [目标事实](facts.zh.md)、[root SSH 传输](ssh.zh.md)和[运行时租约](locking.zh.md)定义执行前提。

## 托管领域

- [APK 仓库和软件包](apk.zh.md)
- [文件](files.zh.md)和[目录](directories.zh.md)
- [组](groups.zh.md)和[用户](users.zh.md)
- [系统设置](system.zh.md)和[内核设置](kernel.zh.md)
- [OpenRC 服务](openrc.zh.md)
- [组件、制品和变更脚本](components.zh.md)
- [Docker Engine 与 Compose](docker.zh.md)
- [nftables](nftables.zh.md)

## 运维、安全与支持

- [运维手册](operations-runbook.zh.md)
- [安全模型](security-model.zh.md)
- [目标端源码构建安全](source-build-security.zh.md)
- [支持矩阵](support-matrix.zh.md)
- [兼容性政策](compatibility-policy.zh.md)

## 开发与发布

- [开发基线](development.zh.md)
- [发布流程](release-process.zh.md)
- [发布说明模板](release-notes-template.zh.md)
- [文档本地化政策](localization-policy.zh.md)
- [libvirt 集成手册](../test/integration/libvirt/README.zh.md)

历史发布说明：

- [v0.1.0-alpha.1](releases/v0.1.0-alpha.1.zh.md)
- [v0.1.0-alpha.2](releases/v0.1.0-alpha.2.zh.md)
- [v0.1.0-alpha.3](releases/v0.1.0-alpha.3.zh.md)
- [v0.1.0-alpha.4](releases/v0.1.0-alpha.4.zh.md)
- [v0.1.0-alpha.5](releases/v0.1.0-alpha.5.zh.md)

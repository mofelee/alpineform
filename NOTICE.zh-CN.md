# 来源说明

<p align="right"><a href="NOTICE.md">English</a> | <strong>简体中文</strong></p>

AlpineForm 使用 DebianForm v0.6.0 作为架构和部分代码的参考来源：

- 上游仓库：<https://github.com/mofelee/debianform>
- 上游提交：`843c5e8251f36cdae426d3ba58c209e71d1da867`
- 上游许可证：MIT，copyright 2026 mofelee

AlpineForm 的初始引导实现复用了高层分层方式、配置源排序、版本元数据模式和状态验证方法。
类型化值、输入类型、表达式求值、变量文件、局部值和变量验证的实现派生自上述上游提交中
对应的 `internal/core/parser` 文件。这些实现已经过精简和修改，以符合仅面向 Alpine 的产品
契约。

与所参考版本相比，主要差异如下：

- 模块和可执行文件分别为 `github.com/mofelee/alpineform` 和 `apf`；
- 配置、变量、环境变量、安装路径、状态和锁均使用 AlpineForm 专用名称；
- 状态包含明确的 AlpineForm 产品标记和独立的 schema；
- 初始引导实现中不包含 Debian 专用的 APT、systemd、codename、locale、Docker、nftables
  和 source-build schema；
- Alpine 和 BusyBox provider、OpenRC 行为、组件制品、发布自动化以及真实 VM 支持门禁，
  均在初始引导之后为 AlpineForm 独立实现。

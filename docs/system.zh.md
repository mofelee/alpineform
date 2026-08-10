<p align="right"><a href="system.md">English</a> | <strong>简体中文</strong></p>

# Alpine 系统设置

主机标签用于标识 AlpineForm 主机，不会隐式管理目标主机名。请显式声明系统设置：

```hcl
host "edge" {
  system {
    hostname = "edge.example"
    timezone = "Asia/Shanghai"
  }
}
```

`hostname` 同时管理运行时主机名和 root 所有的 `/etc/hostname` 文件。值必须是
RFC 1123 主机名。AlpineForm 不会重写 `/etc/hosts`。

`timezone` 必须是相对的 zoneinfo 名称，不得包含空路径段、当前目录路径段或父目录
路径段。AlpineForm 安装并跟踪一个显式的 `tzdata` APK world intent，验证所选时区
解析到 `/usr/share/zoneinfo` 内部，以原子方式将 `/etc/localtime` 管理为符号链接，
并写入 `/etc/timezone`。

如果已声明 `tzdata` 为 present，timezone 资源会复用它。显式声明为 absent 的
`tzdata` 与 timezone 管理冲突，会在验证期间被拒绝。移除 hostname 或 timezone
会停止管理并忘记相应状态；它不会重置远端系统或移除合成的 package。移除 timezone
管理后，仍必须通过显式的 `packages.package "tzdata" { ensure = "absent" }` 声明
才能移除 package。

Alpine 使用 musl，不提供 glibc locale 模型。`system.locale` 会在解析阶段被拒绝，
而不是暴露一个无法工作的设置。

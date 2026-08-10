<p align="right"><a href="kernel.md">English</a> | <strong>简体中文</strong></p>

# Alpine 内核设置

内核资源是主机级、以 present 为目标的声明：

```hcl
host "router" {
  kernel {
    module "br_netfilter" {}

    sysctl "net.bridge.bridge-nf-call-iptables" {
      value = "1"
    }

    sysctl "net.ipv4.ip_forward" {
      value         = "1"
      apply_runtime = false
    }
  }
}
```

`module` 使用 `modprobe` 加载指定模块，并将其原子持久化到
`/etc/modules-load.d/alpineform-<name>.conf`。检查会区分已加载、内建、可用和缺失
模块。内建模块满足运行时状态，但仍会获得持久化文件。自动 absence 和卸载不属于
v0.1：`ensure = "absent"` 会被拒绝；移除声明只会忘记状态，不会调用 `modprobe -r`
或删除持久化配置。

每个 `sysctl` 拥有一个抗冲突的 `/etc/sysctl.d/99-alpineform-*.conf` 文件。`value`
为必填项，`apply_runtime` 默认为 `true`。每个 sysctl 都依赖主机声明的模块。当一个
或多个启用运行时应用的设置发生变化时，AlpineForm 会先写入所有持久化文件，再通过
一条聚合命令应用运行时值。no-op 不会运行任何运行时命令。

移除 sysctl 声明只会删除其 AlpineForm 所有的持久化文件，不会重置当前内核值。
如果旧值不能继续生效，请在移除前显式设置替代运行时值。外部 sysctl 文件绝不会被
扫描、重写或视为 AlpineForm 所有。

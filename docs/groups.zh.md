<p align="right"><a href="groups.md">English</a> | <strong>简体中文</strong></p>

# 托管组

主机级 `groups.group` 资源通过 root SSH 管理 Alpine 组：

```hcl
host "node" {
  groups {
    group "app" {
      gid    = 1500
      system = true
    }
  }
}
```

名称使用 Alpine 账户语法。`gid` 可选，接受 0 至 2147483647 的整数。创建组时，
`system = true` 会调用 BusyBox `addgroup -S`；若显式提供 GID，它不会强制
采用某个 GID 范围。AlpineForm 观察 `/etc/group`，且仅使用 BusyBox `addgroup`
和 `delgroup` 创建及删除组。名称和 ID 均作为位置参数传递，绝不会插值到 provider
脚本中。

显式 GID 发生漂移时，AlpineForm 会原子替换 `/etc/group`，同时保留其所有者、
所属组、模式、成员字段以及所有无关记录。它拒绝使用已由其他组占用的 GID，也拒绝
更改被用作主组的组。省略 GID 时，分配的 ID 不受管理。更改 GID 不会迁移非托管
文件系统条目的所有权；已声明的文件和目录随后会按依赖顺序修复。

由已声明为 present 的组拥有的文件和目录依赖该组。编译器拒绝由已声明为 absent 的
组拥有的 present 路径。已声明为 absent 的路径会先于拥有它们且同样为 absent 的组
移除。

## 删除

- `ensure = "absent"` 显式删除组。
- 移除声明时默认为 `on_remove = "forget"`：移除状态中的所有权记录，远端组保持
  不变。
- `on_remove = "destroy"` 记录组名，并在以后移除声明时删除该组。
- `lifecycle { prevent_destroy = true }` 会在 provider 执行前阻止显式删除和已记录的
  destroy 行为。

删除会拒绝 GID 为 0 的组、被用作主组的组，以及仍有补充成员的组。删除组之前，
必须显式移除这些依赖。

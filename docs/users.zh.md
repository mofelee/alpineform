<p align="right"><a href="users.md">English</a> | <strong>简体中文</strong></p>

# 托管用户

主机级 `users.user` 资源通过 root SSH 管理 Alpine 账户：

```hcl
host "node" {
  groups {
    group "app" { gid = 1500 }
  }
  users {
    user "app" {
      uid    = 1500
      group  = "app"
      home   = "/srv/app"
      shell  = "/sbin/nologin"
      system = true
    }
  }
}
```

名称使用 Alpine 账户语法；有意不支持管理 `root`。`uid` 接受 1 至 2147483647 的
整数。主 `group` 接受 Alpine 组名或数字 ID。`home` 必须是整洁的绝对非根路径，
`shell` 必须是整洁的绝对路径。AlpineForm 使用 BusyBox `adduser -D` 和 `deluser`，
所有值都作为位置参数传递。

UID、group、home 和 shell 均可选。省略的字段会让目标的分配值或默认值不受管理。
仅在创建账户时，`system = true` 才会使用 `adduser -S`。如果显式 home 不存在，
则会创建；它不得是符号链接，并会获得账户所有权。账户的 home 字段变化时，不会移动
已有 home 内容。

## 补充组

`groups` 将附加式补充组成员关系作为独立资源管理：

```hcl
user "app" {
  group  = "app"
  groups = ["wheel", "metrics"]
}
```

条目必须是组名，按声明顺序去重，并且不得解析为主组。先创建托管组，再创建用户成员
关系。移除一个条目会移除 AlpineForm 先前记录的成员关系；AlpineForm 从未管理的
无关远端成员关系保持不变。BusyBox `addgroup USER GROUP` 和
`delgroup USER GROUP` 以位置参数接收名称。

## 授权密钥

`ssh_authorized_keys` 接受 OpenSSH 公钥行：

```hcl
user "operator" {
  ssh_authorized_keys = [
    "ssh-ed25519 <base64-public-key> operator@example",
  ]
}
```

AlpineForm 在规划前解析密钥材料。v0.1 拒绝 `authorized_keys` 选项。身份使用 OpenSSH
SHA-256 指纹，因此注释不同但材料相同的重复密钥会成为一个资源，修改注释也不会重写
已有行。从列表移除条目会移除匹配的密钥材料，同时保留无关行。

provider 会创建 `.ssh` 并原子替换 `authorized_keys`，强制采用 `0700`/`0600`，
修复用户和主组所有权，并拒绝 home、`.ssh` 或文件路径上的符号链接。密钥行、类型、
blob、用户和路径作为位置参数传递，而不是作为 provider 脚本文本。

显式身份漂移通过原子替换 `/etc/passwd` 修复，同时保留其所有者、所属组、模式和无关
记录。UID 冲突会被拒绝。UID 变化不会迁移非托管文件系统条目的所有权；已声明的文件
和目录随后会按依赖顺序修复。

具有已声明主组的用户依赖该组。由已声明用户拥有的文件和目录依赖该用户。编译器拒绝
引用已声明为 absent 的账户的 present 用户或路径。显式 absence 按路径、用户、主组
的顺序执行。

## 删除

- `ensure = "absent"` 显式删除账户。
- 移除声明时默认为 `on_remove = "forget"`：移除状态中的所有权记录，远端账户保持
  不变。
- `on_remove = "destroy"` 记录用户名，并在以后移除声明时删除该用户。
- `lifecycle { prevent_destroy = true }` 会在 provider 执行前阻止显式 absence 和
  已记录的 destroy 行为。

删除会拒绝 UID 0 和仍具有补充组成员关系的用户。它不会移除用户 home 或其他文件
系统数据。删除账户前必须显式移除成员关系。使用 `ensure = "absent"` 时，第一次
apply 应保留托管 `groups` 和 `ssh_authorized_keys` 列表，以便其生成的 absent 资源先于
用户运行；账户 absent 后即可移除这些声明。使用 `on_remove = "destroy"` 移除整个
用户声明时，会采用已记录的反向依赖顺序。

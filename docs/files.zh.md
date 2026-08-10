<p align="right"><a href="files.md">English</a> | <strong>简体中文</strong></p>

# 托管文件

主机级 `files.file` 资源通过 root SSH 管理普通文件：

```hcl
host "node" {
  files {
    file "/etc/example/app.conf" {
      content = "enabled=true\n"
      owner   = "root"
      group   = "root"
      mode    = "0640"
    }
  }
}
```

当 `ensure = "present"` 时，必须且只能提供 `content` 或 `source` 之一。相对 source
路径相对于声明它的配置文件解析。写入时会在目标目录中创建临时文件，设置所有权和
模式，再通过原子重命名覆盖目标。文件资源绝不会替换或删除目录。

`owner` 和 `group` 默认为 `root`；`mode` 默认为 `0644`。账户名和数字 ID 均作为
位置参数传递，绝不会插值到 provider 脚本中。内容仅通过已脱敏的 SSH stdin 传输。

## 顺序与变更触发器

`files.file` 声明可以在其自身已解析的主机或已挂载 component 作用域中，指定静态的
`packages.package`、`files.file` 或运行时 `services.service` 前置资源：

```hcl
packages {
  package "worker-daemon" {}
}

files {
  file "/etc/worker/worker.conf" {
    content    = "enabled=true\n"
    depends_on = [package["worker-daemon"]]
  }
}
```

在正向 apply 中，`depends_on` 会把 package 排在 file 之前。它不是变更触发器：
package 更新不会运行文件的 `on_change` 脚本或 OpenRC 操作。它们使用独立的
`triggered_by` 关系，并且仅在匹配的托管输入确实发生变化时运行。如果一次显式远端
清理同时移除两个资源，则先移除作为依赖方的文件，再移除 package；普通声明移除仍
默认为 forget，并会保留远端文件和 package。完整规则见
[资源依赖](dsl-reference.zh.md#资源依赖)。

## 受保护和只写内容

对于必须脱敏的内容，设置 `sensitive = true`。敏感性也会从敏感变量传播。plan、
graph、state、debug 和错误会省略内容，只暴露受保护的元数据；对于非临时内容，
还会暴露 SHA-256/字节数摘要。

临时内容是只写的，并且要求一个公开的 `content_version`：

```hcl
file "/etc/example/token" {
  content         = var.session_token
  content_version = "rotation-2026-07"
  sensitive       = true
  mode            = "0600"
}
```

由内容派生的摘要不会持久化。AlpineForm 使用公开版本和文件元数据来保证可重复性，
因此无法检测只写文件发生的带外内容变化。更改 `content_version` 会强制重写。

## 删除

- `ensure = "absent"` 显式删除普通文件，且不得包含 `content` 或 `source`。
- 移除声明时默认为 `on_remove = "forget"`：移除状态中的所有权记录，远端文件保持
  不变。
- `on_remove = "destroy"` 在状态中记录删除标识，并在以后移除声明时删除文件。
- `lifecycle { prevent_destroy = true }` 阻止显式删除和已记录的 destroy 行为。若某个
  声明移除后应当销毁资源，请先在禁用该保护的情况下 apply 一次。

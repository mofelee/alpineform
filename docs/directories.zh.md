<p align="right"><a href="directories.md">English</a> | <strong>简体中文</strong></p>

# 托管目录

主机级 `directories.directory` 资源通过 root SSH 管理目录：

```hcl
host "node" {
  directories {
    directory "/srv/example/data" {
      owner = "app"
      group = "app"
      mode  = "0750"
    }
  }
}
```

路径必须是整洁的绝对路径，且不能是根目录。`owner` 和 `group` 默认为
`root`；`mode` 默认为 `0755`。AlpineForm 会创建缺失的父目录、修复所有权和
模式漂移，并拒绝替换非目录路径。账户名、数字 ID 和路径均作为位置参数传递，
绝不会插值到 provider 脚本中。符号链接会报告为冲突路径，绝不会被跟随或删除。

当托管目录声明相互嵌套时，AlpineForm 会先创建距离最近的已声明父目录，再创建
其子目录。托管文件同样依赖其距离最近的已声明父目录。当后代文件和目录也显式
声明为不存在时，显式删除会按叶节点优先的顺序执行。

## 删除

目录删除默认不递归：

```hcl
directory "/srv/example/data" {
  ensure = "absent"
}
```

此操作会删除空目录；若目录中存在条目，则会失败且不更改状态。仅当允许移除整棵
目录树时，才设置 `recursive_delete = true`。同一策略也会记录下来，供日后移除
声明时使用。

- `ensure = "absent"` 使用声明的递归策略显式删除目录。
- 移除声明时默认为 `on_remove = "forget"`：移除状态中的所有权记录，远端目录
  保持不变。
- `on_remove = "destroy"` 会在以后移除声明时删除目录，并使用上次 apply 时的
  `recursive_delete` 值。
- `lifecycle { prevent_destroy = true }` 会在 provider 执行前阻止显式删除和已记录的
  destroy 行为。

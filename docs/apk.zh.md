<p align="right"><a href="apk.md">English</a> | <strong>简体中文</strong></p>

# APK 仓库与密钥

主机级 `apk` 块管理 Alpine 仓库条目和自定义签名密钥，但不执行发行版升级：

```hcl
host "edge" {
  platform { version = "3.24.1" }

  apk {
    repository "main" {
      url = "https://dl-cdn.alpinelinux.org/alpine"
    }
    repository "community" {
      url = "https://dl-cdn.alpinelinux.org/alpine"
    }
  }
}
```

URL 是一个 HTTPS 仓库根地址。AlpineForm 会附加检测到或离线声明的 branch 以及
`component`；后者默认取仓库标签。可选的 `tag` 会生成 APK 的 `@tag` 仓库语法。
包含凭据、查询字符串、片段、编码路径或非 HTTPS scheme 的 URL 会被拒绝。branch
必须与检测到或声明的目标 branch 一致，并且必须是 Alpine 3.21 至 3.24 之一。

## 仓库所有权

`ownership = "managed"` 是默认值。每个声明只拥有 `/etc/apk/repositories` 中由固定
AlpineForm 标记注释标识的一个块。外部仓库行、注释、空行及其相对顺序都会保留。
移除声明会忘记其状态并保留已标记块；要显式移除该块，请使用
`ensure = "absent"`。

`ownership = "authoritative"` 是显式选择加入的模式，会用声明为 present 的条目替换
整个 repositories 文件。在线 plan 会显示完整的 observed 和 desired 文件。在此模式
下，从声明集合中移除仓库会有意将其从 AlpineForm 拥有的文件中删除。

仓库文件以 root 所有权和 `0644` 模式进行原子替换。provider 会拒绝符号链接、
非普通目标、重复标记和格式错误的 managed 块。

## 自定义密钥

自定义公钥使用 `/etc/apk/keys` 下固定且安全的文件名、相对 module source，以及必需的
SHA-256 摘要：

```hcl
apk {
  key "vendor-2026.rsa.pub" {
    source = "keys/vendor-2026.rsa.pub"
    sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  }
}
```

AlpineForm 在编译期间验证 source 摘要，并在远端原子替换前再次验证。source 字节仅
通过 SSH stdin 传输。移除 key 声明时默认为 forget；只有
`ensure = "absent"` 会删除固定的远端文件名。符号链接和非普通 key 目标会被拒绝。

## 索引刷新与安全性

graph 会将自定义 key 排在 repository 之前，将 repository 排在一次合成的 APK
索引刷新之前。这些依赖中的任何 create、adopt、update、漂移修复或显式删除，都会为
主机触发且仅触发一次静默 `apk update`。干净 plan 不会触发刷新，仅移除声明的 forget
也不会触发刷新。package 节点在刷新之后运行。主机 lease 和 graph scheduler 会串行化
所有这些 APK 变更。

该 key -> repository -> refresh -> package 顺序属于推断顺序。它会出现在 plan 的
`depends_on` 中，但不是作者声明的资源依赖元数据，也不会为孤儿拆除而持久化。

此功能面绝不会调用 `apk upgrade`、`apk fix`，不会更改目标 branch，也不接受 package
版本约束。

## Package world 意图

主机级 `packages` 声明管理不带版本的 package 名称：

```hcl
packages {
  package "curl" {}

  package "vendor-agent" {
    repository = "vendor"
  }
}
```

`ensure = "present"` 使用静默、非交互式 `apk add` 安装 package，并要求 package 已
安装且 `/etc/apk/world` 中存在完全匹配的条目。已安装 package 元数据会被观察，但不会
固定版本。可选的 `repository` 值是 APK tag，必须匹配一个声明为 present 的 repository
的 `tag`；由此产生的 world 意图为 `name@tag`。

`packages.package` 声明还可以使用静态、同一作用域的 `depends_on`，引用其他
`packages.package`、`files.file` 或运行时 `services.service` 声明。这些作者声明的边只
增加顺序；绝不会导致 APK 操作或其他资源的变更操作运行。当显式清理在同一 plan 中
移除 package 和依赖它的远端对象时，会先移除依赖方。完整规则见
[资源依赖契约](dsl-reference.zh.md#资源依赖)。

移除 package 声明只会忘记其 AlpineForm 状态。它绝不会运行 `apk del`，并会保留
package 和 world 条目。执行静默 `apk del` 的唯一路径，是当前声明包含
`ensure = "absent"`；即使直接调用，provider 也会拒绝 orphan-destroy 调用。APK 只移除
该命名 world 意图，AlpineForm 绝不会要求它直接编辑 world 文件，因此不相关的外部或
已声明 world 条目仍由各自工具拥有。

Package 名称和 tag 会拒绝空白、shell 语法、版本操作符及约束。Package 名称、world
意图、repository 行、key 路径和摘要始终作为 provider argv，而不是生成的 shell source。

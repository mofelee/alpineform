<p align="right"><a href="dsl-reference.md">English</a> | <strong>简体中文</strong></p>

# DSL 与 CLI 参考

本页是 v0.1 索引。各 domain 页面定义详细属性、默认值、验证、观察和删除行为。

## 命令

| 命令 | 用途 |
| --- | --- |
| `apf validate` | 解析、类型检查、解析引用并验证配置。 |
| `apf plan [--offline]` | 渲染文本或 JSON 变更；可选择写入 HTML。 |
| `apf apply` | 审阅、加锁、重新规划、审批、收敛并持久化 state。 |
| `apf check` | 当观察到的在线 plan 不是 no-op 时以非零状态退出。 |
| `apf fmt` | 检查所选文件的 HCL 语法，然后格式化它们。 |
| `apf component inspect` | 输出已解析的 component 信息。 |
| `apf variable inspect` | 输出稳定 JSON，并对受保护的默认值脱敏。 |
| `apf version` | 打印版本、commit、构建时间、Go 版本和平台。 |

配置输入使用可重复的 `-f`；变量输入使用 `-var-file` 和 `-var`。在线命令接受有界
并行度。`apply` 还接受 `--auto-approve`、`--allow-network-disruption`、`--debug`
以及 lock timeout。network 选项是实时 nftables activation/deletion 的独立必需授权，
绝不会由 `--auto-approve` 隐式授予。请使用命令帮助确认已安装 binary 提供的准确
flag 拼写。

`apf fmt` 会在写入格式化内容之前读取并检查每个所选配置文件的语法。它不会加载变量
输入，也不会求值 AlpineForm 模型。请使用 `apf validate` 来解析、解析引用、执行类型
检查并对配置进行语义验证。

## 可复用模型

- `variable` 支持类型化、经过验证、敏感和临时输入。
- `locals` 包含在输入优先级解析后求值的 HCL 表达式。
- `assert` 会用声明的消息拒绝 false condition。
- `profile` 将主机配置分组，以便确定性 import。
- `component` 定义类型化的可复用原生资源、一个预构建 artifact，或独立处于 Preview
  的 checksummed source build。
- `moved` 在经过审阅的重命名中保留 component 实例的 state 身份。
- `script` 定义 argv-safe command 或已脱敏的 interpreter content，以及可选的 observed
  output。
- `host` 选择 SSH、可选的离线 platform fact、import、component 和原生 resource domain。

`platform.architecture` 和 `platform.version` 是可选的离线断言。在线 branch、libc、
原生 APK architecture 和 kernel architecture 是只读的 detected fact。

## Source-build 工作区根目录

profile 和 host 可以设置目标端 source-build 工作区的默认根目录；一个已挂载的 source
component 可以覆盖它：

```hcl
profile "source_build_defaults" {
  staging {
    root = "/srv/alpineform-builds"
  }
}

host "builder" {
  imports = [profile.source_build_defaults]

  staging {
    root = "/mnt/alpineform-host-builds"
  }

  component "tool" {
    source       = component.tool
    staging_root = "/mnt/alpineform-tool-builds"
  }
}
```

优先级依次为 component 实例的 `staging_root`、经过普通 profile import 和 host override
组合后的有效 host `staging.root`，最后是 `/var/tmp/alpineform/builds`。后出现的
component 实例声明会整体替换之前的实例：若替换声明省略 `staging_root`，则会回退到
有效 host 默认值，而不是继承被替换的值。即使当前没有任何已挂载 source component
使用它，也可以声明 profile 或 host 默认值。非 source component 上的 `staging_root`
会被拒绝。

每个 root 都必须解析为非敏感、非临时字符串。它必须是整洁的绝对 POSIX 路径且不能
为 `/`，不得包含控制字符；允许空格。diagnostic 会保留声明源位置。构建使用该路径前，
会执行目标端所有权、符号链接和模式检查；详见
[source-build 安全](source-build-security.zh.md#工作区放置与所有权)。

所选 root 是执行位置，而不是构建内容身份。它不会进入序列化的 IR、graph、plan、
state、HTML 或常规 debug event，也不会改变资源地址、rebuild identity、installation
identity 或 `on_change` 行为。有界的 workspace-failure diagnostic 可以标识所选 root
和派生 work path。只更改 root 时，只要已验证 output cache 仍有效，就保持 no-op；
下一次由其他原因要求的 rebuild 会使用新选择的 root。

## 预构建 artifact source 表达式

对于 `binary`、`file`、`archive` 和 `ca_certificate` component，只有预构建 `source`
块中的 `source.url` 和 `source.sha256` 可以使用已挂载 component 的 `input.*` context。
这不会限制 component 中其他位置已有的 input 用法。AlpineForm 会规范化并验证一个 mount
的类型化 input，求值该 template 的所有 source URL 和 checksum 表达式，然后在 graph
编译前选择无标签或带 architecture 标签的 source。离线选择使用声明的
`platform.architecture`；在线选择使用 observed fact。

未挂载但依赖 input 的 component 仍会验证其静态 source shape，而不会虚构 required
input 的值。resolved URL 和 checksum 验证会推迟到 mount 提供规范化值时。diagnostic
会同时标识 template field 和 mounted instance。

这是一个 additive alpha 边界。`type`、`version`、source label、`extract`、`build`、
`install`、resource address、state schema v3 和 `alpineform.plan.alpha1` 均不改变。
已有 literal source 保留当前行为和身份。目标端 source build 保留其独立的 Preview input
模型。完整的 cache 和 protected-value 契约见
[component](components.zh.md#每个实例的-source-表达式)。

## Component 地址迁移

顶层、无标签的 `moved` 块声明一个已挂载 component 实例已重命名：

```hcl
component "worker_template" {}

moved {
  from = component.legacy_worker
  to   = component.worker
}

host "edge" {
  component "worker" {
    source = component.worker_template
  }
}
```

两个 endpoint 都必须是静态 `component.<instance>` traversal。string、variable、local、
interpolation、function call、host-qualified address、resource leaf、label、嵌套 block，
以及除 `from` 和 `to` 以外的 attribute 都会被拒绝。两个 attribute 都是必需的。验证还会
拒绝 self-move、重复或分叉 source、many-to-one destination、cycle、重叠 mapping，以及
包含多个 mounted instance 的歧义 chain。支持 `old -> middle -> current` 这样的无环
历史 chain。

该声明会独立投影到每个 host。挂载 destination 的 host 可以迁移 source root 下的每个
tracked address，同时保留其 suffix：

```text
host.edge.component.legacy_worker.*
  -> host.edge.component.worker.*
```

仍只挂载 source 的 host 保持不变，从而支持分阶段 rollout。对于适用的 host，tracked
source state 要求该 host 的 desired graph 中存在最终 destination；tracked source 和
destination root 不能合并。move 绝不会跨 host、state backend 或 product，也不会重命名
远端 file、package、account、service、interface 或其他 provider object。

在线 `plan` 和 `check` 在内存中解析 move，并保持只读。`apply` 在持有 host lease 时
重新计算 move，将其纳入 locked-plan review，并在 provider mutation 前原子持久化。
单独的 move 不是 create、update、adopt、delete、destroy、forget 或 change-script trigger。

请把该 block 视为临时 migration instruction，而不是 alias。在每个相关 host 都完成 apply，
并且保留该 block 时在线 plan/check 干净之前，始终把它保留在配置中。如果只有部分 host
完成迁移后就移除它，剩余 source state 将失去 migration instruction。在每个 source prefix
都 absent 且 destination prefix present 后，移除该 block 并再次验证在线 plan/check；
已完成迁移后的移除是 no-op。参见 [component move 示例](../examples/component-moved.apf.hcl)
和[运维运行手册](operations-runbook.zh.md#重命名-component-实例)。

## 资源依赖

`packages.package`、`files.file` 和运行时 `services.service` 声明接受 additive alpha
`depends_on` attribute。其值是静态类型化引用列表：

```hcl
host "edge" {
  packages {
    package "bird" {}
  }

  files {
    file "/etc/conf.d/bird" {
      content    = "BIRD_ARGS=\"-f\"\n"
      depends_on = [package.bird]
    }
  }

  services {
    service "bird" {
      package    = "bird"
      operation  = "restarted"
      depends_on = [file["/etc/conf.d/bird"]]
    }
  }
}
```

只接受 `package.<label>`、`file.<label>` 和 `service.<label>`，它们分别以这三个声明
family 为目标。生成的 `openrc.service` block 既不接受资源 `depends_on`，本身也不会成为
类型化的 `service.<label>` 目标。当 label 不适合 traversal 语法时，必须使用
`package["bird-tools"]` 这样的 bracket notation。string、raw expanded graph address、
variable、interpolation、computed index、sensitive 或 ephemeral expression、
host-qualified address 以及其他资源类型都会被拒绝。引用会在同一 effective host scope
内的 profile import 和 override 之后解析。在 component template 内，它们只解析到该
mounted component instance 中的资源，绝不会解析到 host 资源或 sibling component。
unknown、duplicate、self-referential 和 cyclic relationship 会失败并提供 source diagnostic。

作者声明的依赖只增加顺序。正向 apply 把 dependency 排在 dependent 前；当两者都从
远端 host 显式移除时，会先移除 dependent。dependency 发生变化绝不会添加
`triggered_by`，因此它本身不能 restart/reload OpenRC service，也不能运行 `on_change`
script。推断出的 package、account、init/conf、APK-refresh 及其他前置资源保持独立。在
示例中，只有匹配的托管 `/etc/conf.d/bird` 文件确实发生变化，才会激活 service operation。

作者声明的 relationship 会保留在 state 中，以便安全拆除 orphan，并会在 no-op apply
期间协调。dependency 绝不会选择资源 action 或 removal policy：
`ensure = "absent"`、受支持的 `on_remove = "destroy"` 和
`lifecycle.prevent_destroy` 保留各自记录的语义。默认移除声明只会 forget state，不执行
远端删除，因此不会把 relationship 转变为 teardown 工作。当前 graph resource 会在 plan
中显示完整的 effective ordering set；参见[plan 关系](plan-format.zh.md#关系)和
[state 依赖元数据](state-backend.zh.md#作者声明的资源依赖)。

## 原生 domain

- `files.file`：[文件](files.zh.md)
- `directories.directory`：[目录](directories.zh.md)
- `groups.group`：[组](groups.zh.md)
- `users.user`、membership 和 key：[用户](users.zh.md)
- `apk.repository`、`apk.key` 和 `packages.package`：[APK](apk.zh.md)
- 受限的 `openrc.service` 和运行时 `services.service`：[OpenRC](openrc.zh.md)
- `system.hostname` 和 `system.timezone`：[系统](system.zh.md)
- `kernel.module` 和 `kernel.sysctl`：[内核](kernel.zh.md)
- 预构建 artifact 和 `on_change`：[component](components.zh.md)
- Preview checksummed 目标端 build：[component](components.zh.md#preview-source-build)
- Preview Docker Engine 和 Compose project：[Docker](docker.zh.md)
- Preview rollback-safe 命名 table：[nftables](nftables.zh.md)

托管资源在有文档说明时支持显式 presence 或 absence。移除声明时默认为仅 forget state。
支持 `on_remove = "destroy"` 的资源会记录 provider-safe 删除身份，以便以后移除。
`lifecycle.prevent_destroy` 会在 provider 执行前阻止显式删除和已记录的 destroy。

## 输出契约

离线 plan 包含结构和 managed graph 节点。在线 plan 包含 `create`、`update`、`adopt`、
`delete`、`destroy`、`forget` 和 `no-op` action。machine format 记录于
[plan format](plan-format.zh.md)。受保护的值绝不会出现在 graph、plan、state、HTML、
debug 或 error 中。

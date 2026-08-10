<p align="right"><a href="components.md">English</a> | <strong>简体中文</strong></p>

# Component、artifact 与变更脚本

component 将类型化 input 与 AlpineForm 已有的 file、directory、group、user、package、
OpenRC 生成及 service 资源结合起来。每个 mounted instance 都保留自己的 graph prefix，
例如 `host.edge.component.worker.files.file["/etc/worker.conf"]`。

## 重命名 mounted instance

仅当 mounted component 的 instance label 发生变化时，使用顶层 `moved` block。
AlpineForm 会把旧 component root 下的每个 tracked address 迁移到新 root，而不重命名或
重新创建远端 object：

```hcl
moved {
  from = component.legacy_worker
  to   = component.worker
}
```

move 会保留 resource ownership、lifecycle 和 deletion policy、observed provider result
以及 protected marker。relationship 和由地址派生的 desired metadata 会根据 destination
graph 重新协调。如果 desired content 也发生变化，则该真实 update 及任何有效 trigger
会在 plan 中与 move 保持独立。

source build 还有额外的地址派生所有权。state schema v2 引入了保留的旧 physical
component name；当前 schema v3 继续保留它，使已有 owner ID、virtual APK package、
dependency 和 installation marker、workspace/cache/build identity 以及 recorded output
在逻辑重命名后保持稳定。之后的 input 变化会通过该保留身份 rebuild 和 cleanup，而不是
创建第二个所有权 namespace。

在分阶段 host rollout 的整个过程中保留该 block；只有所有 host 均已迁移，并且保留
该 block 时 plan/check 干净后，才将其移除。参见 [DSL 验证与生命周期](dsl-reference.zh.md#component-地址迁移)、
[运维步骤](operations-runbook.zh.md#重命名-component-实例)和
[可运行离线示例](../examples/component-moved.apf.hcl)。

四 branch 的
[`component-moved` VM case](../test/integration/libvirt/cases/component-moved)
从旧 worker 和 source-builder instance 开始。一个独立、只读、仅重命名的 plan/check 必须
显示精确的 18 个 move、18 个 no-op resource、零 mutation action，以及逐字节相同的 state
和远端 identity snapshot。随后编号 lifecycle 将 rename 与一次有效 file update 及其
change script 结合起来：`update=2`、`no_op=16`，且没有 create、delete、service restart
或 source rebuild。该 case 还要求保留 block 和移除 block 时均为 no-op，后续 source-input
变化通过原始 physical source-build identity rebuild，并最终移除 component 及其 managed
artifact。断言会拒绝重复 artifact cache、script marker、source-build owner package、
dependency 或 install marker、workspace 和 output ownership。

这项 real-VM 覆盖在 Alpine 3.21 至 3.24 x86_64 上运行，并有专用 aggregate gate。它使
moved-state regression 成为阻塞项，但不会把 additive alpha DSL、由 state v3 保留的
v2-origin identity map 或 plan field 纳入 v0.1 Beta 承诺；component-root move 仍为 Preview。

## Component 内部的资源依赖

component template 内的 `packages.package`、`files.file` 和运行时
`services.service` 声明可以使用静态类型化 `depends_on` 引用；生成的
`openrc.service` 声明不能使用。解析局限于该 template，并会在每个 mounted instance 的
address prefix 下重复。component resource 不能引用 host-level resource 或 sibling
component 中的 resource，即使 label 相同也不行。

这项 resource-level 语法不同于 mounted component block 的
`depends_on = [component.<instance>]`，后者用于排列 component root。资源依赖只增加
顺序，绝不会激活 component `on_change` script。plan 显示当前 graph resource 的完整
effective ordering；state v3 只保留 target 仍被跟踪的作者声明 resource edge，使
component move 和 orphan teardown 能够安全保留它们。规范契约见
[资源依赖](dsl-reference.zh.md#资源依赖)。

## 预构建 artifact

artifact component 声明 `type`、一个或多个已验证 source 和安装 destination：

```hcl
component "tool" {
  type    = "binary"
  version = "1.2.3"

  source "amd64" {
    url    = "https://downloads.example.invalid/tool-1.2.3-linux-amd64"
    sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  }

  source "arm64" {
    url    = "https://downloads.example.invalid/tool-1.2.3-linux-arm64"
    sha256 = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
  }

  install {
    path  = "/usr/local/bin/tool"
    owner = "root"
    group = "root"
    mode  = "0755"
  }
}
```

支持的 type 为 `binary`、`file`、`archive` 和 `ca_certificate`。binary 和 archive
component 仍为 Beta。file 和 CA-certificate component 在 x86_64 上通过阻塞式
Alpine 3.21-3.24 `components` matrix 后同样为 Beta。下面描述的 per-instance
source-expression 扩展是 additive alpha DSL 接口；它不会改变这些运行时支持级别。

architecture label 使用规范化的 `amd64` 或 `arm64` fact。一个无 label 的 `source`
与 architecture 无关；有 label 和无 label 的 source 不能混用。仅当必须选择有 label
source 时，离线 planning 才需要 `platform.architecture`。

### 每个实例的 source 表达式

`source.url` 和 `source.sha256` 可以引用 component input。AlpineForm 会先为一个
mounted instance 规范化、类型检查并验证 input，然后为该 instance 求值每个 source
expression，最后才选择 architecture source 并构建其 artifact graph：

```hcl
component "tool" {
  input "mirror" {
    type      = string
    sensitive = true
  }

  input "checksum" {
    type      = string
    ephemeral = true
  }

  type    = "binary"
  version = "1.2.3"

  source "amd64" {
    url    = "${input.mirror}/tool-1.2.3-linux-amd64"
    sha256 = input.checksum
  }

  install {
    path = "/usr/local/bin/tool"
    mode = "0755"
  }
}

host "edge_a" {
  platform { architecture = "amd64" }
  component "tool" {
    source = component.tool
    inputs = {
      mirror   = "https://mirror-a.example.invalid"
      checksum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
    }
  }
}

host "edge_b" {
  platform { architecture = "amd64" }
  component "tool" {
    source = component.tool
    inputs = {
      mirror   = "https://mirror-b.example.invalid"
      checksum = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
    }
  }
}
```

依赖 input 但未挂载的 template，只要静态 shape 完整就仍然有效。AlpineForm 会保留
expression 及其 source location，而不会虚构 required input value；resolved URL 和
checksum 会针对每个 mount 验证。离线编译根据已声明 platform fact 选择带 label 的
source，在线编译则根据 observed target fact 选择。

该求值边界仅限 `source.url` 和 `source.sha256`。`type`、`version`、source label、
`extract`、`build` 和 `install` 仍是 template-time metadata。目标端 source-build input
保留其已有的独立 Preview 语义。

literal source 声明保留现有 checksum-keyed cache、resource address、desired/state
representation 和 provider 行为。受保护的 resolved URL 和 checksum 仍是临时 controller
内存值：编译期间先存在于 mounted IR，随后存在于 in-memory provider payload。保护不会
引入新的 resource-address scheme：artifact source address 继续使用 logical mounted
component name 和 normalized source label。受保护的 cache path 则使用保留的 physical
component identity 和 normalized source label（`any`、`amd64` 或 `arm64`），绝不使用
原始或派生的受保护材料。这一稳定 cache identity 不会让每次 source 变化都成为 action
no-op：若已有相同且验证通过的 checksum，只更改 URL 或 mirror 是 durable no-op；轮换
checksum 则可能规划 update 或 repair。隐藏的 protected intent 会参与 preview 与 locked
plan 的比较，因此即使 raw value 未序列化，resolved URL 或 checksum 的变化也要求重新
审阅 locked plan。

每个 source 必须是无嵌入凭据或 fragment 的绝对 HTTP(S) URL，并包含精确 64 字符的
SHA-256。download 先进入 component cache 中的临时文件，验证通过后才替换之前的 cache。
binary 和 file install 会再次验证 cache，并原子替换 destination。远端 check 会观察已安装
digest、owner/group 和 mode。

`archive` 当前接受 `tar.gz`，并要求 `extract` block：

```hcl
extract {
  format           = "tar.gz"
  strip_components = 1
}
```

extract 会拒绝绝对路径和父目录遍历路径、link、special file、不安全名称，以及 stripping
后发生冲突的 destination。它会在 destination 旁的空 staging 目录中 extract，且仅在验证
后 swap destination；失败会保留之前的 installation 不变。下面的 source-build workspace
设置不会迁移这个 destination-adjacent staging，因为 archive replacement 必须保留在
destination filesystem 上。已安装 tree 带有 content manifest，供 `check` 检测 missing、
added 或 modified file。

CA certificate 必须以 `.crt` 文件安装到 `/usr/local/share/ca-certificates/` 下。
`update-ca-certificates` 及其 success marker 是 apply transaction 的一部分。失败的 trust
refresh 会重试，绝不会记录为成功的 resource state。

已有 `components` VM case 会在每个受支持 x86_64 branch 上测试 binary、file、archive
和 CA-certificate source。它仍是 12 个阻塞式 integration case 之一，因此 managed-target
matrix 仍为 48 个 job。

移除 component 会销毁其已安装 artifact，并移除其 verified cache。archive destination
会递归移除。如果移除必须要求显式配置变更，请在 component instance 上使用
`lifecycle.prevent_destroy`。

目标端 build 是独立的 Preview capability。其 schema、protected-value 规则、所有权、
失败行为和 threat boundary 记录于[目标端 source-build 安全](source-build-security.zh.md)。
它们不会削弱上面的预构建 artifact 契约。

## Preview source build

source build 具有固定 input、argv command、一个相对 output，以及普通 component install
destination：

```hcl
component "musl_hello" {
  type = "source"

  build {
    input "source" {
      source      = "fixtures/hello.c"
      sha256      = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
      destination = "hello.c"
    }
    command { argv = ["mkdir", "-p", "build"] }
    command { argv = ["cc", "-Os", "-static", "-o", "build/hello", "hello.c"] }

    output           = "build/hello"
    max_output_bytes = 67108864
    executable       = true
    dependencies     = ["build-base"]
    network          = "none"
    on_remove        = "forget"
  }

  install {
    path = "/usr/local/bin/musl-hello"
    mode = "0755"
  }
}
```

### 工作区放置

目标端 build 默认使用 `/var/tmp/alpineform/builds`。合并后的 profile/host
`staging.root` 提供 host 默认值，mounted source component 的 `staging_root` 优先级最高。
完整语法、验证和 replacement 行为见 [DSL 参考](dsl-reference.zh.md#source-build-工作区根目录)。

root 仅是运行时位置。它不会序列化，也不进入 component 的 build identity、graph
identity、state、installation decision 或 change-script trigger。因此，当 verified output
cache 仍有效时，只更改 root 不会 rebuild 或 reinstall。下一次因实际 input、command、
output-policy、platform、dependency 或 install 变更而触发的 rebuild，会使用新选择的 root。

每次 build 都会获得 root 所有的私有 `<root>/<64-hex-build-identity>` 目录及其 `build`
子目录，两者模式均为 `0700`，另有模式为 `0600` 的 ownership marker。持久 dependency
ownership 仍位于 `/var/lib/alpineform/builds` 下，verified output cache 仍位于
`/var/cache/alpineform/builds` 下，受保护 ephemeral input 仍位于
`/run/alpineform/build-inputs` 下，而不会移至可配置 disk root。只有通过 provider 的
ownership、mode、symbolic-link 和 marker 检查后，workspace root 和 recorded old path
才会被接受或移除。详见[安全契约](source-build-security.zh.md#工作区放置与所有权)。

input 必须且只能选择 `source`、`url` 或 `content` 之一，始终附带精确 `sha256` 和整洁的
workspace-relative `destination`。`source` 是 declaring module 目录下 controller-local
普通文件。`url` 是 HTTP(S) transport locator；在 checksum 通过前不信任其 response。
`content` 可以使用受保护 component input，此时还要求公开的 `content_version`。input
可以添加：

```hcl
extract {
  format           = "tar.gz"
  strip_components = 1
}
```

`working_directory` 默认为 `.`。每个 `command` 都要求 `argv`；由 sensitive 或 ephemeral
值派生的可选 `stdin` 要求 `stdin_version`。`environment` 是 string map；受保护条目要求
一个公开的 `environment_version`。不得 override `PATH`、loader injection variable、
shell startup variable、`HOME` 和 `TMPDIR`。

`output_sha256` 可选。`max_output_bytes` 默认为 64 MiB，且不得超过 1 GiB。
`executable = true` 会添加安装前 execution-bit 检查。Bubblewrap 会自动添加到
`dependencies`；所有 dependency 都属于一个由地址派生的 APK virtual package，并在验证
后移除。唯一 network policy 是 `none`。

移除时默认为仅 forget state。`on_remove = "destroy"` 会记录 verified
installation/cache identity，以供 guarded deletion 使用，component
`lifecycle.prevent_destroy` 会阻止它。完整可运行示例见
[source-build 示例](../examples/source-build.apf.hcl)。

专用 `source-build` VM case 在 Alpine 3.21、3.22、3.23 和 3.24 x86_64 上运行，每个
版本有 48 项显式断言。它证明 legacy default、instance root 胜过 profile/host candidate、
在 `/var/tmp` 受限时运行、cached root-only no-op、下次 rebuild 的放置、guarded cleanup、
failure preservation，以及已有 Bubblewrap 和 protected-input 保证。compiler test 覆盖
优先级规则的 profile-only 和 host-default branch。该阻塞式 gate 不会把目标端 source
build 提升到 Preview 以上。

## 变更脚本

script 使用 command array 或 interpreter content：

```hcl
script "refresh_worker" {
  commands = [
    ["rc-service", "worker", "reload"],
  ]
  outputs = ["/run/worker.refreshed"]
}

component "worker_config" {
  script "render" {
    interpreter = ["/bin/sh", "-eu"]
    content     = "render-worker-config"
    sensitive   = true
  }

  files {
    file "/etc/worker.conf" {
      content   = "enabled=true\n"
      on_change = global.script.refresh_worker
    }
  }
}
```

`script.<name>` 会先解析 component-local 声明，再解析 top-level 声明。
`global.script.<name>` 显式选择 top-level 声明。去重在一台 host 上使用 resolved
declaration identity，而不是 label 或 command text。因此，引用同一个 top-level script
的多个 changed file 或 artifact 只生成一次 operation；unchanged plan 不会运行任何
operation。component-local 声明对每个 mounted instance 仍互不相同。作者声明的 resource
`depends_on` 边绝不会激活 script；只有独立的 `on_change` relationship 会贡献 active
`triggered_by` address。

`outputs` 是绝对普通文件路径。成功执行后，其 digest 和 script declaration digest 会记录
在远端 marker 中。output 缺失或发生变化，以及 script body 变化，都会重新运行 script。
output 会被观察，但移除 script 声明时不会删除。

provider 会向每次执行导出 `APF_SCRIPT_NAME`、`APF_TRIGGER_ADDRESS`、
`APF_TRIGGER_PATH`、`APF_TRIGGER_ADDRESSES` 和 `APF_TRIGGER_PATHS`。command 作为位置
参数传递；content 通过已脱敏 stdin 发送。sensitive script payload 会从 graph、plan、
state、HTML、debug output 和 provider error 中省略。script 失败会在成功 state write 前
中止 apply。

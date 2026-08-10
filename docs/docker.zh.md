<p align="right"><a href="docker.md">English</a> | <strong>简体中文</strong></p>

# Docker Engine 与 Compose

Docker 和 Compose 是适用于持久化 Alpine 3.21 至 3.24 x86_64 主机的 Preview domain。
AlpineForm 使用 Alpine 的 `docker` 和 `docker-cli-compose` package、发行版提供的 OpenRC
service 以及 Docker CLI plugin。它绝不会配置 Docker APT repository、systemd unit 或
Docker 上游 package repository。

```hcl
variable "app_env" {
  type      = string
  sensitive = true
  ephemeral = true
}

host "edge" {
  docker {
    enable  = true
    members = ["deploy"]

    daemon_config = jsonencode({
      log-driver = "json-file"
      log-opts = {
        max-size = "10m"
        max-file = "3"
      }
    })

    project "app" {
      directory = "/srv/app"
      compose = <<-YAML
        services:
          app:
            image: alpine:3.24
            restart: unless-stopped
            command: ["sleep", "infinity"]
      YAML
      env         = var.app_env
      env_version = "production-v1"
      state       = "running"
    }
  }
}
```

## Engine 与 package 所有权

`docker` block 接受：

| 属性 | 默认值 | 含义 |
| --- | --- | --- |
| `ensure` | `present` | `present` 管理 engine；`absent` 会先停止 project 和 OpenRC，再显式移除 owned package。 |
| `enable` | `true` | 在 OpenRC 的 `default` runlevel 中 enable Docker 并保持 running。`false` 让已安装的 engine 保持 stopped 和 disabled。 |
| `package_source` | `alpine` | `alpine`、`custom` 或 `none`。 |
| `package_repository` | none | `custom` 必需的 APK repository tag；其他 source 禁止使用。 |
| `members` | `[]` | 必须成为 `docker` 组补充成员的已有或已声明 Alpine user。 |
| `daemon_config` | unmanaged | `/etc/docker/daemon.json` 的 JSON string。 |
| `daemon_config_version` | none | JSON expression 为 ephemeral 时必需的公开变更 token。 |
| `daemon_config_sensitive` | `false` | 即使 expression 未标记为 sensitive，也保护整个 daemon-config resource。 |

`package_source = "alpine"` 管理检测到的目标 branch 对应的精确官方 `community` 条目，
例如 `https://dl-cdn.alpinelinux.org/alpine/v3.21/community`，以及显式 APK world intent
`docker` 和 `docker-cli-compose`。已有的等价 present repository 会被复用。如果 host APK
所有权是 authoritative，则必须在 `apk` block 中显式包含该 repository；AlpineForm 拒绝
隐式更改 authoritative 集合。

2026-07-14 的 Alpine 3.24 x86_64 gate 解析出 `docker 29.5.3-r0` 和
`docker-cli-compose 5.1.4-r0`。patch revision 不固定，因为 stable branch repository 会
原地更新；每次 VM 运行都会打印已安装的 `apk info -v` 结果，使 CI 证据中始终可见确切
测试版本。

`package_source = "custom"` 仍安装 Alpine package 名称，但会把声明的
`package_repository` tag 附加到它们的 world intent。该 tag 必须引用 host `apk` block 中
一个 present、带 tag 的 repository。repository 和 signing-key lifecycle 仍由该 APK
声明拥有。

`package_source = "none"` 绝不会更改 repository、APK world intent 或 package。
Docker、其 init script 和 Compose plugin 必须已存在。AlpineForm 随后可以管理 service、
daemon 配置、group membership 和 project。缺失的前置条件会让 apply 失败，而不是合成
第三方 source。

该 domain 拥有 package 名称、`docker` group、Docker OpenRC service 和
`/etc/docker/daemon.json`；重复的通用声明会被拒绝。移除整个 block 会忘记这些 state
条目并保持目标不变。使用 `ensure = "absent"` 显式移除。package absent 只对两个已记录
world intent 使用 `apk del`；`docker` group 会被 forget 而不是 delete。

## Daemon 配置与 restart

编译器要求 `daemon_config` 是 JSON object，并确定性地将其规范化。apply 仅通过受保护
SSH stdin 发送 content，将其 stage 到目标旁，运行：

```sh
dockerd --validate --config-file <staged-file>
```

并且只在验证后原子替换 `/etc/docker/daemon.json`。无效 JSON 会在编译期间失败；
Docker 判定无效的 candidate 会保留之前的文件和 running daemon 不变。provider 拒绝
符号链接和非普通目标。

graph 中只有一个 Docker service 节点。daemon-config 的任何 create、update 或漂移修复
都会在文件成功后触发该节点一次，因此同一 plan 中的多个变更不会导致多次 restart。
语义相同的 JSON 会规范化为相同 content，不执行 restart。只移除 `daemon_config`
attribute 会 forget 该文件，不会 delete 或 restart；`docker.ensure = "absent"` 会在删除
文件前停止 Docker。

## Compose project

每个 `project "name"` 都有稳定身份，并接受：

| 属性 | 默认值 | 含义 |
| --- | --- | --- |
| `directory` | required | 整洁的绝对 project 目录，位于 `/etc/docker` 之外。 |
| `compose` | required | Compose YAML content。AlpineForm 拥有 `<directory>/compose.yaml`。 |
| `compose_version` | none | ephemeral Compose content 必需的公开变更 token。 |
| `env` | absent | 可选 env-file content。AlpineForm 拥有 `<directory>/.env`。 |
| `env_version` | none | ephemeral env content 必需的公开变更 token。 |
| `state` | `running` | `running`、`stopped` 或 `absent`。 |
| `sensitive` | `false` | 保护两个托管文件和所有序列化/diagnostic surface。 |
| `on_remove` | `forget` | `forget` 保留文件和 runtime 不变；`destroy` 为 orphan cleanup 记录安全 project identity。 |

project 名称使用小写字母、数字、下划线和连字符。每个 host 上的目录必须唯一。生成的文件
是 root 所有、模式为 `0600` 的普通文件；目标符号链接和非普通文件会被拒绝。

对于每次 create 或 update，AlpineForm 会把两个 desired payload 写入临时私有目录，并对
candidate 运行 `docker compose config --quiet`。只有有效 candidate 才能替换持久化文件
或调用 `up`、`stop` 或 `down`。`running` 执行 `up --detach --remove-orphans`；
`stopped` 会停止已有 container，然后运行 `create --remove-orphans`，使新声明收敛为已经
存在但从未启动的 container。显式 `absent` 会验证 candidate、执行
`down --remove-orphans`，并且只移除两个托管文件。不会移除 named volume、image 或
external resource。

检查使用 Compose 配置的 service set 以及 Docker 的 project/service label，并报告一个
稳定类别：

- `running`：每个已声明 service 都有 running container，且没有 stopped 副本。
- `partial`：已声明 service 缺失，或在 running 和 stopped 间混合。
- `stopped`：每个已声明 service 都有 stopped/created container，且没有运行中的副本。
- `absent`：不存在 project container。
- `degraded`：Docker/config 检查失败，或 container label/state 与已声明 project 不一致。

这些类别参与普通 no-op 和漂移修复。Compose 文件的 restart policy 仍属于应用意图；
当 `running` project 必须在 host reboot 后存续时，请使用合适的 policy。

敏感值绝不会放入 graph JSON、plan text/JSON/HTML、state observation、debug event、
provider error 或 integration diagnostic。ephemeral content 还会省略其 content-derived
digest，并要求公开 version token，以便无需持久化即可规划变化的 intent。

## 删除与恢复

移除声明时默认为仅 forget state。带 `on_remove = "destroy"` 的 project 只记录其名称
和固定托管路径。orphan destroy 会先使用已有的有效 Compose 文件；如果它们不可用，则
只移除带有完全匹配的已记录 Compose project label 的 container 和 network。它绝不会
移除 volume 或 image。`lifecycle.prevent_destroy` 会在 provider 执行前阻止显式的
project/engine absence 和已记录的 destroy。

文件、元数据和 runtime state 仍匹配的未受保护 forgotten project 可以在不远端写入的
情况下 adopt。只写 content 有意采用不同语义：state 条目丢失后，AlpineForm 无法证明
远端 secret 与公开 version token 匹配，因此重新引入时会规划 update，而不是不安全的
adopt。

apply 失败时，请保持 Docker 运行，修正 candidate，并重新运行 `apf plan` 和
`apf apply`。由于只有完整 host sequence 成功后才写入 state，检查会显示任何已成功更改
的文件或 runtime state，下一次 apply 会使其收敛。受保护 diagnostic 见
[运维运行手册](operations-runbook.zh.md)。

## 支持边界

四 branch 阻塞式 `docker` libvirt matrix 证明 package 安装和版本报告、OpenRC/reboot
持久性、Docker-invalid daemon 和 Compose-invalid candidate 隔离、单次触发的 daemon
修复、受保护 env content、新建 running/stopped project、partial/degraded 漂移恢复、
forget/adopt、保留 named volume 的限定范围 destroy、显式 absence 以及完整 engine 移除。
该 domain 仍为 Preview，因为 Alpine `community` 的支持窗口短于 `main`；AlpineForm 的
compatibility gate 不会延长上游对旧 branch 的安全维护。不存在 Alpine aarch64 Docker
VM gate。

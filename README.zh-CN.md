# AlpineForm

<p align="right"><a href="README.md">English</a> | <strong>简体中文</strong></p>

AlpineForm（`apf`）是一款面向 Alpine Linux 主机的声明式配置工具。它使用同一份配置验证
HCL、预览变更、通过 root SSH 收敛目标，并报告漂移：

```text
apf validate -> apf plan -> apf apply -> apf check
```

AlpineForm 是预发布软件，并非 Alpine Linux 官方项目。首个完整 preview 版本是
`v0.1.0-alpha.5`。Alpha.1 至 alpha.4 作为不完整的 prerelease 保留，不得使用。兼容性
保证记录在[兼容性策略](docs/compatibility-policy.zh.md)中。

## 受支持的核心（Supported Core）

阻塞门禁覆盖使用 OpenRC 的持久化 Alpine 3.21 至 3.24 x86_64 目标。核心可管理文件、
目录、组、用户、authorized key、APK 仓库和软件包、受限或原始 OpenRC 服务、主机名、
时区、内核模块、sysctl，以及经过验证的预构建组件。每个 Beta domain 都会在每个受支持
分支的全新 VM 中完成 apply、no-op plan、适用时的漂移与修复，以及重启测试。

在四分支阻塞 `components` case 下，binary、file、archive 和 CA-certificate 组件均为
Beta。每个挂载实例的 `source.url` 和 `source.sha256` 字段可以在输入经过规范化后分别求值；
该表达式语法属于增量 alpha。literal 行为、资源地址、state schema v3 和
`alpineform.plan.alpha1` 保持兼容，目标侧 source-build 语义不变。

Alpine 3.21 至 3.24 aarch64 仍为 Preview，因为它具有交叉构建和 selector 覆盖，但没有
阻塞式真实 VM 门禁。Docker Engine 和 Compose 是已实现的 Preview domain，并由四分支
x86_64 VM 门禁覆盖；由于它们依赖 Alpine `community`，仍不属于 v0.1 核心承诺。
具备回滚安全性的 named-table nftables 也是已实现的 Preview domain，拥有专用的四分支
阻塞回滚门禁，并要求单独批准网络中断。目标侧 source build 是独立的 Preview domain，
具备校验和输入、离线 argv 执行、受所有权管理的构建依赖、原子安装和专用的破坏性 Alpine
VM 门禁；它仍不属于核心承诺。完整信息请参阅[支持矩阵](docs/support-matrix.zh.md)。

## 安装

Release archive 使用 `CGO_ENABLED=0` 构建，支持 Linux 和 macOS 上的 amd64 与 arm64。
安装程序下载所选 archive 和 `checksums.txt`，验证 SHA-256，并原子安装 `apf`：

```sh
curl -fsSL https://raw.githubusercontent.com/mofelee/alpineform/main/scripts/install.sh |
  sh -s -- --version v0.1.0-alpha.5
apf version
```

每个 archive 还包含双语根文档和完整的双语 `docs/` 文档树。curl 安装程序和
`make install` 会把这些资料放在 `<prefix>/share/alpineform` 下；请从
[文档索引](docs/README.zh.md)开始阅读。

安装到私有 prefix：

```sh
sh scripts/install.sh \
  --version v0.1.0-alpha.5 \
  --prefix "$HOME/.local"
```

此 release 不发布 Homebrew。只有在其安装、测试和升级路径具备真实自动化证据后才会提供。

对于私有仓库或镜像，请从已认证的 checkout 运行安装程序，并导出 `GITHUB_TOKEN` 或
`GH_TOKEN`；安装程序会通过 GitHub API 解析需要认证的 release asset。

## 快速开始

控制主机需要安装 `apf` 和 OpenSSH。被管理主机必须是可使用密钥以 root 身份访问的持久化
Alpine 3.21 至 3.24 安装。请将目标写入 OpenSSH 配置；在线事实发现不要求在 AlpineForm
文件中提供 platform 值：

```sshconfig
Host alpine
  HostName 192.0.2.10
  User root
  IdentityFile ~/.ssh/alpine
  IdentitiesOnly yes
```

[`examples/quickstart.apf.hcl`](examples/quickstart.apf.hcl) 会创建一个小型受管目录和文件：

```hcl
host "alpine" {
  ssh {
    host = "alpine"
  }

  directories {
    directory "/etc/alpineform-example" {}
  }

  files {
    file "/etc/alpineform-example/managed.conf" {
      content = "managed-by=alpineform\n"
      mode    = "0644"
    }
  }
}
```

运行完整工作流：

```sh
apf validate -f examples/quickstart.apf.hcl
apf plan --offline -f examples/quickstart.apf.hcl
apf plan -f examples/quickstart.apf.hcl
apf apply -f examples/quickstart.apf.hcl
apf check -f examples/quickstart.apf.hcl
```

`apply` 会先预览，再获取锁，并在可续期的逐主机 lease 内重新生成 plan，然后请求批准实际
锁定的 plan。无漂移时 `check` 以零退出；存在漂移时，它会输出所需 action 并以非零退出。
远程 state 存储在 `/var/lib/alpineform/state.json`，mode 为 `0600`。

当前 state schema v3 会在内存中读取 v1 和 v2，并在下一次 apply 时写入 v3，即使该 apply
完全 no-op 也一样。在执行该 apply 之前，请保留逐主机备份以及匹配的旧配置和 binary；参阅
[state 迁移 runbook](docs/operations-runbook.zh.md#state-备份和恢复)。

nftables 的实时激活或删除会被另行标记为网络中断操作，并要求使用
`apf apply --allow-network-disruption`；仅提供 `--auto-approve` 并不充分。

## 配置

配置使用 `*.apf.hcl`。变量输入可使用 `alpineform.apfvars[.json]`、
`*.auto.apfvars[.json]`、显式 `-var-file`、`-var` 或 `APF_VAR_<name>`。可复用的
`profile`、`component`、`script`、`locals`、`variable` 和 `assert` 声明会编译为
确定性的资源地址和依赖顺序。

`packages.package`、`files.file` 和运行时 `services.service` 声明接受静态、同 scope、
带类型的 `depends_on` 引用。生成式 `openrc.service` 声明不接受此引用。作者声明的依赖会
增加排序关系，包括在显式远程删除时反转顺序。它们绝不会激活 OpenRC operation 或 change
script。推断出的前置条件与 `triggered_by` 关系仍彼此独立。参阅
[DSL reference](docs/dsl-reference.zh.md#资源依赖)和
[plan 关系契约](docs/plan-format.zh.md#关系)。

请从 [DSL 和 CLI reference](docs/dsl-reference.zh.md)开始，然后使用各 domain 指南：

- [文件](docs/files.zh.md)、[目录](docs/directories.zh.md)、
  [组](docs/groups.zh.md)和[用户](docs/users.zh.md)
- [APK 和软件包](docs/apk.zh.md)
- [OpenRC 服务](docs/openrc.zh.md)
- [系统设置](docs/system.zh.md)和[内核设置](docs/kernel.zh.md)
- [组件和 change script](docs/components.zh.md)
- [Docker Engine 和 Compose](docs/docker.zh.md)（Preview）
- [具备回滚安全性的 nftables](docs/nftables.zh.md)（Preview）

运维契约由[架构](docs/architecture.zh.md)、[state backend](docs/state-backend.zh.md)、
[lock 模型](docs/locking.zh.md)、[安全模型](docs/security-model.zh.md)和
[运维 runbook](docs/operations-runbook.zh.md)说明。

## 开发

```sh
make build
make docs-check
make check
make vulncheck
make test-integration-layout
```

真实 VM harness 和远程 libvirt 设置记录在[integration runbook](test/integration/libvirt/README.zh.md)
中。Release 工作遵循[发布流程](docs/release-process.zh.md)。文档变更遵循
[本地化政策](docs/localization-policy.zh.md)。

## 来源与许可证

AlpineForm 使用 DebianForm v0.6.0 作为架构和部分代码的参考来源。
[NOTICE.md](NOTICE.zh-CN.md)记录了确切的上游提交和主要变更。AlpineForm 独立进行版本管理，
不接受 DebianForm 配置或 state。本项目采用 MIT License；详见 [LICENSE](LICENSE)。

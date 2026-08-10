<p align="right"><a href="security-model.md">English</a> | <strong>简体中文</strong></p>

# 安全模型

AlpineForm 是 root 配置管理器。一次成功的 apply 可以修改整个目标；配置、release artifact、
control host、SSH key 和经过审查的 plan 都属于信任边界。

## 传输和权限

- v0.1 使用系统 OpenSSH client，并始终以 root 身份连接。
- Host-key 检查、alias、proxy jump 和 identity selection 仍由 OpenSSH policy 管理。
  `APF_SSH_CONFIG` 可用于隔离显式配置文件。
- AlpineForm 启用 batch mode、禁用 forwarding，并限制连接时间。它不实现 sudo、doas、
  password login 或 agent policy。
- 远端 script 的 source 固定。用户控制的 identity 和 value 作为 positional argument 或已脱敏
  stdin 传递，而不是插值到 shell 中。

## Plan、Lock 和 State

在线编译在读写 state 前发现固定的只读 facts。非 Alpine 目标和 platform-mismatched 目标会在
state、lock 或资源 mutation 前失败。`apply` 显示 preview，获取可续约的独占租约，重新 plan，
并要求批准锁内 execution plan。Nftables mutation 会添加独立的 `network_disruption` plan risk，
在 preview 和 locked review 时都要求 `--allow-network-disruption`；普通 plan approval 和
`--auto-approve` 不能静默授予 firewall authorization。

State 以原子方式写入 `/var/lib/alpineform/state.json`，directory mode 为 `0700`，file mode 为
`0600`。运行时租约位于 `/run/lock` 下，重启后不会保留。State 不是 secret vault：应保护目标
root access，不要把明文 secret 放入非 sensitive 资源字段。

Schema v2 引入对逻辑 component root 及其 legacy physical component name 的保留，使声明 move 后
由地址派生的 provider ownership 保持稳定。当前 schema v3 保留该 map，并能存储 authored resource
dependency address，用于 dependent-first orphan teardown。这些名称、资源地址和 relationship array
是 metadata，不是 secret channel。不要把 credential 或其他受保护材料放入 declaration label、
资源 identity 字段、file path、service name 或 dependency target。资源 `depends_on` 只接受静态
typed reference；dynamic、sensitive、ephemeral 和原始 expanded graph-address expression 会在
graph 或 state 序列化前被拒绝。

## 受保护值

Sensitive value 会在 graph、plan text、plan JSON、HTML、state、debug、diagnostic 和 error
序列化前被替换。Ephemeral value 既不持久化其值，也不持久化从内容派生的 digest。受保护的 SSH
stdin 和远端 stderr 会从 error 中省略。Integration failure artifact 会清除 public key material、
key blob 和 sensitive sentinel；private key、seed image、state 和 scenario copy 绝不会上传。

已实现的 move 只暴露 `host`、`from` 和 `to` 地址。Move summary、validation failure、state
collision diagnostic、locked-plan comparison 和 retry error 不得把这些地址展开为 desired 或
observed payload、component input、provider output 或存储的受保护数据。移动受保护资源会保留其
protected marker 和 ephemeral digest 规则；state rewrite 绝不会实体化脱敏值。

Plan `depends_on` 和 `triggered_by` array 同样只包含稳定地址。它们绝不会把地址展开为 desired
content、command、provider output 或受保护值。Authored ordering 不能静默变成 service 或 script
trigger。

## 下载和 Component

Component download 要求声明 SHA-256，并在 installation 前重新验证。Archive extraction 会拒绝
traversal、absolute path、link、special file、不安全名称和 strip 后的 collision。APK repository
接受不含嵌入 credential、query 或 fragment 的 HTTPS URL。AlpineForm 不会调用 distribution
upgrade。

预构建 component `source.url` 和 `source.sha256` 表达式从挂载 input 继承 sensitive 和 ephemeral
mark。已解析的受保护值只在挂载 IR 的 controller-memory value 和内存 provider payload 中短暂
存在。Provider command 仅通过标记为需要脱敏的 stdin 发送这些值。它们绝不会序列化到 compiled
host 或 graph JSON、text/JSON/HTML plan、state、debug event、diagnostic、provider error、远端
command argument、script、environment、output 或 log。

受保护 artifact cache 使用保留的 physical component identity 和规范化 source label，而不是 URL、
checksum 或从任一值派生的 digest。序列化 state 可以保留 cache 和 delete path、protection flag、
verification status、ownership、mode、deletion policy，以及只从安全 metadata 计算的 desired
digest 等安全 metadata。它绝不保留原始或派生的受保护 URL 或 checksum。公开 literal source
保持其现有 checksum-keyed cache identity 和 state 表示。

Preview 目标端 build 有独立的[威胁模型和 ownership 契约](source-build-security.zh.md)。它们要求
带 checksum 的 input 和 argv command，禁用 build-command networking，省略 build log，并且只有
在 output verification 和受管 cleanup 成功后才替换 installation。

## Docker 和 Compose

Docker package 只能来自受支持的 Alpine repository set 或显式带 tag 的 APK 资源；AlpineForm
绝不会添加 Docker upstream 或 Debian repository。Daemon JSON 会 canonicalize、stage、由
`dockerd --validate` 验证，并在 graph 触发的单次 OpenRC restart 前以原子方式替换。

Compose 和 env content 通过受保护的 SSH stdin 进入 mode-`0700` 临时目录。在 persistent file
或 runtime state 发生变化前，`docker compose config --quiet` 必须接受完整 candidate。
Persistent project file 使用 mode `0600`。Project name 和 path 是经过验证的 provider argument，
不是 shell source。显式 project deletion 受 label 和 path 限定，绝不会移除 volume 或 image。
Sensitive 或 ephemeral payload、远端 stderr 和从内容派生的 ephemeral digest 会从序列化和
diagnostic 表面中省略。

## nftables

Preview nftables domain 只拥有声明的 `(family, name)` table identity。它不能表达 include、nested
table、top-level command 或 whole-ruleset flush。现有 table 需要显式 adoption，external table、
stock configuration 和 stock OpenRC service 仍在 AlpineForm ownership 之外。

Rule body、active 与 persistent snapshot、observed fingerprint、runtime token 和 token digest
保持在受保护 provider 边界后。仅 root 可访问的 detached watchdog 会在激活前 snapshot 先前 named
table 和 persistence，然后恢复它们，除非新的 SSH process 通过已配置管理路径确认 candidate。
只有确认后才写入 state。Pending 或 failed recovery artifact 保持仅 root 可访问，以供记录的
[operator 恢复流程](operations-runbook.zh.md)使用；transaction 可能仍存活时，不得发布或删除它们。

## Release 供应链

Release binary 使用 `CGO_ENABLED=0`、固定版本的 GoReleaser tooling 和四个固定 OS/architecture
目标。Release 包含 SHA-256 checksum、每 archive 一个 SPDX JSON SBOM、`checksums.txt` 的
keyless Sigstore bundle，以及 GitHub provenance attestation。Installer 在 extraction 或 replacement
前验证 archive checksum。验证命令位于[运维手册](operations-runbook.zh.md)中。

## 报告

按照 [SECURITY.zh-CN.md](../SECURITY.zh-CN.md) 中的说明，通过 GitHub private security advisory
报告疑似漏洞。不要在 public issue 中放入目标 detail、secret、key、state、plan、debug log 或
failure artifact。

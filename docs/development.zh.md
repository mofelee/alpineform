<p align="right"><a href="development.md">English</a> | <strong>简体中文</strong></p>

# 开发基线

AlpineForm 核心遵循单向 package 边界：

```text
parser -> merge -> IR -> graph -> plan -> engine -> provider -> backend
                                      |                    |
                                      +------ state -------+
```

- `parser` 发现并解码 AlpineForm 配置和变量输入。
- `merge` 将可复用 declaration 解析为中间表示。
- `ir` 包含已解析且与 provider 无关的期望状态。
- `graph` 创建资源 identity 和依赖顺序。
- `plan` 比较期望状态、先前状态和观测状态，不产生副作用。
- `engine` 调度 plan、apply 和 check 工作流。
- `provider` 负责 Alpine 资源的观测和收敛。
- `backend` 负责传输、远端 state 持久化和运行时 lock。
- `state` 验证 AlpineForm state 信封和 schema 兼容性。

当前核心实现了 source discovery、typed variable、locals、input 优先级、product constant、version
metadata、确定性离线 plan、Alpine facts、root SSH、远端 state、运行时租约、在线
plan/apply/check，以及由 provider 支持的 host file、directory、group、user、supplementary
membership、authorized key、APK repository、APK signing key 和显式 APK package world intent。
静态资源依赖在 composition 后解析，编译进 graph，由 engine 强制执行，并作为仅 authored 的
metadata 保留在 state 中，以便可能的 orphan teardown。
`apf variable inspect` 输出稳定 JSON，并对 sensitive 和 ephemeral default 脱敏。`apf fmt`
在写入任何格式化内容前对每个选定文件做语法检查，不读取变量或运行时 input，并且是幂等的。
`apf validate` 负责 AlpineForm parsing、resolution、type checking 和 semantic validation。
不会暴露任何 Debian 资源 schema。

## 已实现的语言子集

- `variable`、`locals`、根级和嵌套 `assert`
- `profile` import，具有确定性的 component-instance 和 `staging.root` override 顺序
- typed `component` input、每个 instance 的预构建 source 表达式、组合的原生 domain、
  source-build `staging_root`，以及本地 instance dependency 验证
- 顶层和 component-local script，具有 reference-identity `on_change` 聚合和 output 观测
- `host` import 和可选的离线 `platform.architecture` / `version`
- component instance 上的 `lifecycle.prevent_destroy` metadata
- host 级 file、directory、group、user、membership、authorized-key、APK repository、APK key、
  聚合 APK update、package 和 service 资源
- `packages.package`、`files.file` 和运行时 `services.service` declaration 之间同一静态 scope
  内的 `depends_on` 引用

Platform architecture 规范化为 `amd64` 或 `arm64`。Alpine branch、`libc=musl` 和原生 APK
architecture 是推导出的只读 facts。仅当表达式实际引用相应 platform fact 时，离线编译才要求
architecture 或 version。

Parser 保留预构建的 `source.url` 和 `source.sha256` 表达式及其源码位置。Merge 在对这些表达式
求值并选择无 label 或架构特定 source 之前，规范化并验证每个挂载 instance 的 input。挂载后的 IR
只在 controller 内存中临时保存受保护的已解析值；graph 编译使其不进入序列化 desired data，
并通过内存中的 provider payload 携带这些值。此边界不扩展到 component `type`、`version`、
`extract`、`build` 或 `install`，也不会更改现有 source-build input model。

对于 source build，parser 还会保留 profile/host `staging.root` 和 instance `staging_root` 值、
protection mark 以及源码位置。Merge 按 instance、有效 host/profile、product default 的优先级进行
解析，并只把选定路径存入从 JSON 排除的运行时 IR 字段。Build identity document 有意省略 placement。
Graph 编译保持其稳定的 desired/resource identity，只通过内存中的 provider payload 加上未序列化的
runtime-intent digest 携带 root。Engine 的 plan-safe copy 清除 host/build placement 字段，而 graph
JSON 省略 runtime payload 和 digest。这些边界共同使 root 不会进入 graph、plan、HTML、debug 和
state 序列化。

资源 `depends_on` 作为 typed syntax 解析，而不是普通 HCL value。Merge 在 profile 优先级处理后
解析引用，并在每个挂载 component scope 内单独解析。Graph 编译把这些 authored edge 与结构性和
推断的 prerequisite 合并，但绝不会与 `TriggeredBy` 合并。执行保留正向顺序，对显式远端删除反转
authored edge，并使用仅 authored 的 state metadata 完成 orphan teardown。

## 离线 Plan

`apf plan --offline` 渲染 text 或 JSON，并能以原子方式写入独立 HTML report。
`alpineform.plan.alpha1` JSON 契约没有生成 timestamp，因此相同 input 和参数顺序会生成相同输出。
其 graph 为 host、platform fact 和 component instance 包含结构性的 `managed=false` node。
由 provider 支持的 host 和 component 资源是 `managed=true` node，并成为 plan summary 中的变更。

对于当前 graph 资源，plan `depends_on` 是经过排序和去重的结构性、推断和 authored 顺序的有效
集合。`graph[].triggered_by` 是结构性 trigger 集合；在线 `changes[].triggered_by` 只包含由
计划变更激活的 trigger。仅存在于 state 的 orphan 不会伪造当前 plan relationship。不存在公开的
仅 authored plan 字段；state v3 负责该狭窄 metadata 契约。

受保护的 desired value 会在 graph、plan、JSON 或 HTML 序列化前被替换。`--color auto` 遵守
`NO_COLOR` 和非终端输出；`--color always` 只影响 text。

尽管 root 本身不是受保护配置，source-build workspace placement 仍遵循相同的公开输出边界。
当已验证 output cache 满足 build 时，只更改 root 不会改变 desired digest 或 action；如果之后有
无关变更需要执行，运行时 provider node 仍会收到当前 root。

## 在线工作流

在线 plan/check/apply 使用两阶段编译。第一阶段只提取经过验证的 SSH identity，然后使用固定的
只读命令发现目标 facts。第二阶段使用这些 facts 重新编译所有 assertion 和资源 graph，之后才读取
远端 state。因此，不支持的目标和 platform mismatch 会在 state、lock 或资源写入前失败。

`apply` 在加锁前审查 preview。每台 host 都会在其可续约租约内重新构建并重新 plan，然后显示实际
的锁内 plan，并在 provider 或 state 写入前请求批准。`--parallel` 限制 host 工作并发量，同时
保持确定性的结果顺序。取消会停止同级工作，租约 cleanup 路径仍会尝试 release。任何非 no-op action
都会使 `check` 返回错误；干净 plan 则成功。

执行后，即使 provider action 是 no-op，apply 也会针对最终跟踪 state 协调 authored dependency
metadata。当前显式远端删除和仅 state orphan removal 使用 dependent-first 顺序；默认 forget 不执行
远端删除。

Nftables 激活和删除在 text 与 JSON plan 中标记为 `network_disruption`。除非提供
`--allow-network-disruption`，否则 `apf apply` 会拒绝这些步骤；普通交互批准和 `--auto-approve`
并不隐含该授权。preview 和每次锁内 replan 都会独立检查，因此在获取租约期间新出现的风险会在
provider 或 state 变更前被拒绝。

`apply --debug` 只输出结构性 facts/state/lock/inspect/apply/operation/cleanup event。命令 output、
stdin、desired/observed value 和原始受保护 error 绝不会包含在内。

目标 facts 使用与 state 和 lock backend 分离的只读 engine capability。请参阅
[facts.zh.md](facts.zh.md)。

远端 state 持久化在 [state-backend.zh.md](state-backend.zh.md) 中说明。
运行时 lock 行为在 [locking.zh.md](locking.zh.md) 中说明。
Root SSH 传输行为在 [ssh.zh.md](ssh.zh.md) 中说明。
受管 file 行为在 [files.zh.md](files.zh.md) 中说明。
受管 directory 行为在 [directories.zh.md](directories.zh.md) 中说明。
受管 group 行为在 [groups.zh.md](groups.zh.md) 中说明。
受管 user 行为在 [users.zh.md](users.zh.md) 中说明。
受管 APK repository 和 key 行为在 [apk.zh.md](apk.zh.md) 中说明。
有界 OpenRC init 生成和运行时收敛在 [openrc.zh.md](openrc.zh.md) 中说明。
Alpine hostname 和 timezone 行为在 [system.zh.md](system.zh.md) 中说明。
Alpine kernel module 和 sysctl 行为在 [kernel.zh.md](kernel.zh.md) 中说明。
Component artifact、composition 和变更 script 在 [components.zh.md](components.zh.md) 中说明。

## Product 名称

| 表面 | 值 |
| --- | --- |
| executable | `apf` |
| configuration | `*.apf.hcl` |
| default variables | `alpineform.apfvars[.json]` |
| automatic variables | `*.auto.apfvars[.json]` |
| environment variables | `APF_VAR_<name>` |
| remote state | `/var/lib/alpineform/state.json` |
| runtime lock | `/run/lock/alpineform/lock` |

变量优先级从低到高依次为 declaration default、`APF_VAR_`、default/automatic variable file、
显式 variable file，然后是命令行 `-var` 值。同一个 source class 中，后出现的 input 获胜。

## 检查

```sh
make build
make docs-check
make check
make vulncheck
git diff --check
```

`make check` 包含双语文档门禁和 Alpine 3.21-3.24 libvirt matrix 的静态 layout gate。
文档门禁要求每个发生变更的维护中 Markdown 文件同步更新其对应版本，并检查同语言导航、
已跟踪结构、围栏内容、链接和技术字面量。
matrix 保持 12 个 case 与四个 branch 交叉组合（48 个 job）。其中阻塞性的 `components` case 覆盖 binary、file、archive
和 CA-certificate artifact；这是它们 Beta 状态的运行时证据边界，而每个 instance 的 source-expression
语法仍是增量 alpha。现有的四 branch `openrc` case 还验证 package -> managed configuration ->
service dependency lifecycle，不增加 matrix cardinality。专用的四 branch `source-build` Preview
case 在每个 Alpine 版本上包含 48 条显式 assertion，覆盖 legacy default、instance 优先于
profile/host candidate、受限 `/var/tmp`、cached no-op、下一次 rebuild 选择、cleanup、failure
preservation 和 sandbox/protected-input 边界。Compiler test 覆盖剩余的优先级分支。Source build
保持 Preview。运行 `make ALPINE_BRANCH=v3.21 test-integration` 可在一个 branch 上执行全部真实 VM
case；运行 `make ALPINE_BRANCH=v3.21 test-integration-case CASE=<name>` 可执行一个 case。固定 image、
lifecycle、case contract、remote-libvirt 设置、diagnostic 和 cleanup 行为记录在
[integration runbook](../test/integration/libvirt/README.zh.md)中。

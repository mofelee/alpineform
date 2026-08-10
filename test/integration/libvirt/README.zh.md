<p align="right"><a href="README.md">English</a> | <strong>简体中文</strong></p>

# Alpine 3.21-3.24 libvirt 集成测试

阻塞性的受管目标 gate 在每个受支持 branch 上运行 12 个 case，共启动 48 台全新的 persistent
x86_64 VM。runner 固定使用以下不可变的官方 image：

| Branch | Image | SHA-512 前缀 |
| --- | --- | --- |
| v3.21 | `generic_alpine-3.21.7-x86_64-uefi-cloudinit-r0.qcow2` | `612691a05c8e` |
| v3.22 | `generic_alpine-3.22.5-x86_64-uefi-cloudinit-r0.qcow2` | `132c8f0f3926` |
| v3.23 | `generic_alpine-3.23.5-x86_64-uefi-cloudinit-r0.qcow2` | `7f8818009bb8` |
| v3.24 | `generic_alpine-3.24.1-x86_64-uefi-cloudinit-r0.qcow2` | `ed976ef40de1` |

版本、架构、image name、source URL 和完整 checksum 固定在 `alpine-target.sh` 中。runner 在接受
download 或 cached image 前，会对照固定 checksum 检查 Alpine 发布的 sidecar。

## 生命周期

每个 case 获得 overlay disk、NoCloud seed、生成的 root SSH key、隔离 NAT network，以及名称以
`dbf-test-alpineform-` 开头的 domain。Cloud-init 只安装该次运行的 public key，并写入 completion
marker。调用 AlpineForm 前，runner 验证 `ID=alpine`、选定的精确 patch version、APK architecture
`x86_64` 和 kernel architecture `x86_64`。它只会在临时 case copy 中，把 3.24 fixture baseline
改写为选定 branch。

每个编号配置按顺序运行以下适用的阻塞阶段。只有该配置定义了 drift hook 时，才执行 drift
注入和修复：

1. validate 和离线 plan；
2. 在线 plan 和经过审查的 `apply --auto-approve`；
3. 经过断言的 JSON no-op plan 和干净 `check`；
4. case 特定的远端 assertion；
5. 如果已定义，则执行带外 drift、非零 `check`、repair、no-op 和干净 `check`；
6. 已配置的 VM reboot、干净 `check` 和 persistence assertion。

之后的编号配置覆盖 removal 语义。APK case 在显式 `ensure = "absent"` 前验证 declaration removal
仅执行 forget。OpenRC case 使用 authored package -> managed configuration -> service 链。在所有
四个 branch 上，它验证 dependency-first 初始收敛、JSON no-op 和干净 check、drift detection 和
repair，以及 dependent-first 显式 cleanup。然后验证默认 forget policy 下移除 declaration 不会执行
远端删除。仅 package 变更绝不会激活 service operation；restart/reload 仍与实际匹配的受管 init/conf
变更绑定。Plan assertion 区分 effective ordering、structural trigger 和 active trigger；state assertion
要求仅 authored 的 v3 metadata、reference pruning，且 forget-only orphan 不得伪造 relationship。
这扩展了现有 case，而不是添加第十三个 case。
Docker case 验证 package-version evidence、candidate preflight、protected value、invalid-daemon
isolation、daemon crash recovery、partial/degraded drift repair、全新 running/stopped reboot
persistence、project forget/adopt、保留 volume 的 scoped destroy、显式 absence，以及
service/package removal ordering。
Components case 使用三个编号配置，同时仍是 12 个阻塞 case 之一。它保留 literal-source
兼容性，并通过挂载 input 提供受保护的 binary、file、archive 和 CA-certificate source 表达式。
单 host sequence 在不同配置中先使用 mirror A，再使用 byte-equivalent mirror B。同时双 host
resolution 仍由 [compiler](../../../internal/core/merge/components_test.go) 和
[graph](../../../internal/core/graph/artifact_source_workflow_test.go) 契约测试覆盖。在所有四个 branch
上，VM case 验证 wrong-checksum preservation 加已脱敏 debug output、no-op、四种 installation 的
drift 和 repair、保守的 cache-loss repair、reboot persistence、精确 teardown，以及 reboot 后
absence。
Nftables case 是第十个阻塞 case。其 `.allow-network-disruption` marker 只允许该 case 添加单独的
apply authorization；layout validator 会拒绝该 marker 出现在其他任何位置。该 case 通过 41 条
显式 assertion，覆盖安全 create/update/delete、JSON no-op、drift 和 repair、三次 reboot、
invalid syntax 和未 mutation 的 approval refusal、external ownership、真实 SSH loss、本地
`SIGKILL`、detached 和 synchronous confirmed rollback、state preservation、stale-artifact
cleanup，以及 protected-log scanning。
Source-build case 是第十一个 case 和专用 Preview gate。它在每个 Alpine branch 上恰好通过 48 条
显式 assertion。四个编号配置会在 legacy default root 下对 checksummed C fixture 进行 musl 编译，
验证 profile/host/instance root 优先级是完整 no-op，通过 instance root rebuild 大于故意限制为
2 MiB 的 `/var/tmp` 的 workspace，并保留现有 source 和 build-definition drift 覆盖。Failure loop
覆盖 checksum、compiler、missing-output、symlink-output、cancellation、selected-root ENOSPC 和
legacy owned-leftover recovery，同时要求先前 installation 和 protected state 保持存活。它还检查
每个 candidate root 和 `/run` input path 上的 private ownership、有界 capacity diagnostic 和
cleanup。
Component-moved case 是第十二个 case，并有自己的 Preview gate。它从旧 worker 和 source-builder
component instance 开始。二者共同覆盖 file、account、package、OpenRC、预构建 artifact、script
和 source build。独立只读 rename-only plan/check 要求 18 个精确 mapping、18 个 no-op 资源、零
mutation action，以及 byte-identical state 和 remote identity snapshot。编号 rename/update plan
包含 `move=18`、`update=2` 和 `no_op=16`：一个 file update 及其 script trigger，不发生 service
restart 或 source rebuild。之后的阶段要求 retained-block no-op、通过 legacy owner 执行且
`move=0` 的 source-input rebuild、带正常 drift repair 的 removed-block no-op，以及最终 component
cleanup。该 case 会拒绝重复 artifact cache、script marker、owner package、dependency/install
marker、workspace 或 output ownership。
Account 和 lifecycle case 验证 recorded destroy ordering。Layout validator 要求连续 config、每个
step 的 check hook、每个 case 至少一个 drift hook、固定的离线 facts、shell syntax、仅 nftables
可用的 risk marker，并且不得提交 key 或 state。Case 可以提交 `expected-assertions` 文件，使其精确
运行时 assertion 数量成为阻塞条件。

CI 恰好发现 12 个 case，并将它们与四个 Alpine branch 交叉组合。聚合
`Alpine 3.21-3.24 core gate` 要求完整 48-job matrix。独立的 nftables、source-build 和
component-moved Preview gate 防止这些 Preview schema 在没有四 branch 运行时覆盖时通过。

## 本地运行

无需启动 VM 即可验证 layout：

```sh
make test-integration-layout
```

针对本地 `qemu:///system` 运行全部 case 或一个 case：

```sh
make test-integration
make test-integration-case CASE=files-directories-secrets
make test-integration-case CASE=nftables
make test-integration-case CASE=component-moved
make ALPINE_BRANCH=v3.21 test-integration-case CASE=facts-state-lock
```

runner 还支持远端 libvirt。VM 文件必须位于 hypervisor storage pool 上，因此创建 overlay 前会把
verified image 同步到那里：

```sh
APF_LIBVIRT_URI=qemu+ssh://ks/system \
APF_INTEGRATION_HYPERVISOR=ks \
APF_INTEGRATION_POOL=vm \
make ALPINE_BRANCH=v3.21 test-integration-case CASE=facts-state-lock
```

有用的环境变量：

| 变量 | 用途 |
| --- | --- |
| `APF_INTEGRATION_ALPINE_BRANCH` | 选择 `v3.21`、`v3.22`、`v3.23` 或 `v3.24`；默认为 `v3.24`。 |
| `APF_INTEGRATION_CASE` | 运行一个已发现的 case。 |
| `APF_INTEGRATION_IMAGE_CACHE` | cache 经过 checksum 验证的官方 image。 |
| `APF_INTEGRATION_ARTIFACT_DIR` | 存储已脱敏的 failure diagnostic。 |
| `APF_INTEGRATION_KEEP_WORKDIR=1` | 保留 controller 工作文件以便 debug。 |
| `APF_INTEGRATION_DISABLE_KVM=1` | 强制使用 QEMU software emulation。 |
| `APF_LIBVIRT_URI` | 选择本地或远端 libvirt。 |
| `APF_INTEGRATION_HYPERVISOR` | 拥有远端 libvirt 文件的 SSH host。 |
| `APF_INTEGRATION_POOL` | 远端 storage pool，默认为 `vm`。 |
| `APF_INTEGRATION_REMOTE_BASE_IMAGE` | hypervisor 端经过验证的 base image path。 |

## 诊断和清理

失败时，runner 保存 domain XML、serial console、guest status 和 AlpineForm command log。Public-key
material 会被脱敏，包含 private key 的 scenario copy 绝不会上传。Sensitive fixture value 会在 case
通过前从 log 中扫描清除。

Exit、failure、interruption 和 cancellation 都运行同一个 cleanup trap。它只 destroy 并 undefine
精确生成的 domain 和 network，移除精确的 overlay、seed、console log 和 helper directory，并移除
controller work directory，除非请求保留。共享的 checksum-verified base image 作为 cache 保留。
Component-moved case 还会在 disposable VM 自身 teardown 前，移除其受管远端 object、state、lock、
build workspace、artifact cache、source-build marker 和 script marker。

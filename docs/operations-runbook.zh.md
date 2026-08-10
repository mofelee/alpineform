<p align="right"><a href="operations-runbook.md">English</a> | <strong>简体中文</strong></p>

# 运维手册

## Apply 前

1. 在审查或 CI 中运行 `apf validate` 和离线 plan。
2. 使用 `ssh` 独立确认 root SSH host-key 和 identity policy。
3. 运行在线 `apf plan`，仔细审查 destructive、authoritative 和 adoption action。
4. 升级或进行高风险变更前，备份 `/var/lib/alpineform/state.json`。
5. 保留之前的 `apf` binary 和配置，以便 alpha rollback。

## State 备份和恢复

在目标上、没有 apply 正在运行时执行：

```sh
install -m 0600 /var/lib/alpineform/state.json \
  /var/lib/alpineform/state.json.backup
```

只能恢复来自同一 host、且 schema 能被所选 binary 理解的 state 文件。先停止并发自动化，保留
mode `0600`，并使用原子替换。恢复 state 不会撤销目标端 mutation；之后应立即运行 `apf plan`。

当前 state schema 是 v3。其 binary 读取 schema v1 和 v2，并在内存中规范化任一版本；但下一次
state 写入会持久化 v3，而 schema-v1 和 schema-v2 binary 会拒绝 v3。首次使用 schema-v3 binary
执行会写 state 的 apply 前，备份每个所选 host 当前的 v1 或 v2 state，并保留匹配的旧配置和 binary。
在线 plan/check 是只读的，不会跨过该边界。

要在 v3 已写入后降级，应停止 apply 自动化，以原子方式恢复该 host 精确的 v1 或 v2 备份，并使用
匹配的旧配置和 binary。恢复 state 不会逆转备份后执行的远端 action，因此在批准 reconciliation
前应审查在线 plan。如果没有兼容备份，请继续使用支持 schema v3 的 binary；不支持手动编辑
`schema_version`、资源地址、`depends_on` 或 `component_identities`。Schema v2 过去引入
`component_identities`；v3 保留它们并添加 authored resource dependency metadata。

## 重命名 Component 实例

当挂载的 component instance label 发生变化、但远端 object 不变时，使用声明式 move：

```hcl
moved {
  from = component.legacy_worker
  to   = component.worker
}
```

采用以下 rollout 顺序：

1. 在同一次配置变更中重命名挂载的 component 并添加 `moved` block。运行 `apf validate` 和离线
   plan，无需读取远端 state 即可捕获静态 endpoint、duplicate、chain 和 mount 错误。
2. 每个 rollout 目标首次执行会写 schema v3 的 apply 前，备份其 state。
3. 为所选 host 运行在线 plan。确认 `moves` 下完整的新旧地址，验证 `summary.move`，并单独审查
   任何真实资源 action 或 trigger。
4. Apply 已审查的 locked plan。AlpineForm 在任何 provider mutation 前，原子提交该 host 的 move。
5. 保留 block，再次运行在线 plan 和 `apf check`。要求不存在待处理 move 或意外资源 action。
6. 对每台相关 host 重复这些步骤。host 可以分批迁移；仍然只挂载 source 的 host 保持不变。
7. 仅当所有 host state 中都不再有 source prefix 且存在 destination prefix 后，才移除 block。
   移除后，最终在线 plan 和 check 必须仍然干净。

不要因为第一台 host 或第一批 host 已经干净就移除 block。仍携带 source state 的 host 会失去迁移
instruction，继而可能 plan destination creation 加 source forget/destroy 行为。当 host selection
或不同配置 branch 分阶段 rollout 时，这一点尤其重要。

如果 move prewrite 失败，先前的原子 state 文件保持有效，并且不会开始 provider mutation；修正
state backend 问题后保留 block 并重试。如果后续 provider action 失败，move 可能已经提交。
保留 block，重新运行在线 plan 和正常 apply；已迁移 host 不会实现 move。多 host 失败可能留下
已迁移和待处理 host 的安全混合，因为写入在每台 host 内具有原子性。不要手动编辑 state，也不要
添加 reverse move 作为失败恢复手段。

## Lock 恢复

租约位于 `/run/lock/alpineform/lock`。正常退出、错误或取消会释放它；重启会删除它。如果获取操作
报告 busy，应识别并停止竞争的 apply，而不是删除有效租约。过期租约由下一位竞争者原子接管。
只有确认没有 owner process 或自动化正在运行后，才能手动删除目录。

## Apply 失败

AlpineForm 只在 provider sequence 完成后才持久化成功的资源 state。失败时：

1. 保留 error 和结构性 debug event，但不要发布原始目标 state 或 secret。
2. 重新运行 `apf plan`，检查目标实际的部分状态。
3. 修正目标 dependency、配置、传输或权限问题。
4. 重新运行 `apf apply`；provider 被设计为幂等收敛。
5. 在关闭 incident 前，要求 JSON no-op plan 和干净的 `apf check`。

使用 `apf apply --debug` 获取结构性 fact、state、lock、inspect、operation、apply 和 cleanup event。
Debug 不包含命令 stdin/output 或受保护值。

## 更改或移除依赖链

资源 `depends_on` 只能用于同一 scope 内的 `packages.package`、`files.file` 和运行时
`services.service` declaration。应分别审查 plan `depends_on` 与 `triggered_by`：dependency
变更不会激活 OpenRC restart/reload 或 `on_change` script。

对于 package -> managed configuration -> OpenRC service 工作流：

1. 添加或更改 typed relationship，运行 `apf validate`，并审查在线 plan。正向 apply 必须按
   package、file、service 的顺序执行。
2. 确认只有匹配的受管 init 或 conf file 实际发生变化时，OpenRC `operation` 才会激活。
   仅 package 变更必须让 service operation 的 `changes[].triggered_by` 保持为空。
3. 对于有意的远端 cleanup，在表达受支持的远端 intent 时保留 declaration 和 relationship：
   stop/disable service、将受管 file 标记为 absent，并将 package 标记为 absent。Locked plan
   必须先安排 dependent service 工作，然后删除 file，最后删除 package。
4. Apply，然后在移除 declaration 前要求 no-op plan 和干净 check。之后移除 declaration 是默认
   forget，不得执行远端工作。

如果支持 recorded destroy 行为的资源已经成为 orphan，state v3 会使用其中持久化的 authored
relationship，按相同的 dependent-first 顺序 teardown。`prevent_destroy` 仍会阻止受保护资源。
不要手动编辑 state 以添加、移除或重新排序 dependency。修正目标端原因后，通过正常 plan/apply
重试失败的 cleanup。

## 轮换每个 Instance 的 Artifact Source

预构建 component mount 可以通过 typed input 提供 `source.url` 和 `source.sha256`。轮换期间应
保留 retained physical component identity（如果逻辑 mount label 变化则使用 `moved`）和规范化
source label。受保护 cache 使用该稳定 identity 作为 key，而不是受保护 URL 或 checksum。

1. 在受保护配置 source 中更改挂载 input 值。不要把 token、checksum 或其 digest 放入 component
   label、source label、path 或其他公开 identity 字段。
2. 运行 `apf validate`。未挂载 template 只验证静态结构；在 source 表达式求值前会规范化并验证
   挂载 input。
3. 使用声明的 platform facts 运行离线 plan 以审查 source selection，然后运行在线 plan 以审查
   根据观测 facts 作出的选择。受保护变更保持脱敏，因此应审查受影响的稳定地址和 action。
4. Apply locked plan。Download 或 checksum 失败会保留先前已验证 cache 和 installation；修正
   source 并正常重试。
5. 轮换后要求 JSON no-op plan 和干净的 `apf check`。

不要仅仅为了诊断受保护 source 而发布配置副本、plan、state、debug log、远端 cache metadata 或
failure artifact。求值后，AlpineForm 只在 controller 内存和已脱敏 provider stdin 中携带已解析
URL 与 checksum；序列化表面只包含安全的结构、status 和 lifecycle metadata。

## 配置 Source-Build Workspace

使用 profile 或 host `staging.root` 作为 fleet 或 host default；只有异常 build 才使用已挂载 source
component 的 `staging_root`。优先级依次为 instance、有效 host/profile，然后是
`/var/tmp/alpineform/builds`。该路径是仅运行时 placement，并有意从 plan、graph、state、HTML 和
常规 debug event 中排除，因此应直接审查选定配置 source。下述有界 failure diagnostic 是刻意设置的
例外。

Rollout 前，验证每个现有 path boundary 都由 root 所有且不是 symlink。选定 root 不得允许 group
或 world 写入。支持由 root 所有、带 sticky bit 的可写 ancestor，例如 `/var/tmp`。AlpineForm
会以私有方式创建缺失 root；operator 也可以预先创建共享、只读且可 traverse 的 root，而不更改
安全的现有 mode：

```sh
install -d -o root -g root -m 0755 /srv/alpineform-builds
```

运行 `apf validate`，然后在 apply 前运行在线 plan/check。如果已验证 output cache 有效，只更改
root 是 no-op：它不会 rebuild、reinstall 或触发 `on_change`。不要仅为测试 placement 而使 cache
失效。下一次因真实 build-identity 变更而必须执行的 rebuild 使用
`<root>/<64-hex-build-identity>`，并在 output 验证后清理该路径。

失败时，保留完整的有界 diagnostic：

```text
staging_root=<selected-root> work_path=<identity-workspace> available_kib=<number|unknown>
```

Capacity 值来自对选定 root 或最近现有 ancestor 执行的 `df -Pk`。修正 ownership、mode、capacity
或 compiler/input 失败，然后重试正常 apply。Cleanup 失败本身就是 apply 失败。不要仅凭路径删除旧
root workspace：AlpineForm 只会在检查 root ownership、非 symlink boundary、私有 workspace mode
以及精确 owner/build identity 后，删除其 marker 记录的 root/path pair。Mismatch 会刻意保留路径
以供调查。

受保护 input file 保持位于 `/run/alpineform/build-inputs` 下；更改磁盘 workspace 绝不会移动它们。
已验证 output cache 和 dependency marker 也保持固定位置。预构建 archive extraction 不受影响，
继续在 install destination 旁 staging，使其最终 replacement 保持在同一 filesystem 上。

## Source-Build 失败恢复

Preview source build 会保留先前 installation，直至 input staging、compilation、output verification、
dependency cleanup 和 destination staging 全部成功。build 失败或取消后，重新运行 `apf plan`；
受管的 `.alpineform-build-*` virtual package、dependency marker、workspace 和 verified-output
marker 都是确定性的，下一次 apply 可以协调它们。

活动命令还有私有的 `/run/alpineform/build-runtime/<owner-id>/process` marker。稳定 supervisor
在启动 Bubblewrap 前发布该 marker，并保持 process-group 和 session leader，直至每个 live member
退出。AlpineForm 会在终止 group 前验证其 generation、PID、process group、session、start time、
owner、identity 和 workspace；绝不会向没有 leader 的 numeric group 发送 signal。位于
`/run/alpineform/build-runtime-locks` 下的每 owner lock 会串行化 publication、retry 和 cleanup，
并作为非 secret、重启即消失的 lock file 保留。不要编辑这些路径或使用宽泛的 `pkill` 恢复；
mismatch 会保留为 cleanup failure，从而绝不会向无关的复用 PID 发送 signal。

不要对 compiler/header package 逐个运行 `apk del`。手动干预前，确认 virtual package 和 marker
属于彼此：

```sh
virtual=.alpineform-build-0123456789abcdef01234567
marker=/var/lib/alpineform/builds/0123456789abcdef0123456789abcdef.dependencies
test -f "$marker"
test "$(stat -c '%u:%a' "$marker")" = 0:600
test "$(sed -n '1p' "$marker")" = "$virtual"
apk info -e "$virtual"
```

当前 dependency marker 有五行：virtual package、owner ID、build identity、selected root 和 exact
workspace。Legacy 三行 marker 只包含前三个字段，并且只授权
`/var/tmp/alpineform/builds/<build-identity>`。任何其他行数、owner/mode、root/path tuple 或
symbolic-link boundary 都是 collision；不要编辑 marker 使其通过。

首选正常 `apf apply`，它只移除该 virtual package，并让 APK 保留仍在 world 中或被其他地方依赖的
package。如果 marker/virtual owner 与 plan 不匹配，应停止操作：这是 ownership collision，而不是
要删除的 stale data。脱敏前，绝不要发布 marker、workspace、output cache、state、build
stdin/environment 或 failure diagnostic。启用网络的 build 和未经检查的 replacement input 都不是
恢复选项。

## nftables 批准和恢复

每个 live nftables create、update、repair 或 recorded delete 都标记为
`risk: network disruption`。审查精确的 `(family, name)` table，确认带外目标访问，并有意传递
`--allow-network-disruption`。交互式 plan approval 与 `--auto-approve` 是独立决定，不隐含该
授权。

CLI 只报告有界结果：confirmed、activation failure with no rollback required、rollback confirmed、
rollback pending 或 rollback failed。要在目标上检查持久化结果而不打印其受保护 token digest，
只读取固定 table status file 的第二行：

```sh
family=inet
table=alpineform_filter
status=/var/lib/alpineform/nftables/recovery/$family-$table.status
test -f "$status" && sed -n '2p' "$status"
```

对于 `pending` 或 `rollback_pending`，停止新的自动化，并至少等待声明的 `rollback_timeout`。
Detached watchdog 可能仍然拥有 live transaction。不要删除、重命名、复制或修改
`/run/alpineform/nftables` 下的任何内容，也不要重启 nftables service。通过原始管理路径重新连接，
再次读取第二行，并在运行 `apf plan` 和 `apf check` 前要求 `rollback_confirmed`。

对于 `rollback_failed`，使用带外 console，保持自动化停止，并保留仅 root 可访问的 transaction
directory 和 recovery file。先修正报告的目标端原因，例如 filesystem 已满、不安全的 target type
或失败的 `nft` 命令。如果恰好保留一项失败 transaction，且记录的 watchdog process 已不再存活，
经过验证的 watchdog 可以重试同一 scope 的 snapshot restoration，而不泄露 token：

```sh
family=inet
table=alpineform_filter
status=/var/lib/alpineform/nftables/recovery/$family-$table.status
set -- /run/alpineform/nftables/*
[ "$#" -eq 1 ] && [ -d "$1" ] || exit 1
transaction=$1
[ "$(stat -c '%u:%g:%a' "$transaction")" = 0:0:700 ] || exit 1
[ "$(stat -c '%u:%g:%a' "$transaction/watchdog.sh")" = 0:0:700 ] || exit 1
[ "$(sed -n '1p' "$transaction/status")" = rollback_failed ] || exit 1
pid=$(sed -n '1p' "$transaction/watchdog.pid")
start=$(sed -n '1p' "$transaction/watchdog.start")
[ -n "$pid" ] && [ -n "$start" ] || exit 1
case "$pid:$start" in *[!0-9:]*) exit 1 ;; esac
if [ -r "/proc/$pid/stat" ] &&
  [ "$(awk '{print $22}' "/proc/$pid/stat")" = "$start" ]; then
  exit 1
fi
(cd "$transaction" && sh ./watchdog.sh)
test "$(sed -n '2p' "$status")" = rollback_confirmed
```

重试会在接触声明的 table 前，重新验证 token-scoped path、family/name identity、snapshot metadata
和 action lock。如果仍然失败，保留所有 artifact 以便进行受保护的 incident analysis。绝不要使用
`nft flush ruleset`，绝不要发布 transaction directory 或 recovery file，也绝不要仅仅为了让之后
的 apply 继续而移除失败 artifact。确认恢复后，在恢复自动化前验证 named table、其专用
persistence、external table、`apf plan` 和 `apf check`。

## Drift 和外部 Manager

遇到 drift 和未 apply 的 intent 时，`apf check` 以非零状态退出。不要让相互竞争的 manager 管理
相同 path、account、package、service 或 sysctl。Managed APK ownership 保留 external line；
authoritative ownership 替换整个 repository file，必须按此审查。

对于 Docker drift，直接在目标上检查 `rc-service docker status`、`docker info` 和声明 project 的
`docker compose ps --all` 输出。不要发布 `.env`、Compose content、daemon configuration 或
container environment。Docker-invalid daemon candidate 绝不会替换当前文件；Compose-invalid
candidate 绝不会调用 `up`、`stop` 或 `down`。修正 candidate，重新运行正常 plan/apply/check
sequence。如果 declaration 已被 forgotten，应重新引入它以 adopt/repair 观测到的 project，然后再
请求显式 `state = "absent"` 或 `on_remove = "destroy"`。带有 write-only content 的 forgotten
project 会被 repair，而不是盲目 adopt，因为 state 丢失后无法比较其远端 secret。

## 卸载

移除 control-host binary 不会改变目标。使用以下命令将其移除：

```sh
rm -f /usr/local/bin/apf
rm -rf /usr/local/share/alpineform
```

删除目标 state 前，应显式收敛期望的 stop、disable、absence 或 recorded destroy 行为。移除
declaration 通常会 forget ownership，并有意保留目标 object。审查最终 plan 后，只有当 AlpineForm
不再管理该 host 时，才手动移除目标 metadata：

```sh
rm -rf /var/lib/alpineform /run/lock/alpineform
```

## 验证 Release

下载一个 archive、`checksums.txt` 和 Sigstore bundle，然后运行：

```sh
sha256sum --check --ignore-missing checksums.txt
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp \
    'https://github.com/mofelee/alpineform/.github/workflows/release.yml@refs/tags/v.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
gh attestation verify apf_<tag>_linux_amd64.tar.gz \
  --repo mofelee/alpineform
```

每个 archive 还有匹配的 `.sbom.spdx.json` asset。

<p align="right"><a href="nftables.md">English</a> | <strong>简体中文</strong></p>

# nftables 管理

nftables domain 为 Preview。它只管理显式声明的命名 table。AlpineForm 不提供整个
ruleset 的所有权，并且在收敛或删除 table 时绝不会使用全局 flush。

其 rollback watchdog、重新连接确认、独立的网络中断审批以及阻塞式 Alpine VM case
均已完整实现。它仍处于 Preview，是因为实时防火墙收敛属于高影响的 alpha 接口；
整个 ruleset 的所有权仍不受支持。

## 命名 table 契约

```hcl
host "edge" {
  nftables {
    table "alpineform_filter" {
      family = "inet"
      content = <<-NFT
        chain input {
          type filter hook input priority 0; policy accept;
          ct state established,related accept
        }
      NFT

      rollback_timeout = "30s"
      adopt_existing    = false
      on_remove         = "forget"

      lifecycle {
        prevent_destroy = true
      }
    }
  }
}
```

table 标签就是远端 nftables table 名称。`family` 默认为 `inet`；支持的 family 为
`arp`、`bridge`、`inet`、`ip`、`ip6` 和 `netdev`。身份是 `(family, name)` 这一对，
因此两个 family 中的同名 table 具有两个不同的资源地址。

`content` 是 table body，而不是完整的 nftables 文件。AlpineForm 提供外层
`table <family> <name> { ... }` 包装。括号不平衡、`include`、嵌套 `table` 声明以及
顶层 nft command verb 都会在远端访问前被拒绝。这会阻止 DSL 表达全局 flush 或修改
第二个 table。

rules content 是受保护的 provider payload。它不会序列化到 IR、graph、text plan、
JSON plan、HTML plan、state 或 debug event 中。state 可以保留 desired digest、
family/name 身份、专用持久化路径以及删除行为。临时 content 要求
`content_version`；其 content hash 和字节数不会保留。

## 所有权和生命周期

已声明 `(family, name)` 身份之外的 table 属于外部资源，在 create、update、repair、
rollback 和 delete 过程中必须保持不变。

已有的已声明 table 不会被静默 adopt。首次 apply 会失败，除非该 table 已记录为
AlpineForm 所有，或 `adopt_existing = true` 显式授权接管并收敛它。adoption 绝不会
授予任何其他 table 的所有权。

移除声明时默认为 `on_remove = "forget"`：AlpineForm 只移除其 state 记录。
`on_remove = "delete"` 要求在以后移除声明时删除已记录的 owned table。
`ensure = "absent"` 是显式 delete 请求。两种 delete 路径都只针对已记录的
family/name 和专用持久化文件，并且都遵守 `lifecycle.prevent_destroy`。它们不会卸载
nftables、disable OpenRC、移除外部配置或 flush ruleset。

## 稳定地址

对于主机 `edge` 上的 table `inet/alpineform_filter`，公开资源地址为：

```text
host.edge.nftables.table["inet/alpineform_filter"]
```

事务协议通过附加以下内容派生稳定的内部地址：

```text
.persistence
.transaction.candidate
.transaction.active
.transaction.watchdog
.transaction.confirmation
```

package 和 OpenRC 集成地址为 `host.edge.packages.package["nftables"]` 和
`host.edge.nftables.service`。运行时 token 和 snapshot 从不属于这些地址，也绝不能
序列化。

## Alpine package 和 OpenRC 布局

AlpineForm 的阻塞式集成证据覆盖 Alpine 3.21 至 3.24 x86_64。各 branch 的 package
revision 不同。安装官方 `nftables` world intent 时也会安装 `nftables-openrc`；这些
package 拥有 `/etc/nftables.nft`、`/etc/conf.d/nftables` 和
`/etc/init.d/nftables`。

原生 `/etc/nftables.nft` 以 `flush ruleset` 开头，原生 OpenRC service 会在
start/reload 时加载该文件，并在 stop 时运行 `nft flush ruleset`。因此 AlpineForm
绝不会 start、reload、stop、rewrite 或 adopt 原生 service 及其配置。这些路径上的
已有文件会作为逐字节不变的外部配置保留。

AlpineForm 安装独立的 `/etc/init.d/alpineform-nftables` service，并将 root 所有、
模式为 `0600` 的 table 文件存储在 root 所有、模式为 `0700` 的目录
`/etc/nftables.d/alpineform` 下。目录和文件目标都会拒绝符号链接及不匹配的文件类型。
更新会先在目标目录中创建临时文件，再进行原子重命名。

专用 service 会在 `default` runlevel 中 enable，但在
`/var/lib/alpineform/nftables/armed` 存在之前有意不执行任何操作。只有实时激活通过
preflight、snapshot、watchdog、reconnect 和 confirmation 后，transaction 和 watchdog
loop 才会创建该 marker。在此之前，start 和 reboot 都无法激活仅持久化的 content。
其 stop 操作绝不会 flush 或 delete 任何 active table。

Loop 2 Alpine VM matrix 通过 61 项显式断言，证明显式 package 安装、精确文件
adoption、no-op、persistence 和 init 漂移检测/修复、已记录 table 删除、声明 forget、
外部配置保留，以及三次 reboot。由于 arming marker 不存在，owned table 保持 inactive；
后续 loop 添加并独立证明了实时 activation、confirmation、approval 和 rollback。

## 激活事务

每次 create、update、repair 或已记录 delete 都使用 `/run/alpineform/nftables` 下一个
token-scoped 运行时目录。该目录由 root 所有，模式为 `0700`；candidate、active
snapshot、persistent snapshot、marker snapshot、activation 和 restore 文件模式均为
`0600`。随机 token 和每个 snapshot 都只属于 provider 数据，绝不会放入 graph、plan、
state、debug event 或 error。

对于 present table，AlpineForm 会将 table-body DSL 渲染为一个完整的
`table <family> <name> { ... }` candidate。随后它会：

1. 针对完整替换 batch 运行 `nft -c -f`；
2. 在不跟随符号链接的情况下，捕获之前的无状态 active table，以及精确的 persistent、
   observed marker 和 arming-marker 字节；
3. 在存在 active snapshot 时验证 active restore batch；
4. 启动一个 detached、token-scoped watchdog，并验证它存活；
5. 再次执行 preflight，并通过一个 nft batch 仅原子替换命名 table；
6. 通过原始配置的管理路径创建新的 runner 和 SSH process；
7. 确认 active digest 和专用 OpenRC service，然后原子写入并重新检查 persistence、
   active/fingerprint marker 和 arming marker；
8. 使用不可预测的 token 对 confirmation 进行身份验证，并让 watchdog 移除 transaction
   目录。

delete 使用相同协议，其 batch 只命名已记录的 owned table。在 create、update、repair、
rollback 或 delete 中都不存在 ruleset-wide flush。

watchdog 在标准流关闭的独立 session 下运行，因此在报告 ready 后不依赖发起操作的
SSH process、本地 `apf` process 或 Go context。token-scoped action lock 将新鲜
confirmation 与超时 rollback 串行化。HUP、INT、TERM、准备失败、confirmation 失败或
超时都会恢复 active、persistent、observed 和 arming snapshot。confirmation 成功会
移除 token 目录。只有新鲜 confirmation 和最终 inspection 成功返回后，AlpineForm 才
写入 state。

重复或过期的 confirmation 会被拒绝，因为其 token 目录已不再 active。新 transaction
只清理已完成的 confirmation/rollback artifact，并拒绝与 active、pending 或 failed
transaction 重叠。如果 rollback 本身失败，root-only transaction 目录、已验证 snapshot、
action status 和受限的 `rollback_failed` marker 会留在目标上供恢复，而不会声称成功。
runtime token 名称、snapshot content 和 rule content 不会出现在 plan、state、graph、
HTML、debug、error 或上传的 diagnostics 中。

Loop 3 Alpine 3.24.1 VM matrix 通过了 40 项断言。它证明无效 nft 语法和不安全 snapshot
目标不会产生变更；create/no-op 和 active/persistent 组合漂移修复能够收敛；外部 table
和配置会在每次非 reboot 操作中存续；已记录 delete 具有明确范围；模拟未来 confirmation
marker 时 reboot content 有效；成功或 rollback 的 transaction 不会留下 runtime token
artifact。该 matrix 是下面独立 Loop 4 测试之前的 pre-watchdog 基线。

Loop 4 Alpine 3.24.1 VM 测试通过了 14 项显式断言。它建立一个已确认 table，验证
fresh-confirm 清理，应用一个会中断 SSH 的 table，观察管理路径失败，以 SIGKILL 终止
本地 `apf` process，并等待 detached 的 10 秒 watchdog。无需发起进程，SSH 即恢复；
之前的 active table、persistence、observed 和 arming marker、外部 table、原生配置及
state hash 均得以保留；token artifact 被移除；受保护日志不包含 rule content；reboot
后恢复最后一次确认的 table。

Loop 5 Alpine 3.24.1 VM 测试通过了 20 项显式断言。它证明普通 approval 和
`--auto-approve` 都不能授权实时防火墙变更，locked replan 不能引入该风险，并且有界
reconnect 会在实际 SSH 中断后报告已确认 rollback。它还验证 reboot、state 保留、
持久所有权恢复和已完成 artifact 清理。

阻塞式 [`nftables` case](../test/integration/libvirt/cases/nftables) 会在每个受支持的
x86_64 branch 上运行 41 项显式断言。它覆盖安全 create、update、JSON no-op、check、
三向 drift、repair、限定范围的 delete、三次 reboot、无变更的无效语法、无变更的
approval 拒绝、外部 table/service/configuration 保留、真实 SSH 中断、本地 `SIGKILL`、
独立 rollback、同步的 confirmed-rollback 报告、stale-artifact 清理、state 保留以及
受保护日志扫描。CI 通过专用 nftables Preview gate 强制运行此 case。

## 审批和运维结果

owned table 的每次 create、update、已记录 delete 或 destroy 都会标记确定性的
`network_disruption` 风险。text plan 会打印 `risk: network disruption`；plan JSON 会
添加 `changes[].risks` 和 `summary.network_disruption`，且不会泄露 rule content。
adopt、forget 和 no-op 操作不属于实时激活，也不携带该风险。

只要 preview 或 locked host replan 包含此类步骤，`apf apply` 就要求
`--allow-network-disruption`。为普通 plan review 输入 `yes` 或传递 `--auto-approve`
都不会授予这项独立授权。如果 locked replan 引入该风险，apply 会在 provider 或 state
写入之前停止。

fresh confirmation 在 table 的 rollback timeout 内使用有界的两秒连接尝试并进行
指数 backoff。一旦该 timeout 到期，AlpineForm 就会停止尝试 confirmation，并仅在有界
grace period 内查询 root-only、token-digest-bound 的 recovery result。context
cancellation 会立即停止本地重试，而 detached watchdog 仍保持 armed。CLI 结果区分：

- 通过已配置管理路径确认成功；
- confirmation 前激活失败，无需 rollback；
- 管理路径失败，且已确认 rollback；
- 管理路径失败、rollback pending，watchdog 仍保持 armed；
- rollback 失败，需要在目标端恢复。

只有这些有界状态字符串会跨越受保护的错误边界。SSH stderr、transaction token、token
digest、snapshot 和 rule 保持隐藏。如果 confirmation 已完成，但本地 state 写入被中断，
有效的 root-only observed marker 会在下次运行时证明先前的 AlpineForm 所有权，因此可以
重建 state，而无需削弱针对外部 table 的 adoption 检查。pending 和 failed rollback 的
处理方式记录于[运维运行手册](operations-runbook.zh.md)。

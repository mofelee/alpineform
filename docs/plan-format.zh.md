<p align="right"><a href="plan-format.md">English</a> | <strong>简体中文</strong></p>

# Plan 格式

AlpineForm 离线和在线 plan 使用
`format_version = "alpineform.plan.alpha1"`。

JSON document 包含：

- `mode`：结构性 desired-state plan 使用 `offline`，观测 action plan 使用 `online`。
- `command.files`：按有效 input 顺序排列的配置 source。
- `hosts`：排序后的已编译 host 名称。
- `summary`：move/create/update/adopt/delete/destroy/forget/no-op 数量、受管资源数量、graph node
  数量，以及计划进行 live firewall 激活或删除时的增量 `network_disruption` 数量。某个 mode
  未使用的 action 和 risk 保持为零或被省略。
- `moves`：在线 plan 中排序后的已实现 state-address mapping。离线 plan 没有要迁移的 state，
  因此输出空 array。
- `graph`：稳定地址、kind、managed status、dependency 和 trigger relationship，以及源码位置。
  它绝不包含 desired value。
- `changes`：由 provider 支持的受管变更。在线 document 包含完整 action model：`create`、
  `update`、`adopt`、`delete`、`destroy`、`forget` 和 `no-op`。受保护的 desired content
  仅表示为 `{ "protected": true }`；observed value 和内部 fingerprint 不会序列化。对于
  nftables create、update、delete 或 destroy action，增量 `risks` array 包含
  `network_disruption`；adopt、forget 和 no-op 不携带该 risk。

Host、platform 和 component metadata 是 `managed = false` 的结构性 graph node；它们可供
审计，但并不意味着目标端 action。该格式有意省略 wall-clock timestamp。当 input 和参数顺序不变时，
重复的离线 plan 在字节级稳定；在线 plan identity 忽略 fact detection time，同时保留所有语义 facts。

## Component Artifact Input

每个挂载 instance 的 `source.url` 和 `source.sha256` 表达式不会添加 plan 字段，也不会更改
`alpineform.plan.alpha1`。在 source selection 和 artifact graph 编译之前，会规范化 input 并对
表达式求值。离线 plan 从声明的 platform facts 中选择；在线 plan 从观测到的 facts 中选择。

公开 literal source 保留其现有地址和 desired rendering。对于受保护的已解析 URL 或 checksum，
`graph` 省略原始 payload，同时保留 address、kind、managed status、源码位置和 relationship。
`changes` 保留安全 summary 和 relationship，并使用现有的 `{ "protected": true }` desired
表示。原始值、受保护值派生的 cache key、内部 provider payload 和观测到的受保护材料绝不会序列化
到 text、JSON 或 HTML。隐藏的受保护 intent 不会序列化，但会参与用于 preview 与 locked plan
比较的内存 plan fingerprint；因此，更改它需要重新审查 locked plan。

## Move

每个在线 move 都有三个必需 string：

```json
{
  "host": "edge",
  "from": "host.edge.component.legacy_worker.files.file[\"/etc/worker.conf\"]",
  "to": "host.edge.component.worker.files.file[\"/etc/worker.conf\"]"
}
```

`host` 标识 state owner。`from` 和 `to` 是迁移前后的完整 graph address。条目依次按 `host`、
`from`、`to` 排序；`summary.move` 恰好等于条目数。一个 component-root block 可以实现多个条目，
因为该 root 下每个持久化资源都单独移动。已完成迁移的 host 不会实现任何条目。

Move 更改 state identity，不是远端资源 action。Move 条目本身不会增加 create、update、adopt、
delete、destroy、forget 或 no-op 数量，也不能隐含远端 rename 或 trigger。同一次 rollout 中真实的
desired-state 变更仍是 `changes` 中的独立条目，具有其正常 action 和 relationship。

Text 输出将每个 mapping 渲染为 `-> <from>`，下一行是 `to: <to>`。HTML 使用独立 Moves table。
JSON 保留每个已实现的 leaf mapping。这些表示只包含 host 和资源地址，绝不包含 desired 或 observed
payload、component input、provider result 或 state metadata。仍然正常应用 JSON encoding 和 HTML
escaping。

## 关系

`graph[]` node 和 `changes[]` 条目都可以包含增量 `depends_on` 和 `triggered_by` array。每个
array 只包含稳定资源地址，按字典顺序排序并去重。`alpineform.plan.alpha1` consumer 必须继续忽略
未知字段。

对于当前 desired graph 资源，plan `depends_on` 是完整的有效顺序集合：结构性 graph parent、
推断的 provider prerequisite 和 authored resource `depends_on` edge 会被合并、排序和去重。
Plan 不会暴露独立的 `explicit_depends_on` 字段。Dependency 发生变化本身并不意味着 dependent
资源被触发。`triggered_by` 记录独立的 change-trigger edge，它们可以激活 OpenRC restart 或共享
`on_change` script 等 operation。

当前 desired 资源的离线 graph node 和 change 会显示其完整 relationship。在在线 plan 中，当前
资源的 `graph[].triggered_by` 仍显示完整结构性 trigger 集合，而 `changes[].triggered_by` 只包含
其计划变更已激活该 operation 的地址。当前 graph 资源的在线 `changes[].depends_on` 仍显示完整
有效顺序集合。

仅存在于 state 的 orphan 没有当前 desired graph relationship 可供渲染，因此其 `graph[]` 和
`changes[]` 条目不会伪造 `depends_on`。Prior-state authored metadata 仍控制 dependent-first
orphan 执行；consumer 通过确定性的 `changes[]` 顺序观察这一点，而不是通过合成 relationship 字段。

State 有意采用更窄的契约：每个资源的 `depends_on` 只保留目标资源仍被跟踪的 authored dependency。
推断和结构性 plan 顺序以及所有 trigger relationship 都从配置重新计算。请参阅
[作者声明的 state 依赖](state-backend.zh.md#作者声明的资源依赖)。

Text 和 HTML plan 使用独立的 `depends_on:` 与 `triggered_by:` label 投影相同字段。
Relationship rendering 绝不会把地址展开为 desired payload、command、sensitive 或 ephemeral
value 或 provider detail。相同的仅地址边界也适用于 `moves`；受保护值不能出现在 move 条目或其
summary 中。

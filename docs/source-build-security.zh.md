<p align="right"><a href="source-build-security.md">English</a> | <strong>简体中文</strong></p>

# 目标端 Source-Build 安全

目标端 component build 是 Preview capability。它们以 root 身份在受管 Alpine host 上执行，因此
经过审查的 build definition 和每个声明 input 与 AlpineForm 配置位于同一个信任边界内。

## 契约和 Identity

Source component 使用 `type = "source"`、一个 `build` block 和一个 `install` block。每个
build 至少有一个具名 input，带精确 SHA-256 和固定的 workspace-relative destination。Input 来自
controller-local regular file、inline content，或不含嵌入 credential 的 HTTP(S) transport locator。
Checksum，而不是 URL 或 branch name，才是 content identity。可选 `extract` block 只接受
`format = "tar.gz"` 和有界的 `strip_components`。Archive listing 和 extracted output 都会拒绝
absolute 或 parent path、link、special file、不安全名称、空结果、过多 entry，以及 stripping 后
发生 collision 的 path。

Command 是重复的 `command` block，其 `argv` 值为非空 string array。AlpineForm 绝不接受 shell
command string，也绝不把 argv 插值到远端 shell source。Working directory、input destination 和
唯一声明 output 都是干净 relative path。首个 Preview 契约固定 `network = "none"`；不支持未声明
download 和启用网络的 build。

`bubblewrap` 是受管 build-dependency virtual package 的自动且可见成员。每个 command 都获得新的
PID、IPC、UTS、cgroup、mount 和 network namespace，并丢弃所有 capability。Workspace 是唯一
persistent writable bind；`/tmp` 是私有 tmpfs。`/bin`、`/sbin`、`/lib` 和 `/usr` 是只读
toolchain mount。Host `/etc`、`/run`、`/var`、state、SSH material、cache 和 install destination
不会被 mount。Command output 被丢弃，core dump 被禁用，取消时 shell 会终止受管 process group。

Build identity 覆盖已解析 component instance、input identity、argv、protected-value version、
确定性 environment policy、目标 platform、APK dependency、output policy 和 install metadata。
Graph 为 input staging、dependency ownership、workspace execution、output verification、cleanup
和 installation 使用独立稳定地址。

Workspace placement 有意不包含在该 identity 中。Profile/host `staging.root` 和 instance
`staging_root` 只选择下一次必须 build 的执行位置，fallback 为 `/var/tmp/alpineform/builds`。
已解析路径不会序列化到 IR、graph、plan、state、HTML 或常规 debug event。有界 workspace-failure
diagnostic 可以标识 selected root 和 work path。只更改 root 时，只要有效 verified output cache
仍存在，就保持 no-op；它不能 reinstall output 或激活 `on_change`。

Output policy 可以限制 byte、要求精确 SHA-256，并要求声明 output 可执行。Owner、group 和 mode
来自 `install`。AlpineForm 会在 installation 前拒绝 missing、ambiguous/globbed、linked、
parent-linked、special、oversized、checksum-invalid 或 non-executable output。

## 工作区放置与所有权

配置只接受不含 control character、干净、绝对且非 root 的 path，并拒绝 sensitive 或 ephemeral
root。在创建或移除任何内容前，目标 provider 会验证每个现有 boundary 都由 root 所有且不是
symlink。选定 root 不得允许 group 或 world 写入。只有由 root 所有并带 sticky bit 的 writable
ancestor 才会被接受；这样允许 `/var/tmp` 下的常规 path，而不会接受不安全 root。

缺失的 root directory 使用 `umask 077` 创建。安全的现有 root 不会被不必要地重新设置 mode，
因此 `0755` 等 mode 保持不变。实际 workspace 恰好是 `<root>/<64-hex-build-identity>`；它及其
`build` child 必须是 root 所有且 mode `0700` 的 directory。Workspace 包含由 root 所有、mode
`0600` 的 marker，将 owner ID、build identity、selected root 和 exact workspace path 绑定在一起。
Symlink、owner 或 mode 变化、malformed marker 或 tuple mismatch 都是 ownership failure，而不是
删除权限。

Dependency marker 保持位于 `/var/lib/alpineform/builds` 下，mode 为 `0600`。新 marker 包含五行：
virtual package、owner ID、build identity、selected root 和 exact workspace。Legacy 三行 marker
只解释为 `/var/tmp/alpineform/builds` 下匹配的 workspace；它不能授权清理其他 root 下的内容。
当必须 rebuild 且 root 发生变化时，AlpineForm 只有在所有 path、ownership、mode 和 marker 检查
通过后才移除记录的旧 workspace。Marker 最后移除，因此 cleanup failure 保持可重试。

受保护 inline input file 保持位于 `/run/alpineform/build-inputs` 下；每 command 的受保护
environment/stdin manifest 保持位于 `/run/alpineform/build-runtime/<owner-id>` 下。该私有 mode
`0700` directory 包含 mode `0600` 的 process marker，将 owner、build identity、workspace、
runtime generation、process group 和 Linux process start time 绑定在一起。只要 Bubblewrap 或
任何 live group member 仍存在，supervisor 就保持经过认证的 group/session leader。Cancellation
和 retry 通过 `/run/alpineform/build-runtime-locks` 中每 owner、mode `0600` 的 lock 串行执行，
验证 record，并使用有界 TERM/KILL teardown。它们会拒绝 leaderless group、PID reuse、marker
tampering 和变化的 runtime generation。Lock file 不包含受保护值，只保留在重启即消失的 storage
上。这两个 runtime path 都不会移动到 configurable workspace root 下。Persistent dependency
marker 和 verified output cache 也保持其固定 state/cache location。预构建 archive 仍是独立的
provider path，并继续在 install destination 旁 staging，以便在同一 filesystem 上原子替换。

## 受保护值

受保护的 inline input、environment value 和 command stdin 需要公开 version string。它们的 byte
保留在 provider payload 和已脱敏 SSH stdin 中，不会出现在 graph、plan、state、HTML、debug
event、error 和 command output 中。受保护值绝不会放入远端 shell source 或远端 command argument。
Build stdout 和 stderr 会被省略，而不是当作安全 diagnostic channel。

## Ownership 和失败行为

声明的 APK build dependency 属于一个由地址派生的 `.alpineform-build-*` virtual package 和仅
root 可访问的 ownership marker。Cleanup 只能移除该精确的受管 virtual package。APK 会保留仍在
world 中或被其他 package 依赖的 package。

稳定 owner ID 从 component 资源地址派生，与变化的 build identity 分离。Mutation 前，AlpineForm
检查 virtual package、`/etc/apk/world`、每个声明的 installed package 和 ownership marker。
匹配的 leftover marker/package 可以在中断后恢复；属于其他 owner 的 virtual package 或 marker
是硬 collision。失败或取消的 dependency installation 只移除受管 virtual package 和 marker。

Input 在 dependency installation 前验证。Command 在确定性 environment 和 network namespace 中
运行。Output verification 会在 AlpineForm cache 中 stage 一个 regular、non-symlink file。Download、
dependency、command、missing-output、checksum、oversize 或 cleanup failure 都不会修改先前
installation。只有最终 provider phase 才把 verified cache 复制到 destination filesystem，并以原子
方式替换目标。

Success、primary failure、cancellation 和 interrupted-build recovery 都会运行受管 cleanup。仅
cleanup 失败会使 apply 失败；当 primary operation 和 cleanup 都失败时，报告的 error 会保留两个
cause。Workspace failure 只添加以下形式的有界 placement diagnostic：
`staging_root=<path> work_path=<path> available_kib=<number|unknown>`，绝不包含受保护 input content。

Declaration removal 默认为 `on_remove = "forget"`。显式 `on_remove = "destroy"` 只记录
AlpineForm 所有的 cache、marker、virtual package 和 installation identity。
`lifecycle.prevent_destroy` 在 provider 执行前阻止这些 recorded destructive action。带有匹配
AlpineForm build 和 output marker 的目标可以被 adopt；未标记目标绝不会在未明确声明的情况下被认领。

Verified output 被复制到 destination filesystem 中的临时文件，再次检查，设置最终
owner/group/mode，并使用 no-follow `mv -T` 替换。Build-definition change 报告为 rebuild；
installed digest 或 metadata drift 报告为 repair。显式 destroy 会重新检查 recorded marker 和
content digest，并拒绝删除 linked、unowned 或 drifted target。

## 威胁边界

- 不可信 source 可以利用 compiler、linker、build tool 或 kernel。Network isolation 会降低影响
  范围，但不会让 root compilation 变得安全。
- Path traversal、symlink、special file、重复 extracted path 和 archive expansion 都要求 provider
  在使用前验证。
- Cancellation 必须终止受管 process group，并运行有界 cleanup。确定性 ownership marker 让下一次
  apply 可以恢复 leftover。
- Output capture、disk use、output size 和 workspace lifetime 都有界；resource exhaustion 仍可能
  使 host 不可用。
- Verified output 仍是不可信 executable content。AlpineForm 证明其来自声明 input 和 command，
  而不证明语义安全。

当 source 或 toolchain 并非完全可信时，operator 应使用专用 build host。不要把 Preview source
build 当作可复现、隔离 release pipeline 的替代品。

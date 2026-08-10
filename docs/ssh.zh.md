<p align="right"><a href="ssh.md">English</a> | <strong>简体中文</strong></p>

# Root SSH 传输

AlpineForm v0.1 仅通过 root OpenSSH session 管理目标。host 可以声明 SSH alias/address、
显式 port 和 identity file。runner 始终传递 `-l root`，即使 alias 在 SSH 配置中使用其他
user；运行命令前还会拒绝 DSL 中的非 root 值。

OpenSSH 配置中的 alias、proxy jump、host key 和其他客户端行为仍然生效。AlpineForm 额外启用
batch mode、禁用 forwarding，并使用有界 connect timeout。远端 script 作为一个经过 shell
引用的命令传递；state 或资源 payload 使用 stdin。

当目标 alias 必须与用户默认配置隔离时，将 `APF_SSH_CONFIG` 设置为显式 OpenSSH 配置文件。
AlpineForm 通过 `ssh -F` 传递该路径；所选文件中的 alias、proxy jump 和 known-host policy
仍然生效。

命令可以将 stdin 和 output 标记为需要 debug 脱敏。受保护的失败会省略远端 stderr，普通诊断
则保留有界的 stderr 摘要。context cancellation 会终止本地 OpenSSH 进程。

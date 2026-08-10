<p align="right"><a href="facts.md">English</a> | <strong>简体中文</strong></p>

# Alpine 目标 facts

AlpineForm 通过三条只读命令发现目标身份：

```text
cat /etc/os-release
apk --print-arch
uname -m
```

v0.1 契约仅接受 `ID=alpine`、`v3.21` 至 `v3.24` 的 release branch，以及原生 APK
架构 `x86_64` 或 `aarch64`。公开架构值规范化为 `amd64` 和 `arm64`；`libc` 推导为
`musl`。AlpineForm state 会持久化精确版本、branch、原生 APK 架构、kernel 架构和检测时间，
将它们作为 facts。

显式的 `platform.architecture` 和 `platform.version` 是断言。它们与检测到的 facts 不匹配时，
会在使用任何 state、lock 或资源 writer 之前失败。branch、libc 和原生架构始终为只读值。

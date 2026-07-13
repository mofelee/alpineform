# Alpine target facts

AlpineForm discovers target identity through three read-only commands:

```text
cat /etc/os-release
apk --print-arch
uname -m
```

The v0.1 contract accepts only `ID=alpine`, release branch `v3.24`, and native
APK architectures `x86_64` or `aarch64`. Public architecture values normalize
to `amd64` and `arm64`; `libc` is derived as `musl`. Exact version, branch,
native APK architecture, kernel architecture, and detection time are persisted
as facts in AlpineForm state.

Explicit `platform.architecture` and `platform.version` are assertions. A
mismatch with detected facts fails before a state, lock, or resource writer is
used. Branch, libc, and native architecture remain read-only.

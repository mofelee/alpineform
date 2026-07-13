# Alpine system settings

Host labels identify AlpineForm hosts and do not implicitly manage the target
hostname. Declare system settings explicitly:

```hcl
host "edge" {
  system {
    hostname = "edge.example"
    timezone = "Asia/Shanghai"
  }
}
```

`hostname` manages both the runtime hostname and the root-owned
`/etc/hostname` file. Values must be RFC 1123 hostnames. AlpineForm does not
rewrite `/etc/hosts`.

`timezone` must be a relative zoneinfo name without empty, current, or parent
path segments. AlpineForm installs and tracks an explicit `tzdata` APK world
intent, verifies that the selected zone resolves inside
`/usr/share/zoneinfo`, atomically manages `/etc/localtime` as a symlink, and
writes `/etc/timezone`.

If `tzdata` is already declared present, the timezone resource reuses it. An
explicit absent `tzdata` declaration conflicts with timezone management and is
rejected during validation. Removing hostname or timezone stops management and
forgets the corresponding state; it does not reset the remote system or remove
the synthesized package. Package removal still requires an explicit
`packages.package "tzdata" { ensure = "absent" }` declaration after timezone
management is removed.

Alpine uses musl and does not provide the glibc locale model. `system.locale`
is rejected during parsing instead of exposing a non-working setting.

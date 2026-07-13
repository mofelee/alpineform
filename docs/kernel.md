# Alpine kernel settings

Kernel resources are host-level, present-oriented declarations:

```hcl
host "router" {
  kernel {
    module "br_netfilter" {}

    sysctl "net.bridge.bridge-nf-call-iptables" {
      value = "1"
    }

    sysctl "net.ipv4.ip_forward" {
      value         = "1"
      apply_runtime = false
    }
  }
}
```

`module` loads the named module with `modprobe` and atomically persists it in
`/etc/modules-load.d/alpineform-<name>.conf`. Inspection distinguishes loaded,
built-in, available, and missing modules. Built-in modules satisfy runtime
state but still receive a persistence file. Automatic absence and unload are
not part of v0.1: `ensure = "absent"` is rejected, and removing a declaration
only forgets state without calling `modprobe -r` or deleting persistence.

Each `sysctl` owns one collision-resistant
`/etc/sysctl.d/99-alpineform-*.conf` file. `value` is required and
`apply_runtime` defaults to `true`. Every sysctl depends on the host's declared
modules. When one or more runtime-enabled settings change, AlpineForm writes
all persistence files first and then applies the runtime values through one
aggregated command. A no-op runs no runtime command.

Removing a sysctl declaration deletes only its AlpineForm-owned persistence
file and does not reset the current kernel value. Set a replacement runtime
value explicitly before removal when the old value must not remain active.
External sysctl files are never scanned, rewritten, or treated as owned.

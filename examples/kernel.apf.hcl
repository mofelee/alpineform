host "alpine_kernel" {
  ssh {
    host = "alpine-kernel"
  }

  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  kernel {
    module "br_netfilter" {}

    sysctl "net.bridge.bridge-nf-call-iptables" {
      value = "1"
    }

    sysctl "net.ipv4.ip_forward" {
      value = "1"
    }
  }
}

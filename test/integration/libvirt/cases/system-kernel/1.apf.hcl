host "cihost" {
  ssh {
    host          = "__APF_VM_HOST__"
    identity_file = "${path.module}/id_ed25519"
  }

  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  system {
    hostname = "apf-ci.alpineform.test"
    timezone = "Asia/Shanghai"
  }

  kernel {
    module "loop" {}

    sysctl "net.ipv4.ip_forward" {
      value = "1"
    }
  }
}

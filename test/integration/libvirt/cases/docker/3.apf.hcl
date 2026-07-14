host "cihost" {
  ssh {
    host          = "__APF_VM_HOST__"
    identity_file = "${path.module}/id_ed25519"
  }
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }
  users {
    user "operator" {}
  }
  docker {
    members = ["operator"]
    daemon_config = jsonencode({
      log-driver = "json-file"
      log-opts = {
        max-size = "5m"
        max-file = "2"
      }
    })
  }
}

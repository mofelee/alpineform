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
    ensure  = "absent"
    enable  = false
    members = ["operator"]
    project "smoke" {
      directory = "/opt/alpineform-docker/smoke"
      compose   = "services: {smoke: {image: alpine:3.24}}\n"
      state     = "absent"
    }
  }
}

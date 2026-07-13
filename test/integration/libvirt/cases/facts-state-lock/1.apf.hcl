host "cihost" {
  ssh {
    host          = "__APF_VM_HOST__"
    identity_file = "${path.module}/id_ed25519"
  }

  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  files {
    file "/etc/alpineform-ci-facts" {
      content   = "alpine=3.24.1\narchitecture=amd64\n"
      on_remove = "destroy"
    }
  }
}

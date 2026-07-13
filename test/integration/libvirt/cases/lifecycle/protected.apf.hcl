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
    file "/tmp/alpineform-protected-delete" {
      ensure = "absent"
      lifecycle {
        prevent_destroy = true
      }
    }
  }
}

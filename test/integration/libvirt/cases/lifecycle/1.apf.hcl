host "cihost" {
  ssh {
    host          = "__APF_VM_HOST__"
    identity_file = "${path.module}/id_ed25519"
  }

  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  directories {
    directory "/srv/alpineform-lifecycle" {
      recursive_delete = true
      on_remove        = "destroy"
    }
  }

  files {
    file "/srv/alpineform-lifecycle/managed.txt" {
      content   = "managed\n"
      on_remove = "destroy"
    }
  }
}

host "cihost" {
  ssh {
    host          = "__APF_VM_HOST__"
    identity_file = "${path.module}/id_ed25519"
  }

  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  groups {
    group "apfci" {
      gid       = 2300
      system    = true
      on_remove = "destroy"
    }
  }

  users {
    user "apfci" {
      uid       = 2300
      group     = "apfci"
      groups    = ["wheel"]
      home      = "/home/apfci"
      shell     = "/bin/sh"
      system    = true
      on_remove = "destroy"
      ssh_authorized_keys = [
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAaCeDgwFMdvRLHkB+Muja0bVQu1dxcrqB8tdD3o08Wl alpineform-integration-fixture",
      ]
    }
  }
}

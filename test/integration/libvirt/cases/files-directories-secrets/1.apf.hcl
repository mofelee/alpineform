variable "token" {
  type      = string
  default   = "alpineform-ci-secret-sentinel"
  sensitive = true
  ephemeral = true
}

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
    directory "/etc/alpineform-ci" {
      mode             = "0750"
      recursive_delete = true
      on_remove        = "destroy"
    }
  }

  files {
    file "/etc/alpineform-ci/app.conf" {
      content   = "enabled=true\n"
      mode      = "0640"
      on_remove = "destroy"
    }

    file "/etc/alpineform-ci/token" {
      content         = var.token
      content_version = "integration-v1"
      sensitive       = true
      mode            = "0600"
      on_remove       = "destroy"
    }
  }
}

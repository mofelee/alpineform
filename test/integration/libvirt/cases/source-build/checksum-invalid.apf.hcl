variable "build_token" {
  type      = string
  default   = "alpineform-ci-secret-sentinel"
  sensitive = true
  ephemeral = true
}
component "failure" {
  type = "source"
  input "token" {
    type      = string
    sensitive = true
    ephemeral = true
  }
  build {
    input "source" {
      source      = "fixtures/tool-v1.c"
      sha256      = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
      destination = "tool.c"
    }
    command { argv = ["false"] }
    environment         = { BUILD_TOKEN = input.token }
    environment_version = "failure-v1"
    output              = "build/tool"
    dependencies        = ["build-base"]
  }
  install { path = "/usr/local/bin/apf-ci-source-tool" }
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
  component "failure" {
    source = component.failure
    inputs = { token = var.build_token }
  }
}

variable "build_token" {
  type      = string
  default   = "alpineform-ci-secret-sentinel"
  sensitive = true
  ephemeral = true
}

component "musl_tool" {
  type    = "source"
  version = "1"

  input "token" {
    type      = string
    sensitive = true
    ephemeral = true
  }

  build {
    input "source" {
      source      = "fixtures/tool-v1.c"
      sha256      = "3764b3a8b3b7a021738231ecc9310011da67487f41d6f1732a02d53b6ef903e6"
      destination = "tool.c"
    }
    input "verify_environment" {
      source      = "fixtures/verify-env.sh"
      sha256      = "734fc94faf2e2dcb43d63d205b44641c21576976b0564a3a7d80f970e9acd77f"
      destination = "verify-env.sh"
    }
    command { argv = ["sh", "verify-env.sh"] }
    command { argv = ["cc", "-Os", "-static", "-o", "build/tool", "tool.c"] }

    environment         = { BUILD_TOKEN = input.token }
    environment_version = "integration-secret-v1"
    output              = "build/tool"
    executable          = true
    dependencies        = ["build-base", "zlib-dev"]
    network             = "none"
  }

  install {
    path  = "/usr/local/bin/apf-ci-source-tool"
    owner = "root"
    group = "root"
    mode  = "0755"
  }
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

  component "musl_tool" {
    source = component.musl_tool
    inputs = { token = var.build_token }
  }
}

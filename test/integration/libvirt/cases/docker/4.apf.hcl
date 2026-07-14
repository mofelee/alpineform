variable "docker_env" {
  type      = string
  default   = "APF_SECRET=alpineform-ci-secret-sentinel\n"
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
    project "smoke" {
      directory = "/opt/alpineform-docker/smoke"
      compose = <<-YAML
        services:
          smoke:
            image: alpine:3.24
            restart: unless-stopped
            command: ["sh", "-c", "trap 'exit 0' TERM INT; while true; do sleep 60; done"]
            environment:
              APF_SECRET: "$${APF_SECRET}"
      YAML
      env         = var.docker_env
      env_version = "integration-v1"
      state       = "absent"
    }
  }
}

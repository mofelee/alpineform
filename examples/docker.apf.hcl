variable "app_env" {
  type      = string
  default   = "APP_MODE=example\n"
  sensitive = true
  ephemeral = true
}

host "alpine_docker" {
  ssh {
    host = "alpine-docker"
  }

  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  docker {
    daemon_config = jsonencode({
      log-driver = "json-file"
      log-opts = {
        max-size = "10m"
        max-file = "3"
      }
    })

    project "example" {
      directory = "/srv/alpineform-example"
      compose = <<-YAML
        services:
          app:
            image: alpine:3.24
            restart: unless-stopped
            command: ["sleep", "infinity"]
      YAML
      env         = var.app_env
      env_version = "example-v1"
      state       = "running"
    }
  }
}

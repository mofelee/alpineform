variable "environment" {
  type     = string
  default  = "dev"
  nullable = false
}

locals {
  app_port = 8080
}

assert {
  condition     = length(var.environment) >= 2
  error_message = "environment must contain at least two characters"
}

script "reload_app" {
  description = "Reserved reload hook metadata; execution is not implemented yet."
}

component "web_app" {
  description = "Typed application component metadata."

  input "port" {
    type     = number
    nullable = false

    validation {
      condition     = input.port >= 1 && input.port <= 65535
      error_message = "port must be between 1 and 65535"
    }
  }

  input "token" {
    type      = string
    default   = "example-token"
    sensitive = true
    ephemeral = true
  }
}

profile "base" {
  component "web" {
    source = component.web_app
    inputs = {
      port = local.app_port
    }

    lifecycle {
      prevent_destroy = true
    }
  }
}

host "alpine_1" {
  imports = [profile.base]

  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  assert {
    condition     = self.platform.branch == "3.24" && self.platform.libc == "musl"
    error_message = "this example requires Alpine 3.24 with musl"
  }
}

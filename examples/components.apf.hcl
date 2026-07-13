script "refresh_worker" {
  commands = [["rc-service", "example-worker", "reload"]]
}

component "example_worker" {
  type    = "binary"
  version = "1.0.0"

  input "port" {
    type    = number
    default = 9000
  }

  source "amd64" {
    url    = "https://downloads.example.invalid/example-worker-1.0.0-linux-amd64"
    sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  }

  source "arm64" {
    url    = "https://downloads.example.invalid/example-worker-1.0.0-linux-arm64"
    sha256 = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
  }

  install {
    path      = "/usr/local/bin/example-worker"
    on_change = global.script.refresh_worker
  }

  files {
    file "/etc/example-worker.conf" {
      content   = "PORT=${input.port}\n"
      on_change = global.script.refresh_worker
    }
  }

  openrc {
    service "example-worker" {
      command            = "/usr/local/bin/example-worker"
      command_args       = ["--config", "/etc/example-worker.conf"]
      command_background = true
      pidfile            = "/run/example-worker.pid"
    }
  }

  services {
    service "example-worker" {
      enabled   = true
      runlevel  = "default"
      state     = "running"
      operation = "restarted"
    }
  }
}

host "component_example" {
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  component "worker" {
    source = component.example_worker
    inputs = { port = 9100 }
  }
}

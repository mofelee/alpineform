host "alpine_openrc" {
  ssh {
    host = "alpine-openrc"
  }

  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  openrc {
    service "example-worker" {
      description        = "Example background worker"
      command            = "/usr/local/bin/example-worker"
      command_args       = ["--listen", "127.0.0.1:9000"]
      command_user       = "nobody"
      directory          = "/var/empty"
      command_background = true
      pidfile            = "/run/example-worker.pid"
      need               = ["net"]
      use                = ["logger"]
      conf               = "WORKERS=2\n"
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

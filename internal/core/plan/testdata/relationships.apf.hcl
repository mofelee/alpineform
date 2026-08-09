host "node" {
  openrc {
    service "worker" {
      command = "/usr/local/bin/worker"
      conf    = "WORKERS=2\n"
    }
  }

  packages {
    package "worker-daemon" {}
  }

  services {
    service "worker" {
      package   = "worker-daemon"
      operation = "restarted"
    }
  }
}

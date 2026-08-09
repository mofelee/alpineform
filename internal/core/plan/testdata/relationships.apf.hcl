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

  files {
    file "/etc/worker/worker.conf" {
      content    = "protected-worker-plan-sentinel\n"
      sensitive  = true
      depends_on = [package["worker-daemon"]]
    }
  }

  services {
    service "worker" {
      package    = "worker-daemon"
      operation  = "restarted"
      depends_on = [file["/etc/worker/worker.conf"]]
    }
  }
}

host "cihost" {
  ssh {
    host          = "__APF_VM_HOST__"
    identity_file = "${path.module}/id_ed25519"
  }

  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  openrc {
    service "apf-ci-worker" {
      description        = "AlpineForm integration worker"
      command            = "/bin/sleep"
      command_args       = ["600"]
      command_background = true
      pidfile            = "/run/apf-ci-worker.pid"
      need               = ["net"]
      conf               = "APF_CI=enabled\n"
    }
  }

  services {
    service "apf-ci-worker" {
      enabled   = true
      runlevel  = "default"
      state     = "running"
      operation = "restarted"
    }
  }
}

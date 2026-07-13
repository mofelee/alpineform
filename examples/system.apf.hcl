host "alpine_system" {
  ssh {
    host = "alpine-system"
  }

  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  system {
    hostname = "edge.example"
    timezone = "Asia/Shanghai"
  }
}

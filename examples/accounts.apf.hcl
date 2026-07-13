host "alpine_accounts" {
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  groups {
    group "example_app" {
      gid    = 1500
      system = true
    }
    group "metrics" {
      system = true
    }
  }

  users {
    user "example_app" {
      uid    = 1500
      group  = "example_app"
      groups = ["metrics"]
      home   = "/var/lib/example-app"
      shell  = "/sbin/nologin"
      system = true
    }
  }

  directories {
    directory "/var/lib/example-app" {
      owner = "example_app"
      group = "example_app"
      mode  = "0750"
    }
  }
}

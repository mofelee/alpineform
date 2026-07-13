host "alpine_files" {
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  directories {
    directory "/etc/alpineform-example" {
      owner = "root"
      group = "root"
      mode  = "0755"
    }
  }

  files {
    file "/etc/alpineform-example/app.conf" {
      content = "managed_by=alpineform\n"
      owner   = "root"
      group   = "root"
      mode    = "0644"
    }
  }
}

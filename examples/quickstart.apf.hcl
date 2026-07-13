host "alpine" {
  ssh {
    host = "alpine"
  }

  directories {
    directory "/etc/alpineform-example" {}
  }

  files {
    file "/etc/alpineform-example/managed.conf" {
      content = "managed-by=alpineform\n"
      mode    = "0644"
    }
  }
}

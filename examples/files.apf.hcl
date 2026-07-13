host "alpine_files" {
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  files {
    file "/etc/alpineform-example.conf" {
      content = "managed_by=alpineform\n"
      owner   = "root"
      group   = "root"
      mode    = "0644"
    }
  }
}

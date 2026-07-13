host "alpine_apk" {
  ssh {
    host = "alpine-apk"
  }

  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  apk {
    repository "main" {
      url = "https://dl-cdn.alpinelinux.org/alpine"
    }

    repository "community" {
      url = "https://dl-cdn.alpinelinux.org/alpine"
    }
  }

  packages {
    package "curl" {}
  }
}

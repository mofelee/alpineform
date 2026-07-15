component "musl_hello" {
  type    = "source"
  version = "1"

  build {
    input "source" {
      source      = "fixtures/source-build-tool.c"
      sha256      = "42828dad5b88e00a6220f58a941edddf4e1402e436d1c3602c36701f8a5b12ed"
      destination = "hello.c"
    }

    command { argv = ["mkdir", "-p", "build"] }
    command { argv = ["cc", "-Os", "-static", "-o", "build/hello", "hello.c"] }

    output       = "build/hello"
    executable   = true
    dependencies = ["build-base"]
    network      = "none"
  }

  install {
    path  = "/usr/local/bin/musl-hello"
    owner = "root"
    group = "root"
    mode  = "0755"
  }
}

host "alpine" {
  ssh {
    host = "alpine"
  }

  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  component "hello" {
    source = component.musl_hello
  }
}

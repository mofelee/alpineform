script "record_component_change" {
  commands = [[
    "/bin/sh",
    "-eu",
    "-c",
    "mkdir -p /var/lib/alpineform; printf '%s\n' \"$APF_TRIGGER_ADDRESSES\" > /var/lib/alpineform/component-ci-triggers; printf 'run\n' >> /var/lib/alpineform/component-ci-runs",
  ]]
  outputs = ["/var/lib/alpineform/component-ci-triggers"]
}

component "tool_fixture" {
  type    = "binary"
  version = "1"

  source "amd64" {
    url    = "http://127.0.0.1:18080/tool"
    sha256 = "6666666666666666666666666666666666666666666666666666666666666666"
  }

  source "arm64" {
    url    = "http://127.0.0.1:18080/tool-arm64"
    sha256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  }

  install {
    path      = "/usr/local/bin/apf-ci-tool"
    on_change = global.script.record_component_change
  }

  files {
    file "/etc/apf-ci-component.conf" {
      content   = "enabled=true\n"
      on_change = global.script.record_component_change
    }
  }
}

component "archive_fixture" {
  type    = "archive"
  version = "1"

  source {
    url    = "http://127.0.0.1:18080/bundle.tar.gz"
    sha256 = "7777777777777777777777777777777777777777777777777777777777777777"
  }

  extract {
    format           = "tar.gz"
    strip_components = 1
  }

  install {
    path = "/opt/apf-ci-bundle"
  }
}

component "protected_binary" {
  input "mirror" {
    type      = string
    sensitive = true
  }
  input "query" {
    type      = string
    sensitive = true
  }
  input "amd64_sha256" {
    type      = string
    ephemeral = true
  }
  input "arm64_sha256" {
    type      = string
    ephemeral = true
  }

  type    = "binary"
  version = "1"

  source "amd64" {
    url    = "${input.mirror}/tool-amd64?token=${input.query}"
    sha256 = input.amd64_sha256
  }
  source "arm64" {
    url    = "${input.mirror}/tool-arm64?token=${input.query}"
    sha256 = input.arm64_sha256
  }

  install {
    path = "/usr/local/bin/apf-protected-tool"
  }
}

component "protected_file" {
  input "mirror" {
    type      = string
    sensitive = true
  }
  input "query" {
    type      = string
    sensitive = true
  }
  input "sha256" {
    type      = string
    ephemeral = true
  }

  type    = "file"
  version = "1"

  source {
    url    = "${input.mirror}/component.conf?token=${input.query}"
    sha256 = input.sha256
  }

  install {
    path = "/etc/alpineform-protected.conf"
  }
}

component "protected_archive" {
  input "mirror" {
    type      = string
    sensitive = true
  }
  input "query" {
    type      = string
    sensitive = true
  }
  input "amd64_sha256" {
    type      = string
    ephemeral = true
  }
  input "arm64_sha256" {
    type      = string
    ephemeral = true
  }

  type    = "archive"
  version = "1"

  source "amd64" {
    url    = "${input.mirror}/bundle-amd64.tar.gz?token=${input.query}"
    sha256 = input.amd64_sha256
  }
  source "arm64" {
    url    = "${input.mirror}/bundle-arm64.tar.gz?token=${input.query}"
    sha256 = input.arm64_sha256
  }

  extract {
    format           = "tar.gz"
    strip_components = 1
  }

  install {
    path = "/opt/alpineform-protected"
  }
}

component "protected_ca" {
  input "mirror" {
    type      = string
    sensitive = true
  }
  input "query" {
    type      = string
    sensitive = true
  }
  input "sha256" {
    type      = string
    ephemeral = true
  }

  type    = "ca_certificate"
  version = "1"

  source {
    url    = "${input.mirror}/root.crt?token=${input.query}"
    sha256 = input.sha256
  }

  install {
    path = "/usr/local/share/ca-certificates/alpineform-protected-root.crt"
  }
}

host "cihost" {
  ssh {
    host          = "__APF_VM_HOST__"
    identity_file = "${path.module}/id_ed25519"
  }

  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  component "tool" {
    source = component.tool_fixture
  }

  component "archive" {
    source = component.archive_fixture
  }

  component "binary" {
    source = component.protected_binary
    inputs = {
      mirror        = "http://127.0.0.1:18080/mirror-b"
      query         = "alpineform-ci-component-query-b-sentinel"
      amd64_sha256  = "0000000000000000000000000000000000000000000000000000000000000000"
      arm64_sha256  = "1111111111111111111111111111111111111111111111111111111111111111"
    }
  }

  component "config_file" {
    source = component.protected_file
    inputs = {
      mirror = "http://127.0.0.1:18080/mirror-b"
      query  = "alpineform-ci-component-query-b-sentinel"
      sha256 = "2222222222222222222222222222222222222222222222222222222222222222"
    }
  }

  component "protected_archive" {
    source = component.protected_archive
    inputs = {
      mirror        = "http://127.0.0.1:18080/mirror-b"
      query         = "alpineform-ci-component-query-b-sentinel"
      amd64_sha256  = "3333333333333333333333333333333333333333333333333333333333333333"
      arm64_sha256  = "4444444444444444444444444444444444444444444444444444444444444444"
    }
  }

  component "root_ca" {
    source = component.protected_ca
    inputs = {
      mirror = "http://127.0.0.1:18080/mirror-b"
      query  = "alpineform-ci-component-query-b-sentinel"
      sha256 = "5555555555555555555555555555555555555555555555555555555555555555"
    }
  }
}

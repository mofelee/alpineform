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
    sha256 = "0000000000000000000000000000000000000000000000000000000000000000"
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
    sha256 = "1111111111111111111111111111111111111111111111111111111111111111"
  }

  extract {
    format           = "tar.gz"
    strip_components = 1
  }

  install {
    path = "/opt/apf-ci-bundle"
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
}

host "cihost" {
  ssh {
    host          = "__APF_VM_HOST__"
    identity_file = "${path.module}/id_ed25519"
  }

  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  packages {
    package "jq" {
      ensure = "absent"
    }
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

  files {
    file "/etc/alpineform-dependency.json" {
      ensure     = "absent"
      depends_on = [package.jq]
    }

    file "/etc/init.d/apf-ci-raw" {
      content = <<-EOT
        #!/sbin/openrc-run
        description="AlpineForm raw integration worker"
        command="/bin/sleep"
        command_args="600"
        command_background=true
        pidfile="/run/apf-ci-raw.pid"
        extra_started_commands="reload"
        description_reload="Reload raw integration worker"
        dependency_config="/etc/alpineform-dependency.json"

        verify_dependencies() {
          jq -e '.enabled == true and .revision == 1' "$dependency_config" >/dev/null
        }

        start_pre() {
          verify_dependencies
          printf '%s\n' dependencies-ready > /run/apf-ci-raw.start-pre
        }

        stop_pre() {
          verify_dependencies
          printf '%s\n' dependencies-ready > /run/apf-ci-raw.stop-pre
        }

        reload() {
          verify_dependencies
          printf '%s\n' dependencies-ready > /run/apf-ci-raw.reload
        }
      EOT
      mode = "0755"
    }
  }

  services {
    service "apf-ci-worker" {
      enabled   = true
      runlevel  = "default"
      state     = "running"
      operation = "restarted"
    }

    service "apf-ci-raw" {
      enabled    = false
      runlevel   = "default"
      state      = "stopped"
      depends_on = [file["/etc/alpineform-dependency.json"]]
    }
  }
}

moved {
  from = component.legacy_worker
  to   = component.worker
}

moved {
  from = component.legacy_builder
  to   = component.builder
}

component "worker_fixture" {
  type    = "binary"
  version = "1"

  source "amd64" {
    url    = "http://127.0.0.1:18081/worker"
    sha256 = "34f2f6e93348efd93f6cf4f422cae7ce9acb6d4d988021a0ff952423f5785817"
  }

  install {
    path  = "/usr/local/bin/apf-moved-worker"
    owner = "root"
    group = "root"
    mode  = "0755"
  }

  groups {
    group "apfmoved" {
      gid       = 2401
      system    = true
      on_remove = "destroy"
    }
  }

  users {
    user "apfmoved" {
      uid       = 2401
      group     = "apfmoved"
      home      = "/var/lib/alpineform-moved/home"
      shell     = "/sbin/nologin"
      system    = true
      on_remove = "destroy"
    }
  }

  packages {
    package "jq" {}
  }

  directories {
    directory "/etc/alpineform-moved" {
      owner            = "apfmoved"
      group            = "apfmoved"
      mode             = "0750"
      recursive_delete = true
      on_remove        = "destroy"
    }
  }

  script "reload_worker" {
    interpreter = ["/bin/sh", "-eu"]
    content     = <<-EOT
      install -d -m 0755 /var/lib/alpineform-moved
      count_file=/var/lib/alpineform-moved/reload.count
      count=0
      if [ -f "$count_file" ]; then
        count="$(cat "$count_file")"
      fi
      printf '%s\n' "$((count + 1))" > "$count_file"
      printf '%s\n' "$APF_TRIGGER_ADDRESS" > /var/lib/alpineform-moved/last-trigger
    EOT
    outputs = [
      "/var/lib/alpineform-moved/reload.count",
      "/var/lib/alpineform-moved/last-trigger",
    ]
  }

  files {
    file "/etc/alpineform-moved/worker.conf" {
      owner     = "apfmoved"
      group     = "apfmoved"
      mode      = "0640"
      content   = "revision=two\n"
      on_change = script.reload_worker
      on_remove = "destroy"
    }
  }

  openrc {
    service "apf-moved-worker" {
      command            = "/usr/local/bin/apf-moved-worker"
      command_user       = "apfmoved"
      command_background = true
      pidfile            = "/run/apf-moved-worker.pid"
      description        = "AlpineForm component-moved worker"
      need               = ["net"]
      conf               = "APF_MOVED_WORKER=enabled\n"
    }
  }

  services {
    service "apf-moved-worker" {
      enabled  = true
      runlevel = "default"
      state    = "running"
      package  = "jq"
      user     = "apfmoved"
      group    = "apfmoved"
    }
  }
}

component "builder_fixture" {
  type    = "source"
  version = "2"

  build {
    input "source" {
      source      = "fixtures/builder-v2.c"
      sha256      = "beb6f914afb6f037b39f84bf64230f06486a1432773c3a1af70103212a82cc92"
      destination = "builder.c"
    }
    input "verify_environment" {
      source      = "fixtures/verify-env.sh"
      sha256      = "2e603c628f40de312db74e6043c9512ca2e16c51176edb843c3f9ad15817999f"
      destination = "verify-env.sh"
    }
    command { argv = ["sh", "verify-env.sh"] }
    command { argv = ["cc", "-Os", "-static", "-o", "build/builder", "builder.c"] }

    output       = "build/builder"
    executable   = true
    dependencies = ["build-base"]
    network      = "none"
    on_remove    = "destroy"
  }

  install {
    path  = "/usr/local/bin/apf-moved-builder"
    owner = "root"
    group = "root"
    mode  = "0755"
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

  component "worker" {
    source = component.worker_fixture
  }

  component "builder" {
    source = component.builder_fixture
  }
}

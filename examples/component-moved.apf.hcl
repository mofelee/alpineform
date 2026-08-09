component "worker_template" {
  files {
    file "/etc/example-worker.conf" {
      content = "enabled=true\n"
    }
  }
}

# Keep this migration instruction until every managed host has been applied.
moved {
  from = component.legacy_worker
  to   = component.worker
}

host "component_move_example" {
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }

  component "worker" {
    source = component.worker_template
  }
}

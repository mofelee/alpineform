package merge

import (
	"strings"
	"testing"
)

func TestCompileServicesDefaultsAndExplicitDependencies(t *testing.T) {
	config, err := compileConfig(t, `
host "node" {
  groups {
    group "worker" {}
  }
  users {
    user "worker" {
      group = "worker"
    }
  }
  packages {
    package "worker-daemon" {}
  }
  services {
    service "worker" {
      package = "worker-daemon"
      user    = "worker"
      group   = "worker"
    }
  }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	service := program.Hosts[0].Services[0]
	if service.Name != "worker" || !service.Enabled || service.Runlevel != "default" || service.State != "running" || service.Package != "worker-daemon" || service.User != "worker" || service.Group != "worker" {
		t.Fatalf("compiled service = %#v", service)
	}
}

func TestCompileServiceInfersManagedStructuredCommandUser(t *testing.T) {
	config, err := compileConfig(t, `
host "node" {
  users {
    user "worker" {}
  }
  openrc {
    service "worker" {
      command      = "/bin/true"
      command_user = "worker"
    }
  }
  services {
    service "worker" {}
  }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	if program.Hosts[0].Services[0].User != "worker" {
		t.Fatalf("inferred service dependency = %#v", program.Hosts[0].Services[0])
	}
}

func TestCompileServiceRejectsInvalidStateAndMissingDependencies(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "state", body: `service "app" { state = "restarted" }`, wantErr: `must be "running" or "stopped"`},
		{name: "runlevel", body: `service "app" { runlevel = "bad level" }`, wantErr: "runlevel"},
		{name: "package", body: `service "app" { package = "missing" }`, wantErr: "not declared present"},
		{name: "user", body: `service "app" { user = "missing" }`, wantErr: "not declared present"},
		{name: "group", body: `service "app" { group = "missing" }`, wantErr: "not declared present"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, "host \"node\" {\n  services {\n    "+test.body+"\n  }\n}\n")
			if err == nil {
				_, err = Compile(config)
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("service validation error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

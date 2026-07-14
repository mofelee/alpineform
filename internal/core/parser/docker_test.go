package parser

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDockerDomainAndProjects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docker.apf.hcl")
	writeConfig(t, path, `
host "node" {
  docker {
    ensure                  = "present"
    enable                  = true
    package_source          = "custom"
    package_repository      = "containers"
    members                 = ["deploy"]
    daemon_config           = "{\"log-driver\":\"json-file\"}"
    daemon_config_sensitive = true

    project "app" {
      directory       = "/srv/app"
      compose         = "services: {app: {image: alpine:3.24}}\n"
      compose_version = "v1"
      env             = "TOKEN=test-only\n"
      env_version     = "v1"
      state           = "running"
      on_remove       = "destroy"
      lifecycle { prevent_destroy = true }
    }
    lifecycle { prevent_destroy = true }
  }
}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	docker := config.Hosts["node"].Docker
	if docker == nil || len(docker.Attributes) != 7 || len(docker.Projects) != 1 || !docker.Lifecycle.PreventDestroy {
		t.Fatalf("docker = %#v", docker)
	}
	project := docker.Projects[0]
	if project.Label != "app" || !project.Lifecycle.PreventDestroy || project.Source.Path != `host["node"].docker.project["app"]` {
		t.Fatalf("project = %#v", project)
	}
}

func TestParseDockerRejectsInvalidShape(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "labeled", body: `docker "bad" {}`, want: "must be an unlabeled block"},
		{name: "unknown attribute", body: `docker { apt_repository = true }`, want: "unsupported attribute"},
		{name: "unknown child", body: "docker {\n  container \"app\" {}\n}", want: "unsupported block"},
		{name: "duplicate project", body: "docker {\n  project \"app\" {}\n  project \"app\" {}\n}", want: "duplicate Docker project"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.apf.hcl")
			writeConfig(t, path, "host \"node\" {\n"+test.body+"\n}\n")
			_, err := ParseFiles([]string{path})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseFiles() error = %v, want %q", err, test.want)
			}
		})
	}
}

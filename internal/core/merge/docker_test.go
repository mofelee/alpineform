package merge

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompileDockerAddsAlpineResourcesAndProtectedProjects(t *testing.T) {
	config, err := compileConfig(t, `
variable "token" {
  type      = string
  default   = "not-a-real-docker-secret"
  sensitive = true
  ephemeral = true
}
host "node" {
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }
  users {
    user "deploy" {}
  }
  docker {
    members       = ["deploy"]
    daemon_config = "{\"log-driver\":\"json-file\",\"log-opts\":{\"max-size\":\"10m\"}}"
    project "app" {
      directory   = "/srv/app"
      compose     = "services: {app: {image: alpine:3.24}}\n"
      env         = var.token
      env_version = "secret-v1"
      state       = "running"
      on_remove   = "destroy"
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
	host := program.Hosts[0]
	if host.Docker == nil || host.Docker.PackageSource != "alpine" || len(host.Docker.Projects) != 1 {
		t.Fatalf("docker = %#v", host.Docker)
	}
	if host.APK == nil || len(host.APK.Repositories) != 1 || host.APK.Repositories[0].Component != "community" {
		t.Fatalf("apk = %#v", host.APK)
	}
	if len(host.Packages) != 2 || host.Packages[0].Name != "docker" || host.Packages[1].Name != "docker-cli-compose" {
		t.Fatalf("packages = %#v", host.Packages)
	}
	if len(host.Groups) != 1 || host.Groups[0].Name != "docker" {
		t.Fatalf("groups = %#v", host.Groups)
	}
	if got := host.Docker.DaemonConfig.Content; got != "{\n  \"log-driver\": \"json-file\",\n  \"log-opts\": {\n    \"max-size\": \"10m\"\n  }\n}\n" {
		t.Fatalf("canonical daemon config = %q", got)
	}
	project := host.Docker.Projects[0]
	if !project.Sensitive || !project.Ephemeral || !project.EnvWriteOnly || project.EnvVersion != "secret-v1" || project.EnvSHA256 != "" {
		t.Fatalf("project = %#v", project)
	}
	data, err := json.Marshal(program)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "not-a-real-docker-secret") || strings.Contains(string(data), "services:") {
		t.Fatalf("IR JSON leaked Docker payload: %s", data)
	}
}

func TestCompileDockerRejectsUnsafeContracts(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "custom without repository", body: `package_source = "custom"`, want: "required when package_source"},
		{name: "repository with alpine", body: "package_source = \"alpine\"\npackage_repository = \"custom\"", want: "supported only"},
		{name: "invalid daemon json", body: `daemon_config = "not-json"`, want: "valid JSON"},
		{name: "daemon version without config", body: `daemon_config_version = "v1"`, want: "requires daemon_config"},
		{name: "daemon sensitivity without config", body: `daemon_config_sensitive = true`, want: "requires daemon_config"},
		{name: "invalid package source", body: `package_source = "official"`, want: "must be \"alpine\", \"none\", or \"custom\""},
		{name: "unknown custom repository", body: "package_source = \"custom\"\npackage_repository = \"missing\"", want: "must reference a present tagged repository"},
		{name: "daemon config must be object", body: `daemon_config = "[]"`, want: "JSON object"},
		{name: "disabled running", body: "enable = false\nproject \"app\" {\n  directory = \"/srv/app\"\n  compose = \"services: {app: {image: alpine}}\"\n}", want: "cannot be running"},
		{name: "invalid name", body: "project \"Bad.Name\" {\n  directory = \"/srv/app\"\n  compose = \"services: {app: {image: alpine}}\"\n}", want: "must use lowercase"},
		{name: "relative directory", body: "project \"app\" {\n  directory = \"srv/app\"\n  compose = \"services: {}\"\n}", want: "clean absolute"},
		{name: "Docker config subtree", body: "project \"app\" {\n  directory = \"/etc/docker/app\"\n  compose = \"services: {}\"\n}", want: "outside /etc/docker"},
		{name: "env version without env", body: "project \"app\" {\n  directory = \"/srv/app\"\n  compose = \"services: {}\"\n  env_version = \"v1\"\n}", want: "requires env"},
		{name: "invalid project state", body: "project \"app\" {\n  directory = \"/srv/app\"\n  compose = \"services: {}\"\n  state = \"paused\"\n}", want: "must be \"running\", \"stopped\", or \"absent\""},
		{name: "invalid removal mode", body: "project \"app\" {\n  directory = \"/srv/app\"\n  compose = \"services: {}\"\n  on_remove = \"delete\"\n}", want: "must be \"forget\" or \"destroy\""},
		{name: "absent running", body: "ensure = \"absent\"\nproject \"app\" {\n  directory = \"/srv/app\"\n  compose = \"services: {app: {image: alpine}}\"\n}", want: "must use state"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, "host \"node\" {\n  platform {\n    architecture = \"amd64\"\n    version = \"3.24.1\"\n  }\n  docker {\n"+test.body+"\n  }\n}\n")
			if err == nil {
				_, err = Compile(config)
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compile() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCompileDockerRejectsOwnershipCollisions(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "generic membership",
			body: `host "node" {
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }
  users {
    user "deploy" {
      groups = ["docker"]
    }
  }
  docker { members = ["deploy"] }
}`,
			want: `membership "deploy in docker" conflicts`,
		},
		{
			name: "component daemon path",
			body: `component "agent" {
  files {
    file "/etc/docker/daemon.json" {
      content = "{}"
    }
  }
}
host "node" {
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }
  docker { daemon_config = "{}" }
  component "agent" { source = component.agent }
}`,
			want: `path "/etc/docker/daemon.json" conflicts`,
		},
		{
			name: "component project directory",
			body: `component "agent" {
  directories {
    directory "/srv/app" {}
  }
}
host "node" {
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }
  docker {
    project "app" {
      directory = "/srv/app"
      compose   = "services: {}"
    }
  }
  component "agent" { source = component.agent }
}`,
			want: `path "/srv/app" conflicts`,
		},
		{
			name: "component OpenRC service",
			body: `component "agent" {
  openrc {
    service "docker" {
      command = "/bin/true"
    }
  }
}
host "node" {
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }
  docker {}
  component "agent" { source = component.agent }
}`,
			want: `service "docker" conflicts`,
		},
		{
			name: "component membership",
			body: `component "agent" {
  users {
    user "deploy" {
      groups = ["docker"]
    }
  }
}
host "node" {
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }
  docker { members = ["deploy"] }
  component "agent" { source = component.agent }
}`,
			want: `membership "deploy in docker" conflicts`,
		},
		{
			name: "absent component member",
			body: `component "agent" {
  users {
    user "deploy" {
      ensure = "absent"
    }
  }
}
host "node" {
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }
  docker { members = ["deploy"] }
  component "agent" { source = component.agent }
}`,
			want: `Docker member "deploy" is declared absent by component "agent"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, test.body)
			if err == nil {
				_, err = Compile(config)
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compile() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCompileDockerCustomSourceReferencesTaggedAPK(t *testing.T) {
	config, err := compileConfig(t, `
host "node" {
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }
  apk {
    repository "containers" {
      url = "https://mirror.example/alpine"
      component = "community"
      tag = "containers"
    }
  }
  docker {
    package_source     = "custom"
    package_repository = "containers"
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
	if got := program.Hosts[0].Packages[0].WorldIntent; got != "docker@containers" {
		t.Fatalf("Docker world intent = %q", got)
	}
}

func TestCompileDockerNoneSourceDoesNotOwnAPKPackages(t *testing.T) {
	config, err := compileConfig(t, `
host "node" {
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }
  docker {
    package_source = "none"
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
	host := program.Hosts[0]
	if host.APK != nil || len(host.Packages) != 0 {
		t.Fatalf("package_source none mutated APK intent: apk=%#v packages=%#v", host.APK, host.Packages)
	}
	if host.Docker == nil || host.Docker.PackageSource != "none" || len(host.Groups) != 1 || host.Groups[0].Name != "docker" {
		t.Fatalf("Docker none-source domain = %#v groups=%#v", host.Docker, host.Groups)
	}
}

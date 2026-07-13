package parser

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseModelBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.apf.hcl")
	writeConfig(t, path, `
variable "environment" {
  type    = string
  default = "prod"
}

locals {
  port = 8080
}

assert {
  condition     = var.environment != ""
  error_message = "environment is required"
}

script "reload_app" {
  description = "Reserved reload hook metadata."
}

component "web_app" {
  description = "Example typed component."

  input "port" {
    type     = number
    nullable = false
    validation {
      condition     = input.port >= 1 && input.port <= 65535
      error_message = "port must be valid"
    }
  }

  input "token" {
    type      = string
    sensitive = true
    ephemeral = true
  }

  assert {
    condition     = input.port > 0
    error_message = "component port must be positive"
  }
}

profile "base" {
  assert {
    condition     = var.environment == "prod"
    error_message = "base profile requires prod"
  }

  component "web" {
    source = component.web_app
    inputs = {
      port  = local.port
      token = "test-only-token"
    }
    lifecycle {
      prevent_destroy = true
    }
  }
}

host "node_1" {
  imports = [profile.base]

  platform {
    architecture = "x86_64"
    version      = "3.24.1"
  }

  component "worker" {
    source     = component.web_app
    depends_on = [component.web]
    inputs = {
      port  = 9090
      token = "another-test-token"
    }
  }

  assert {
    condition     = self.platform.libc == "musl"
    error_message = "Alpine must use musl"
  }
}
`)

	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Asserts) != 1 || len(config.Profiles) != 1 || len(config.Components) != 1 || len(config.Hosts) != 1 || len(config.Scripts) != 1 {
		t.Fatalf("model counts: asserts=%d profiles=%d components=%d hosts=%d scripts=%d", len(config.Asserts), len(config.Profiles), len(config.Components), len(config.Hosts), len(config.Scripts))
	}
	host := config.Hosts["node_1"]
	if host.Platform == nil || host.Platform.Architecture != "amd64" || host.Platform.NativeArchitecture != "x86_64" || host.Platform.Version != "3.24.1" || host.Platform.Branch != "3.24" || host.Platform.Libc != "musl" {
		t.Fatalf("platform = %#v", host.Platform)
	}
	profileInstance := config.Profiles["base"].Components[0]
	if !profileInstance.Lifecycle.PreventDestroy || profileInstance.Template != "web_app" {
		t.Fatalf("profile component = %#v", profileInstance)
	}
	if input := config.Components["web_app"].Inputs["token"]; !input.Sensitive || !input.Ephemeral {
		t.Fatalf("token input = %#v", input)
	}
}

func TestParseModelRejectsInvalidLabelsAndReadOnlyPlatformFacts(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "invalid label", content: `host "bad-name" {}`, want: "must match"},
		{name: "branch", content: "host \"node\" {\n  platform {\n    branch = \"3.24\"\n  }\n}\n", want: "read-only derived fact"},
		{name: "libc", content: "host \"node\" {\n  platform {\n    libc = \"musl\"\n  }\n}\n", want: "read-only derived fact"},
		{name: "architecture", content: "host \"node\" {\n  platform {\n    architecture = \"ppc64le\"\n  }\n}\n", want: "use amd64 or arm64"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.apf.hcl")
			writeConfig(t, path, test.content)
			_, err := ParseFiles([]string{path})
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), path) {
				t.Fatalf("ParseFiles() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestParseAssertRejectsProtectedErrorMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assert.apf.hcl")
	writeConfig(t, path, `
variable "token" {
  type      = string
  default   = "not-a-real-secret"
  sensitive = true
}
assert {
  condition     = true
  error_message = var.token
}
`)
	_, err := ParseFiles([]string{path})
	if err == nil || !strings.Contains(err.Error(), "must not use sensitive or ephemeral values") || strings.Contains(err.Error(), "not-a-real-secret") {
		t.Fatalf("ParseFiles() error = %v", err)
	}
}

func TestParseModelRejectsDuplicateComponentInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duplicate.apf.hcl")
	writeConfig(t, path, `
component "app" {}
host "node" {
  component "app" { source = component.app }
  component "app" { source = component.app }
}

`)
	_, err := ParseFiles([]string{path})
	if err == nil || !strings.Contains(err.Error(), `duplicate component instance "app"`) {
		t.Fatalf("ParseFiles() error = %v", err)
	}
}

func TestParseHostSSHContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh.apf.hcl")
	writeConfig(t, path, `
host "node" {
  ssh {
    host          = "alpine-prod"
    port          = 2222
    user          = "root"
    identity_file = "~/.ssh/alpine"
  }
}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	ssh := config.Hosts["node"].SSH
	if ssh.Host != "alpine-prod" || ssh.Port != 2222 || ssh.User != "root" || ssh.IdentityFile != "~/.ssh/alpine" || !strings.HasSuffix(ssh.Source.Path, ".ssh") {
		t.Fatalf("ssh = %#v", ssh)
	}
}

func TestParseHostSSHRejectsUnsupportedValues(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "non-root", content: "host \"node\" {\n  ssh { user = \"admin\" }\n}\n", want: "requires root SSH"},
		{name: "zero port", content: "host \"node\" {\n  ssh { port = 0 }\n}\n", want: "between 1 and 65535"},
		{name: "fractional port", content: "host \"node\" {\n  ssh { port = 22.5 }\n}\n", want: "must be an integer"},
		{name: "option-like alias", content: "host \"node\" {\n  ssh { host = \"-oProxyCommand=bad\" }\n}\n", want: "single alias or address"},
		{name: "spaced alias", content: "host \"node\" {\n  ssh { host = \"bad alias\" }\n}\n", want: "single alias or address"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.apf.hcl")
			writeConfig(t, path, test.content)
			_, err := ParseFiles([]string{path})
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "invalid.apf.hcl") {
				t.Fatalf("ParseFiles() error = %v, want %q", err, test.want)
			}
		})
	}
}

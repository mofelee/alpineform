package merge

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
)

const artifactSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestCompileSelectsNormalizedArtifactArchitecture(t *testing.T) {
	config, err := compileConfig(t, `
component "tool" {
  type = "binary"
  source "amd64" {
    url = "https://example.invalid/tool-amd64"
    sha256 = "`+artifactSHA+`"
  }
  source "arm64" {
    url = "https://example.invalid/tool-arm64"
    sha256 = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
  }
  install { path = "/usr/local/bin/tool" }
}
host "node" {
  platform { architecture = "x86_64" }
  component "cli" { source = component.tool }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	component := program.Hosts[0].Components[0]
	if component.SelectedSource == nil || component.SelectedSource.Architecture != "amd64" || component.SelectedSource.URL != "https://example.invalid/tool-amd64" || component.Install == nil || component.Install.Mode != "0755" {
		t.Fatalf("compiled artifact = %#v", component)
	}
}

func TestCompileScriptsResolveReferencesAndRedactPayloads(t *testing.T) {
	secret := "not-a-real-script-secret"
	config, err := compileConfig(t, `
variable "token" {
  type      = string
  default   = "`+secret+`"
  sensitive = true
}
script "refresh" {
  commands = [["/usr/local/bin/refresh", var.token]]
  outputs  = ["/run/refreshed"]
}
component "first" {
  type = "file"
  source {
    url    = "https://example.invalid/first"
    sha256 = "`+artifactSHA+`"
  }
  install {
    path      = "/etc/first"
    on_change = global.script.refresh
  }
}
component "second" {
  type = "file"
  source {
    url    = "https://example.invalid/second"
    sha256 = "`+artifactSHA+`"
  }
  install {
    path      = "/etc/second"
    on_change = script.refresh
  }
}
host "node" {
  component "first" { source = component.first }
  component "second" { source = component.second }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	root := program.Hosts[0].Scripts["refresh"]
	if !root.Executable || !root.Sensitive || len(root.Commands) != 1 || root.Commands[0][1] != secret || root.DeclarationID != `script["refresh"]` {
		t.Fatalf("root script = %#v", root)
	}
	for _, component := range program.Hosts[0].Components {
		if component.Install == nil || component.Install.OnChange == nil || component.Install.OnChange.Scope != "root" || component.Install.OnChange.DeclarationID != root.DeclarationID {
			t.Fatalf("component reference = %#v", component.Install)
		}
	}
	data, err := json.Marshal(program)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("program JSON leaked script payload: %s", data)
	}
}

func TestCompileComponentLocalScriptUsesInputContext(t *testing.T) {
	config, err := compileConfig(t, `
component "worker" {
  input "service" {
    type    = string
    default = "worker"
  }
  script "reload" {
    content = "rc-service ${input.service} reload"
  }
  type = "file"
  source {
    url    = "https://example.invalid/worker"
    sha256 = "`+artifactSHA+`"
  }
  install {
    path      = "/etc/worker"
    on_change = script.reload
  }
}
host "node" {
  component "worker" { source = component.worker }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	component := program.Hosts[0].Components[0]
	script := component.Scripts["reload"]
	if script.Content != "rc-service worker reload" || script.DeclarationID != `component.worker.script["reload"]` || component.Install.OnChange.Scope != "component" || component.Install.OnChange.DeclarationID != script.DeclarationID {
		t.Fatalf("component script = %#v, reference = %#v", script, component.Install.OnChange)
	}
}

func TestCompileComponentComposesNativeDomains(t *testing.T) {
	config, err := compileConfig(t, `
script "refresh" { commands = [["rc-service", "worker", "reload"]] }
component "worker" {
  input "port" {
    type    = number
    default = 9000
  }
  groups {
    group "worker" { system = true }
  }
  users {
    user "worker" {
      group  = "worker"
      home   = "/var/lib/worker"
      shell  = "/sbin/nologin"
      system = true
    }
  }
  directories {
    directory "/var/lib/worker" {
      owner = "worker"
      group = "worker"
    }
  }
  files {
    file "/etc/worker.conf" {
      content   = "PORT=${input.port}\n"
      on_change = global.script.refresh
    }
  }
  packages {
    package "busybox-extras" {}
  }
  openrc {
    service "worker" {
      command            = "/usr/local/bin/worker"
      command_background = true
      pidfile            = "/run/worker.pid"
    }
  }
  services {
    service "worker" {
      enabled   = true
      state     = "running"
      operation = "restarted"
      package   = "busybox-extras"
      user      = "worker"
      group     = "worker"
    }
  }
}
host "node" {
  component "app" { source = component.worker }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	component := program.Hosts[0].Components[0]
	if len(component.Groups) != 1 || len(component.Users) != 1 || len(component.Directories) != 1 || len(component.Packages) != 1 || len(component.Services) != 1 || len(component.OpenRC) != 1 {
		t.Fatalf("component domains = %#v", component)
	}
	var configFile ir.ManagedFileSpec
	for _, file := range component.Files {
		if file.Path == "/etc/worker.conf" {
			configFile = file
		}
	}
	if configFile.Content != "PORT=9000\n" || configFile.OnChange == nil || configFile.OnChange.Scope != "root" {
		t.Fatalf("component file = %#v", configFile)
	}
}

func TestCompileArtifactValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "offline architecture", body: `
component "tool" {
  type = "file"
  source "amd64" {
    url    = "https://example.invalid/file"
    sha256 = "` + artifactSHA + `"
  }
  install { path = "/etc/tool" }
}
host "node" {
  component "tool" { source = component.tool }
}
`, want: "declare platform.architecture for offline source selection"},
		{name: "checksum", body: `
component "tool" {
  type = "file"
  source {
    url    = "https://example.invalid/file"
    sha256 = "bad"
  }
  install { path = "/etc/tool" }
}
host "node" {
  component "tool" { source = component.tool }
}
`, want: "exactly 64 hexadecimal"},
		{name: "unsupported type", body: `
component "tool" {
  type = "source"
  source {
    url    = "https://example.invalid/src"
    sha256 = "` + artifactSHA + `"
  }
  install { path = "/usr/local/bin/tool" }
}
host "node" {
  component "tool" { source = component.tool }
}
`, want: "supports binary, file, archive, and ca_certificate"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, test.body)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Compile(config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compile() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCompileArchiveAndCACertificateArtifacts(t *testing.T) {
	config, err := compileConfig(t, `
component "bundle" {
  type = "archive"
  source {
    url    = "https://example.invalid/bundle.tar.gz"
    sha256 = "`+artifactSHA+`"
  }
  extract { strip_components = 1 }
  install { path = "/opt/bundle" }
}
component "root_ca" {
  type = "ca_certificate"
  source {
    url    = "https://example.invalid/root.crt"
    sha256 = "`+artifactSHA+`"
  }
  install { path = "/usr/local/share/ca-certificates/example-root.crt" }
}
host "node" {
  component "bundle" { source = component.bundle }
  component "root_ca" { source = component.root_ca }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	archive := program.Hosts[0].Components[0]
	certificate := program.Hosts[0].Components[1]
	if archive.ArtifactType != "archive" || archive.Extract == nil || archive.Extract.Format != "tar.gz" || archive.Extract.StripComponents != 1 {
		t.Fatalf("archive = %#v", archive)
	}
	if certificate.ArtifactType != "ca_certificate" || certificate.Install == nil || certificate.Install.Mode != "0644" {
		t.Fatalf("certificate = %#v", certificate)
	}
	if len(certificate.Packages) != 1 || certificate.Packages[0].Name != "ca-certificates" || certificate.Packages[0].Ensure != "present" {
		t.Fatalf("certificate packages = %#v", certificate.Packages)
	}
}

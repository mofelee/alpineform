package merge

import (
	"strings"
	"testing"
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

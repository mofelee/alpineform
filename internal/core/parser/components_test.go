package parser

import (
	"path/filepath"
	"testing"
)

func TestParseArtifactComponentSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact.apf.hcl")
	writeConfig(t, path, `
component "tool" {
  type    = "binary"
  version = "1.2.3"
  source "amd64" {
    url    = "https://example.invalid/tool-amd64"
    sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  }
  install {
    path = "/usr/local/bin/tool"
  }
}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	component := config.Components["tool"]
	if component.ArtifactType != "binary" || component.Version != "1.2.3" || component.Sources["amd64"].URL == "" || component.Install == nil || component.Install.Path != "/usr/local/bin/tool" {
		t.Fatalf("artifact component = %#v", component)
	}
}

func TestParseSourceBuildSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "build.apf.hcl")
	writeConfig(t, path, `
component "tool" {
  type = "source"
  build {
    input "source" {
      content     = "int main(void) { return 0; }"
      sha256      = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
      destination = "main.c"
    }
    command { argv = ["cc", "-o", "tool", "main.c"] }
    output = "tool"
  }
  install { path = "/usr/local/bin/tool" }
}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	build := config.Components["tool"].Build
	if build == nil || len(build.Inputs) != 1 || len(build.Commands) != 1 || build.Inputs[0].Name != "source" {
		t.Fatalf("source build = %#v", build)
	}
}

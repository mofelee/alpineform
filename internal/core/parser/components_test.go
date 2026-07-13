package parser

import (
	"path/filepath"
	"strings"
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

func TestParseRejectsTargetBuildWithFollowUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "build.apf.hcl")
	writeConfig(t, path, `
component "tool" {
  build {}
}
`)
	_, err := ParseFiles([]string{path})
	if err == nil || !strings.Contains(err.Error(), "unsupported in v0.1") || !strings.Contains(err.Error(), "#14") {
		t.Fatalf("ParseFiles() error = %v", err)
	}
}

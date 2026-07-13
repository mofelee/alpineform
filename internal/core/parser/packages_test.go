package parser

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePackagesCollection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.apf.hcl")
	writeConfig(t, path, `
host "node" {
  packages {
    package "curl" {}
    package "vendor-agent" {
      repository = "vendor"
      ensure     = "absent"
      lifecycle { prevent_destroy = true }
    }
  }
}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	resources := config.Hosts["node"].Resources
	if len(resources) != 2 || resources[0].Kind != ResourcePackage || resources[0].Label != "curl" || resources[1].Attributes["repository"].Expression == nil || !resources[1].Lifecycle.PreventDestroy {
		t.Fatalf("parsed packages = %#v", resources)
	}
}

func TestParsePackagesRejectsUnsupportedVersionSurface(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.apf.hcl")
	writeConfig(t, path, `
host "node" {
  packages {
    package "curl" { version = ">=8" }
  }
}
`)
	_, err := ParseFiles([]string{path})
	if err == nil || !strings.Contains(err.Error(), "unsupported attribute") {
		t.Fatalf("ParseFiles() error = %v", err)
	}
}

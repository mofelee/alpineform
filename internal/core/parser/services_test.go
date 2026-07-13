package parser

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseServiceStateAndDependencies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.apf.hcl")
	writeConfig(t, path, `
host "node" {
  services {
    service "worker" {
      enabled  = false
      runlevel = "boot"
      state    = "stopped"
      package  = "worker-daemon"
      user     = "worker"
      group    = "worker"
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
	if len(resources) != 1 || resources[0].Kind != ResourceService || resources[0].Label != "worker" || !resources[0].Lifecycle.PreventDestroy {
		t.Fatalf("parsed services = %#v", resources)
	}
}

func TestParseServiceRejectsOperationBeforeProviderSurfaceExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.apf.hcl")
	writeConfig(t, path, `
host "node" {
  services {
    service "worker" { operation = "restarted" }
  }
}
`)
	_, err := ParseFiles([]string{path})
	if err == nil || !strings.Contains(err.Error(), "unsupported attribute") {
		t.Fatalf("ParseFiles() error = %v", err)
	}
}

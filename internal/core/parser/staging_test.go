package parser

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseStagingPoliciesRetainValuesAndSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "staging.apf.hcl")
	writeConfig(t, path, `
profile "base" {
  staging { root = "/srv/profile staging" }
}

component "tool" {}

host "node" {
  staging { root = "/srv/host-staging" }
  component "tool" {
    source       = component.tool
    staging_root = "/mnt/build-work"
  }
}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	profile := config.Profiles["base"]
	if profile.Staging == nil || profile.Staging.Root.String != "/srv/profile staging" || profile.Staging.Source.Path != `profile["base"].staging` || profile.Staging.Root.Source.Path != `profile["base"].staging.root` {
		t.Fatalf("profile staging = %#v", profile.Staging)
	}
	host := config.Hosts["node"]
	if host.Staging == nil || host.Staging.Root.String != "/srv/host-staging" || host.Staging.Root.Source.Path != `host["node"].staging.root` {
		t.Fatalf("host staging = %#v", host.Staging)
	}
	instance := host.Components[0]
	if instance.StagingRoot == nil || instance.StagingRoot.String != "/mnt/build-work" || instance.StagingRoot.Source.Path != `host["node"].component["tool"].staging_root` {
		t.Fatalf("component staging root = %#v", instance.StagingRoot)
	}
}

func TestParseStagingPreservesProtectedMarks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "protected-staging.apf.hcl")
	writeConfig(t, path, `
variable "sensitive_root" {
  type      = string
  default   = "/not-a-real-sensitive-root"
  sensitive = true
}
variable "ephemeral_root" {
  type      = string
  default   = "/not-a-real-ephemeral-root"
  ephemeral = true
}
profile "base" {
  staging { root = var.sensitive_root }
  component "tool" {
    source       = component.tool
    staging_root = var.ephemeral_root
  }
}
component "tool" {}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	profile := config.Profiles["base"]
	if profile.Staging == nil || !profile.Staging.Root.ContainsSensitive() || profile.Staging.Root.ContainsEphemeral() {
		t.Fatalf("profile staging marks = %#v", profile.Staging)
	}
	instance := profile.Components[0]
	if instance.StagingRoot == nil || instance.StagingRoot.ContainsSensitive() || !instance.StagingRoot.ContainsEphemeral() {
		t.Fatalf("component staging marks = %#v", instance.StagingRoot)
	}
}

func TestParseStagingRejectsInvalidShape(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "label", content: `
host "node" {
  staging "bad" { root = "/srv/work" }
}
`, want: "must be an unlabeled attribute-only block"},
		{name: "nested block", content: `
host "node" {
  staging {
    root = "/srv/work"
    nested {}
  }
}
`, want: "must be an unlabeled attribute-only block"},
		{name: "missing root", content: `
profile "base" {
  staging {}
}
`, want: ".staging.root is required"},
		{name: "unknown attribute", content: `
profile "base" {
  staging {
    root = "/srv/work"
    mode = "0700"
  }
}
`, want: "unsupported attribute"},
		{name: "duplicate profile", content: `
profile "base" {
  staging { root = "/srv/one" }
  staging { root = "/srv/two" }
}
`, want: "duplicate profile[\"base\"].staging block"},
		{name: "duplicate host", content: `
host "node" {
  staging { root = "/srv/one" }
  staging { root = "/srv/two" }
}
`, want: "duplicate host[\"node\"].staging block"},
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

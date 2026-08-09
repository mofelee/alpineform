package parser

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTypedResourceDependencies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.apf.hcl")
	writeConfig(t, path, `
profile "bird" {
  packages {
    package "base" {}
    package "bird-tools" {
      depends_on = [package.base]
    }
  }
  files {
    file "/etc/bird.conf" {
      content    = "# managed\n"
      depends_on = [package["bird-tools"], service["bird-router"]]
    }
  }
  services {
    service "bird-router" {
      depends_on = [file["/etc/bird.conf"]]
    }
  }
}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	resources := config.Profiles["bird"].Resources
	if len(resources) != 4 {
		t.Fatalf("profile resources = %#v", resources)
	}
	pack := resources[1]
	if len(pack.DependsOn) != 1 || pack.DependsOn[0].Kind != ResourcePackage || pack.DependsOn[0].Label != "base" {
		t.Fatalf("package depends_on = %#v", pack.DependsOn)
	}
	file := resources[2]
	if len(file.DependsOn) != 2 || file.DependsOn[0].Kind != ResourcePackage || file.DependsOn[0].Label != "bird-tools" || file.DependsOn[0].Source.Path != `profile["bird"].files.file["/etc/bird.conf"].depends_on[0]` || file.DependsOn[1].Kind != ResourceService || file.DependsOn[1].Label != "bird-router" {
		t.Fatalf("file depends_on = %#v", file.DependsOn)
	}
	service := resources[3]
	if len(service.DependsOn) != 1 || service.DependsOn[0].Kind != ResourceFile || service.DependsOn[0].Label != "/etc/bird.conf" {
		t.Fatalf("service depends_on = %#v", service.DependsOn)
	}
	if _, exists := file.Attributes["depends_on"]; exists {
		t.Fatal("depends_on was retained as an evaluable resource attribute")
	}
}

func TestParseRejectsInvalidResourceDependencies(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{name: "not list", expression: `package.bird`, want: "must be a list"},
		{name: "literal", expression: `["package.bird"]`, want: "entry must be"},
		{name: "cross host", expression: `[host.other.package.bird]`, want: "entry must be"},
		{name: "unsupported type", expression: `[component.bird]`, want: "type is out of scope"},
		{name: "sensitive value", expression: `[var.sensitive_path]`, want: "type is out of scope"},
		{name: "ephemeral value", expression: `[var.ephemeral_path]`, want: "type is out of scope"},
		{name: "dynamic", expression: `[file[var.sensitive_path]]`, want: "entry must be"},
		{name: "empty", expression: `[file[""]]`, want: "non-empty static string"},
		{name: "duplicate", expression: `[package.bird, package["bird"]]`, want: "duplicate depends_on reference package.bird"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.apf.hcl")
			writeConfig(t, path, `
variable "sensitive_path" {
  type      = string
  default   = "sensitive-secret-label"
  sensitive = true
}
variable "ephemeral_path" {
  type      = string
  default   = "ephemeral-secret-label"
  ephemeral = true
}
host "node" {
  packages {
    package "bird" {}
  }
  files {
    file "/etc/bird.conf" {
      content    = "ok"
      depends_on = `+test.expression+`
    }
  }
}
`)
			_, err := ParseFiles([]string{path})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseFiles() error = %v, want %q", err, test.want)
			}
			if !strings.Contains(err.Error(), ".depends_on") {
				t.Fatalf("error lacks depends_on source path: %v", err)
			}
			if strings.Contains(err.Error(), "sensitive-secret-label") || strings.Contains(err.Error(), "ephemeral-secret-label") {
				t.Fatalf("error leaked a protected variable value: %v", err)
			}
		})
	}
}

func TestParseResourceDependenciesRejectAmbiguousLexicalTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.apf.hcl")
	writeConfig(t, path, `
profile "duplicate" {
  packages {
    package "bird" {}
  }
  packages {
    package "bird" {}
  }
  files {
    file "/etc/bird.conf" {
      content    = "ok"
      depends_on = [package.bird]
    }
  }
}
`)
	_, err := ParseFiles([]string{path})
	if err == nil || !strings.Contains(err.Error(), `duplicate package label "bird"`) {
		t.Fatalf("ParseFiles() error = %v, want duplicate target diagnostic", err)
	}
}

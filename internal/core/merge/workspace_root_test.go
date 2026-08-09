package merge

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/product"
)

const workspaceRootBuildComponent = `
component "tool" {
  type = "source"
  build {
    input "source" {
      content     = "source"
      sha256      = "41cf6794ba4200b839c53531555f0f3998df4cbb01a4d5cb0b94e3ca5e23947d"
      destination = "source.txt"
    }
    command { argv = ["cp", "source.txt", "tool"] }
    output = "tool"
  }
  install { path = "/usr/local/bin/tool" }
}
`

func hostByName(t *testing.T, hosts []ir.HostSpec, name string) ir.HostSpec {
	t.Helper()
	for _, host := range hosts {
		if host.Name == name {
			return host
		}
	}
	t.Fatalf("host %q not found in %#v", name, hosts)
	return ir.HostSpec{}
}

func TestCompileComponentWorkspaceRootPrecedenceAndWholeInstanceOverlay(t *testing.T) {
	config, err := compileConfig(t, workspaceRootBuildComponent+`
profile "base" {
  staging { root = "/srv/base-work" }
  component "tool" {
    source       = component.tool
    staging_root = "/mnt/base-instance"
  }
}
profile "production" {
  imports = [profile.base]
  staging { root = "/srv/profile-work" }
  component "tool" {
    source = component.tool
  }
}
profile "later" {
  staging { root = "/srv/later-work" }
}
host "default" {
  component "tool" { source = component.tool }
}
host "profile" {
  imports = [profile.production]
}
host "later_import" {
  imports = [profile.production, profile.later]
  component "tool" { source = component.tool }
}
host "host_override" {
  imports = [profile.production]
  staging { root = "/srv/host-work" }
}
host "instance_override" {
  imports = [profile.production]
  component "tool" {
    source       = component.tool
    staging_root = "/mnt/instance-work"
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
	tests := map[string]struct {
		workspace string
		staging   string
	}{
		"default":           {workspace: product.DefaultComponentBuildWorkspaceRoot},
		"profile":           {workspace: "/srv/profile-work", staging: "/srv/profile-work"},
		"later_import":      {workspace: "/srv/later-work", staging: "/srv/later-work"},
		"host_override":     {workspace: "/srv/host-work", staging: "/srv/host-work"},
		"instance_override": {workspace: "/mnt/instance-work", staging: "/srv/profile-work"},
	}
	for name, want := range tests {
		host := hostByName(t, program.Hosts, name)
		if len(host.Components) != 1 || host.Components[0].Build == nil || host.Components[0].Build.WorkspaceRoot != want.workspace {
			t.Fatalf("host %s component build = %#v, want workspace %q", name, host.Components, want.workspace)
		}
		if want.staging == "" {
			if host.Staging != nil {
				t.Fatalf("host %s staging = %#v, want nil", name, host.Staging)
			}
		} else if host.Staging == nil || host.Staging.Root != want.staging {
			t.Fatalf("host %s staging = %#v, want %q", name, host.Staging, want.staging)
		}
	}
	encoded, err := json.Marshal(program)
	if err != nil {
		t.Fatal(err)
	}
	for _, runtimeOnly := range []string{"workspace_root", "/srv/profile-work", "/srv/host-work", "/mnt/instance-work"} {
		if strings.Contains(string(encoded), runtimeOnly) {
			t.Fatalf("serialized program exposed runtime-only workspace placement %q: %s", runtimeOnly, encoded)
		}
	}
	// profile.production replaces the complete base instance, so the lower
	// precedence per-instance root must not survive that replacement.
	if got := hostByName(t, program.Hosts, "profile").Components[0].Build.WorkspaceRoot; got == "/mnt/base-instance" {
		t.Fatalf("whole-instance overlay retained lower-precedence staging_root %q", got)
	}
}

func TestCompileComponentWorkspaceRootDoesNotChangeBuildIdentity(t *testing.T) {
	compile := func(root string) *ir.ComponentBuildSpec {
		config, err := compileConfig(t, workspaceRootBuildComponent+`
host "node" {
  staging { root = "`+root+`" }
  component "tool" { source = component.tool }
}
`)
		if err != nil {
			t.Fatal(err)
		}
		program, err := Compile(config)
		if err != nil {
			t.Fatal(err)
		}
		return program.Hosts[0].Components[0].Build
	}
	first := compile("/srv/first-work")
	second := compile("/srv/second-work")
	if first.WorkspaceRoot == second.WorkspaceRoot {
		t.Fatalf("workspace roots = %q and %q, want different", first.WorkspaceRoot, second.WorkspaceRoot)
	}
	if first.Identity != second.Identity || !reflect.DeepEqual(first.IdentityDocument, second.IdentityDocument) {
		t.Fatalf("workspace placement changed build identity: %#v != %#v", first, second)
	}
}

func TestCompileRejectsInvalidComponentWorkspaceRoots(t *testing.T) {
	tests := []struct {
		name      string
		root      string
		want      string
		instance  bool
		protected string
		secret    string
		path      string
	}{
		{name: "relative", root: `"var/tmp/builds"`, want: "clean absolute non-root path"},
		{name: "root", root: `"/"`, want: "clean absolute non-root path"},
		{name: "unclean", root: `"/srv/../tmp/builds"`, want: "clean absolute non-root path"},
		{name: "trailing slash", root: `"/srv/builds/"`, want: "clean absolute non-root path"},
		{name: "control", root: `"/srv/builds\nother"`, want: "without control characters"},
		{name: "wrong type", root: `42`, want: "workspace root must be a string"},
		{name: "sensitive host", root: `var.root`, want: "must not use sensitive or ephemeral values", protected: "sensitive", secret: "/not-a-real-sensitive-root", path: `host["node"].staging.root`},
		{name: "ephemeral instance", root: `var.root`, want: "must not use sensitive or ephemeral values", protected: "ephemeral", secret: "/not-a-real-ephemeral-root", instance: true, path: `host["node"].component["tool"].staging_root`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			variable := ""
			if test.protected != "" {
				variable = `
variable "root" {
  type      = string
  default   = "` + test.secret + `"
  ` + test.protected + ` = true
}
`
			}
			policy := "\n  staging { root = " + test.root + " }\n  component \"tool\" { source = component.tool }\n"
			if test.instance {
				policy = "\n  component \"tool\" {\n    source = component.tool\n    staging_root = " + test.root + "\n  }\n"
			}
			config, err := compileConfig(t, variable+workspaceRootBuildComponent+`host "node" {`+policy+`}`)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Compile(config)
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "main.apf.hcl") {
				t.Fatalf("Compile() error = %v, want %q with source", err, test.want)
			}
			if test.secret != "" && strings.Contains(err.Error(), test.secret) {
				t.Fatalf("Compile() error leaked protected root: %v", err)
			}
			if test.path != "" && !strings.Contains(err.Error(), test.path) {
				t.Fatalf("Compile() error = %v, want source path %q", err, test.path)
			}
		})
	}
}

func TestCompileWorkspaceRootAllowsSpacesAndUnusedHostPolicy(t *testing.T) {
	config, err := compileConfig(t, `
component "file" {
  type = "file"
  source {
    url    = "https://example.invalid/file"
    sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  }
  install { path = "/etc/example" }
}
host "node" {
  staging { root = "/srv/build volume" }
  component "file" { source = component.file }
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
	if host.Staging == nil || host.Staging.Root != "/srv/build volume" || host.Components[0].Build != nil {
		t.Fatalf("compiled host = %#v", host)
	}
}

func TestCompileRejectsStagingRootOnNonSourceComponent(t *testing.T) {
	config, err := compileConfig(t, `
component "file" {
  type = "file"
  source {
    url    = "https://example.invalid/file"
    sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  }
  install { path = "/etc/example" }
}
host "node" {
  component "file" {
    source       = component.file
    staging_root = "/srv/builds"
  }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(config)
	if err == nil || !strings.Contains(err.Error(), "staging_root is valid only for source-build components") || !strings.Contains(err.Error(), `host["node"].component["file"].staging_root`) {
		t.Fatalf("Compile() error = %v", err)
	}
}

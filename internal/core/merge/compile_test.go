package merge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/parser"
)

func compileConfig(t *testing.T, content string) (*parser.Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.apf.hcl")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := parser.ParseFiles([]string{path})
	if err != nil {
		return nil, err
	}
	return config, nil
}

func TestCompileResolvesProfilesComponentsAndProtectedMetadata(t *testing.T) {
	config, err := compileConfig(t, `
variable "api_token" {
  type      = string
  default   = "not-a-real-variable-secret"
  sensitive = true
}

component "app" {
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
    default   = "not-a-real-component-secret"
    sensitive = true
    ephemeral = true
  }
  assert {
    condition     = input.port > 0
    error_message = "port must be positive"
  }
}

component "alternate" {
  input "port" {
    type = number
  }
}

profile "base" {
  component "service" {
    source = component.app
    inputs = { port = 8080 }
  }
}

profile "production" {
  imports = [profile.base]
  component "service" {
    source = component.alternate
    inputs = { port = 9090 }
    lifecycle { prevent_destroy = true }
  }
}

host "z_node" {
  imports = [profile.production]
  platform {
    architecture = "aarch64"
    version      = "3.24.1"
  }
  component "worker" {
    source     = component.app
    depends_on = [component.service]
    inputs = {
      port  = 7070
      token = var.api_token
    }
  }
  assert {
    condition     = self.platform.branch == "3.24" && self.platform.libc == "musl"
    error_message = "unexpected platform"
  }
}

host "a_node" {}
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{program.Hosts[0].Name, program.Hosts[1].Name}; !reflect.DeepEqual(got, []string{"a_node", "z_node"}) {
		t.Fatalf("host order = %#v", got)
	}
	host := program.Hosts[1]
	if host.Platform == nil || host.Platform.Architecture != "arm64" || host.Platform.NativeArchitecture != "aarch64" || host.Platform.Branch != "3.24" {
		t.Fatalf("platform = %#v", host.Platform)
	}
	if len(host.Components) != 2 || host.Components[0].Name != "service" || host.Components[0].Template != "alternate" || !host.Components[0].Lifecycle.PreventDestroy {
		t.Fatalf("resolved components = %#v", host.Components)
	}
	worker := host.Components[1]
	if !reflect.DeepEqual(worker.DependsOn, []string{"service"}) || !reflect.DeepEqual(worker.ProtectedInputs, []string{"token"}) {
		t.Fatalf("worker = %#v", worker)
	}

	data, err := json.Marshal(program)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, secret := range []string{"not-a-real-variable-secret", "not-a-real-component-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("IR JSON leaked %q: %s", secret, text)
		}
	}
	if got := program.Variables["api_token"].Default; got != "<sensitive>" {
		t.Fatalf("protected variable default = %#v", got)
	}
	if !strings.Contains(text, "protected_inputs") {
		t.Fatalf("IR JSON missing protected input metadata: %s", text)
	}

	second, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	secondData, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(secondData) {
		t.Fatalf("compile output is not deterministic:\n%s\n%s", data, secondData)
	}
}

func TestCompileRejectsProfileAndComponentGraphErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "profile cycle",
			content: `
profile "one" { imports = [profile.two] }
profile "two" { imports = [profile.one] }
host "node" { imports = [profile.one] }
`,
			want: "profile import cycle: profile.one -> profile.two -> profile.one",
		},
		{
			name:    "unknown profile",
			content: `host "node" { imports = [profile.missing] }`,
			want:    "unknown profile.missing",
		},
		{
			name: "unknown template",
			content: `
host "node" {
  component "app" { source = component.missing }
}
`,
			want: "unknown component.missing",
		},
		{
			name: "unknown input",
			content: `
component "app" {}
host "node" {
  component "app" {
    source = component.app
    inputs = { missing = true }
  }
}
`,
			want: `unknown input "missing"`,
		},
		{
			name: "required input",
			content: `
component "app" {
  input "port" { type = number }
}
host "node" {
  component "app" { source = component.app }
}
`,
			want: `input "port" is required`,
		},
		{
			name: "invalid input type",
			content: `
component "app" {
  input "port" { type = number }
}
host "node" {
  component "app" {
    source = component.app
    inputs = { port = "wrong" }
  }
}
`,
			want: "must be number",
		},
		{
			name: "input validation",
			content: `
component "app" {
  input "port" {
    type = number
    validation {
      condition     = input.port > 0
      error_message = "port must be positive"
    }
  }
}
host "node" {
  component "app" {
    source = component.app
    inputs = { port = 0 }
  }
}
`,
			want: "port must be positive",
		},
		{
			name: "invalid protected default",
			content: `
component "app" {
  input "token" {
    type      = number
    default   = "not-a-real-secret"
    sensitive = true
  }
}
`,
			want: "invalid protected default",
		},
		{
			name: "unknown dependency",
			content: `
component "app" {}
host "node" {
  component "app" {
    source     = component.app
    depends_on = [component.missing]
  }
}
`,
			want: "unknown component.missing on host node",
		},
		{
			name: "dependency cycle",
			content: `
component "app" {}
host "node" {
  component "one" {
    source     = component.app
    depends_on = [component.two]
  }
  component "two" {
    source     = component.app
    depends_on = [component.one]
  }
}
`,
			want: "component dependency cycle on host node: component.one -> component.two -> component.one",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, test.content)
			if err == nil {
				_, err = Compile(config)
			}
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "main.apf.hcl") {
				t.Fatalf("Compile() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCompileRequiresOnlyReferencedOfflinePlatformFacts(t *testing.T) {
	config, err := compileConfig(t, `host "plain" {}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(config); err != nil {
		t.Fatalf("plain host unexpectedly requires platform: %v", err)
	}

	config, err = compileConfig(t, `
host "node" {
  assert {
    condition     = self.platform.architecture == "amd64"
    error_message = "amd64 required"
  }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(config)
	if err == nil || !strings.Contains(err.Error(), "declare platform.architecture for offline evaluation") {
		t.Fatalf("Compile() error = %v", err)
	}
}

func TestCompileProtectedInputErrorDoesNotLeak(t *testing.T) {
	secret := "not-a-real-protected-value"
	config, err := compileConfig(t, `
component "app" {
  input "token" {
    type      = number
    sensitive = true
  }
}
host "node" {
  component "app" {
    source = component.app
    inputs = { token = "`+secret+`" }
  }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(config)
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "invalid protected input") {
		t.Fatalf("Compile() error = %v", err)
	}
}

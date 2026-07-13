package merge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
	"github.com/mofelee/alpineform/internal/product"
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
			want: "invalid protected component input default",
		},
		{
			name: "invalid default validation",
			content: `
component "app" {
  input "port" {
    type    = number
    default = 0
    validation {
      condition     = input.port > 0
      error_message = "default port must be positive"
    }
  }
}
`,
			want: "default port must be positive",
		},
		{
			name: "cross input validation",
			content: `
component "app" {
  input "port" {
    type    = number
    default = 80
    validation {
      condition     = input.other > 0
      error_message = "invalid reference"
    }
  }
  input "other" { type = number }
}
`,
			want: "can only read input.port",
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

	config, err = compileConfig(t, `
host "node" {
  assert {
    condition     = self.platform.branch == "3.24"
    error_message = "Alpine 3.24 required"
  }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(config)
	if err == nil || !strings.Contains(err.Error(), "declare platform.version for offline evaluation") {
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

func TestCompileWithDetectedFactsProvidesSecondPhaseContext(t *testing.T) {
	config, err := compileConfig(t, `
host "node" {
  assert {
    condition     = target.platform.architecture == "amd64" && target.platform.version == "3.24.1" && target.platform.branch == "3.24" && target.platform.libc == "musl"
    error_message = "unexpected detected platform"
  }
}

`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(config); err == nil || !strings.Contains(err.Error(), "declare platform.architecture") {
		t.Fatalf("offline Compile() error = %v", err)
	}
	facts := ir.HostFacts{OSID: "alpine", Version: "3.24.1", Branch: "v3.24", Architecture: "amd64", NativeArchitecture: "x86_64", KernelArchitecture: "x86_64", Libc: "musl", DetectedAt: "2026-07-13T07:00:00Z"}
	program, err := CompileWithOptions(config, CompileOptions{HostFacts: map[string]ir.HostFacts{"node": facts}})
	if err != nil {
		t.Fatal(err)
	}
	if len(program.Hosts) != 1 || program.Hosts[0].Facts == nil || *program.Hosts[0].Facts != facts {
		t.Fatalf("compiled facts = %#v", program.Hosts)
	}
}

func TestConnectionTargetsPrecedeFactDependentCompile(t *testing.T) {
	config, err := compileConfig(t, `
host "node" {
  ssh {
    host          = "alpine-alias"
    port          = 2222
    user          = "root"
    identity_file = "~/.ssh/alpine"
  }
  assert {
    condition     = target.platform.branch == "3.24"
    error_message = "requires Alpine 3.24"
  }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(config); err == nil || !strings.Contains(err.Error(), "offline evaluation") {
		t.Fatalf("first-phase full Compile() error = %v", err)
	}
	targets, err := ConnectionTargets(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Name != "node" || targets[0].SSH.Host != "alpine-alias" || targets[0].SSH.Port != 2222 || targets[0].SSH.User != "root" || targets[0].SSH.IdentityFile != "~/.ssh/alpine" {
		t.Fatalf("connection targets = %#v", targets)
	}
	if targets[0].State.Path != product.DefaultStatePath || targets[0].State.LockPath != product.DefaultLockPath {
		t.Fatalf("connection target state defaults = %#v", targets[0].State)
	}
}

func TestCompileWithDetectedFactsRejectsBeforePlanning(t *testing.T) {
	config, err := compileConfig(t, `
host "node" {
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }
}

`)
	if err != nil {
		t.Fatal(err)
	}
	base := ir.HostFacts{OSID: "alpine", Version: "3.24.1", Branch: "v3.24", Architecture: "amd64", NativeArchitecture: "x86_64", KernelArchitecture: "x86_64", Libc: "musl", DetectedAt: "2026-07-13T07:00:00Z"}
	tests := []struct {
		name string
		host string
		edit func(*ir.HostFacts)
		want string
	}{
		{name: "unknown host", host: "missing", want: `facts were provided for unknown host "missing"`},
		{name: "foreign OS", host: "node", edit: func(facts *ir.HostFacts) { facts.OSID = "debian" }, want: "not a supported Alpine"},
		{name: "unsupported version", host: "node", edit: func(facts *ir.HostFacts) { facts.Version = "3.23.4" }, want: "unsupported exact version"},
		{name: "invalid architecture", host: "node", edit: func(facts *ir.HostFacts) { facts.Architecture = "ppc64le" }, want: "unsupported architecture"},
		{name: "native mismatch", host: "node", edit: func(facts *ir.HostFacts) { facts.NativeArchitecture = "aarch64" }, want: "mismatch amd64"},
		{name: "declared architecture mismatch", host: "node", edit: func(facts *ir.HostFacts) { facts.Architecture = "arm64"; facts.NativeArchitecture = "aarch64" }, want: `declares "amd64", but detected architecture is "arm64"`},
		{name: "declared version mismatch", host: "node", edit: func(facts *ir.HostFacts) { facts.Version = "3.24.2" }, want: `declares "3.24.1", but detected exact version is "3.24.2"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			facts := base
			if test.edit != nil {
				test.edit(&facts)
			}
			_, err := CompileWithOptions(config, CompileOptions{HostFacts: map[string]ir.HostFacts{test.host: facts}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("CompileWithOptions() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCompileSetsSSHAndBackendDefaults(t *testing.T) {
	config, err := compileConfig(t, `host "node" {}`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	host := program.Hosts[0]
	if host.SSH.Host != "node" || host.SSH.User != "root" || host.SSH.Port != 0 || host.State.Path != "/var/lib/alpineform/state.json" || host.State.LockPath != "/run/lock/alpineform/lock" {
		t.Fatalf("compiled host defaults = %#v", host)
	}
}

func TestCompileFileResourceProtectsWriteOnlyContent(t *testing.T) {
	secret := "not-a-real-write-only-file-secret"
	config, err := compileConfig(t, `
variable "payload" {
  type      = string
  default   = "`+secret+`"
  sensitive = true
  ephemeral = true
}
host "node" {
  files {
    file "/etc/app/config" {
      content         = var.payload
      content_version = "release-7"
      owner           = "root"
      group           = "root"
      mode            = "600"
      on_remove       = "destroy"
      lifecycle { prevent_destroy = true }
    }
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
	if len(program.Hosts[0].Files) != 1 {
		t.Fatalf("files = %#v", program.Hosts[0].Files)
	}
	file := program.Hosts[0].Files[0]
	if file.Content != secret || file.ContentSHA256 != "" || file.ContentVersion != "release-7" || !file.ContentWriteOnly || !file.Sensitive || !file.Ephemeral || file.Mode != "0600" || file.OnRemove != "destroy" || !file.Lifecycle.PreventDestroy {
		t.Fatalf("compiled file = %#v", file)
	}
	data, err := json.Marshal(program)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), `"content"`) {
		t.Fatalf("compiled program leaked content: %s", data)
	}
}

func TestCompileFileResourceValidation(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		wantErr string
	}{
		{name: "relative path", file: "file \"relative\" {\n  content = \"x\"\n}", wantErr: "clean absolute"},
		{name: "missing content", file: "file \"/tmp/x\" {}", wantErr: "exactly one of content or source"},
		{name: "both payloads", file: "file \"/tmp/x\" {\n  content = \"x\"\n  source = \"x\"\n}", wantErr: "only one of content or source"},
		{name: "invalid mode", file: "file \"/tmp/x\" {\n  content = \"x\"\n  mode = \"0988\"\n}", wantErr: "octal string"},
		{name: "invalid owner", file: "file \"/tmp/x\" {\n  content = \"x\"\n  owner = \"bad owner\"\n}", wantErr: "valid Alpine account"},
		{name: "absent payload", file: "file \"/tmp/x\" {\n  ensure = \"absent\"\n  content = \"x\"\n}", wantErr: "must not set content"},
		{name: "bad remove behavior", file: "file \"/tmp/x\" {\n  content = \"x\"\n  on_remove = \"delete\"\n}", wantErr: "forget"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, "host \"node\" {\n  files {\n    "+test.file+"\n  }\n}\n")
			if err != nil {
				t.Fatal(err)
			}
			_, err = Compile(config)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Compile() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestCompileEphemeralFileRequiresPublicVersion(t *testing.T) {
	secret := "not-a-real-ephemeral-content"
	config, err := compileConfig(t, `
variable "payload" {
  type      = string
  default   = "`+secret+`"
  ephemeral = true
}
host "node" {
  files {
    file "/tmp/x" { content = var.payload }
  }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(config)
	if err == nil || !strings.Contains(err.Error(), "content_version") || strings.Contains(err.Error(), secret) {
		t.Fatalf("Compile() error = %v", err)
	}
}

func TestCompileFileLoadsRelativeSource(t *testing.T) {
	dir := t.TempDir()
	payload := "source-backed content\n"
	if err := os.WriteFile(filepath.Join(dir, "payload.txt"), []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "main.apf.hcl")
	content := `
host "node" {
  files {
    file "/etc/source-backed" {
      source = "payload.txt"
    }
  }
}
`
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := parser.ParseFiles([]string{configPath})
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	file := program.Hosts[0].Files[0]
	if file.Content != payload || file.ContentBytes != int64(len(payload)) || file.ContentSHA256 == "" {
		t.Fatalf("source-backed file = %#v", file)
	}
}

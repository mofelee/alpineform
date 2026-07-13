package merge

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
	"github.com/mofelee/alpineform/internal/product"
	"golang.org/x/crypto/ssh"
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

func validTestAuthorizedKey(t *testing.T) string {
	t.Helper()
	publicKey, err := ssh.NewPublicKey(ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(publicKey)))
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

func TestCompileDirectoryResourceDefaultsAndPolicy(t *testing.T) {
	config, err := compileConfig(t, `
host "node" {
  directories {
    directory "/srv/app" {}
    directory "/srv/app/data" {
      owner            = "1000"
      group            = "app"
      mode             = "750"
      recursive_delete = true
      on_remove        = "destroy"
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
	directories := program.Hosts[0].Directories
	if len(directories) != 2 {
		t.Fatalf("directories = %#v", directories)
	}
	parent := directories[0]
	if parent.Path != "/srv/app" || parent.Owner != "root" || parent.Group != "root" || parent.Mode != "0755" || parent.Ensure != "present" || parent.OnRemove != "forget" || parent.RecursiveDelete || parent.Lifecycle.PreventDestroy {
		t.Fatalf("default directory = %#v", parent)
	}
	child := directories[1]
	if child.Path != "/srv/app/data" || child.Owner != "1000" || child.Group != "app" || child.Mode != "0750" || child.Ensure != "present" || child.OnRemove != "destroy" || !child.RecursiveDelete || !child.Lifecycle.PreventDestroy {
		t.Fatalf("configured directory = %#v", child)
	}
}

func TestCompileDirectoryResourceValidation(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "relative path", body: "directories {\n    directory \"relative\" {}\n  }", wantErr: "clean absolute non-root"},
		{name: "root path", body: "directories {\n    directory \"/\" {}\n  }", wantErr: "clean absolute non-root"},
		{name: "unclean path", body: "directories {\n    directory \"/srv/../app\" {}\n  }", wantErr: "clean absolute non-root"},
		{name: "invalid mode", body: "directories {\n    directory \"/srv/app\" { mode = \"0988\" }\n  }", wantErr: "octal string"},
		{name: "file conflict", body: `directories {
    directory "/srv/app" {}
  }
  files {
    file "/srv/app" { content = "x" }
  }`, wantErr: "conflicts with directory"},
		{name: "present file under absent directory", body: `directories {
    directory "/srv/app" { ensure = "absent" }
  }
  files {
    file "/srv/app/config" { content = "x" }
  }`, wantErr: "inside directory"},
		{name: "present directory under absent directory", body: `directories {
    directory "/srv/app" { ensure = "absent" }
    directory "/srv/app/data" {}
  }`, wantErr: "inside directory"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, "host \"node\" {\n  "+test.body+"\n}\n")
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

func TestCompileGroupResourceDefaultsAndPolicy(t *testing.T) {
	config, err := compileConfig(t, `
host "node" {
  groups {
    group "app" {}
    group "worker" {
      gid       = 1500
      system    = true
      on_remove = "destroy"
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
	groups := program.Hosts[0].Groups
	if len(groups) != 2 {
		t.Fatalf("groups = %#v", groups)
	}
	if group := groups[0]; group.Name != "app" || group.GID != "" || group.System || group.Ensure != "present" || group.OnRemove != "forget" || group.Lifecycle.PreventDestroy {
		t.Fatalf("default group = %#v", group)
	}
	if group := groups[1]; group.Name != "worker" || group.GID != "1500" || !group.System || group.Ensure != "present" || group.OnRemove != "destroy" || !group.Lifecycle.PreventDestroy {
		t.Fatalf("configured group = %#v", group)
	}
}

func TestCompileGroupResourceValidation(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "invalid name", body: "groups {\n    group \"Bad.Name\" {}\n  }", wantErr: "valid Alpine account name"},
		{name: "numeric name", body: "groups {\n    group \"1500\" {}\n  }", wantErr: "valid Alpine account name"},
		{name: "string gid", body: "groups {\n    group \"app\" { gid = \"1500\" }\n  }", wantErr: "non-protected integer"},
		{name: "negative gid", body: "groups {\n    group \"app\" { gid = -1 }\n  }", wantErr: "between 0 and 2147483647"},
		{name: "large gid", body: "groups {\n    group \"app\" { gid = 2147483648 }\n  }", wantErr: "between 0 and 2147483647"},
		{name: "duplicate gid", body: `groups {
    group "app" { gid = 1500 }
    group "worker" { gid = 1500 }
  }`, wantErr: "duplicates explicit gid 1500"},
		{name: "bad ensure", body: "groups {\n    group \"app\" { ensure = \"missing\" }\n  }", wantErr: "present"},
		{name: "present file uses absent group", body: `groups {
    group "app" { ensure = "absent" }
  }
  files {
    file "/srv/app/config" {
      content = "x"
      group   = "app"
    }
  }`, wantErr: "group \"app\" declared absent"},
		{name: "present directory uses absent numeric group", body: `groups {
    group "app" {
      gid    = 1500
      ensure = "absent"
    }
  }
  directories {
    directory "/srv/app" { group = "1500" }
  }`, wantErr: "group \"app\" declared absent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, "host \"node\" {\n  "+test.body+"\n}\n")
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

func TestCompileUserResourceDefaultsAndPolicy(t *testing.T) {
	config, err := compileConfig(t, `
host "node" {
  users {
    user "app" {}
    user "worker" {
      uid       = 1500
      group     = "app"
      home      = "/srv/worker"
      shell     = "/sbin/nologin"
      system    = true
      on_remove = "destroy"
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
	users := program.Hosts[0].Users
	if len(users) != 2 {
		t.Fatalf("users = %#v", users)
	}
	if user := users[0]; user.Name != "app" || user.UID != "" || user.PrimaryGroup != "" || user.Home != "" || user.Shell != "" || user.System || user.Ensure != "present" || user.OnRemove != "forget" || user.Lifecycle.PreventDestroy {
		t.Fatalf("default user = %#v", user)
	}
	if user := users[1]; user.Name != "worker" || user.UID != "1500" || user.PrimaryGroup != "app" || user.Home != "/srv/worker" || user.Shell != "/sbin/nologin" || !user.System || user.Ensure != "present" || user.OnRemove != "destroy" || !user.Lifecycle.PreventDestroy {
		t.Fatalf("configured user = %#v", user)
	}
}

func TestCompileUserResourceValidation(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "root", body: "users {\n    user \"root\" {}\n  }", wantErr: "non-root"},
		{name: "invalid name", body: "users {\n    user \"Bad.Name\" {}\n  }", wantErr: "non-root"},
		{name: "string uid", body: "users {\n    user \"app\" { uid = \"1500\" }\n  }", wantErr: "non-protected integer"},
		{name: "uid zero", body: "users {\n    user \"app\" { uid = 0 }\n  }", wantErr: "uid 0 is reserved"},
		{name: "duplicate uid", body: `users {
    user "app" { uid = 1500 }
    user "worker" { uid = 1500 }
  }`, wantErr: "duplicates explicit uid 1500"},
		{name: "relative home", body: "users {\n    user \"app\" { home = \"home/app\" }\n  }", wantErr: "clean absolute non-root"},
		{name: "root home", body: "users {\n    user \"app\" { home = \"/\" }\n  }", wantErr: "clean absolute non-root"},
		{name: "relative shell", body: "users {\n    user \"app\" { shell = \"bin/ash\" }\n  }", wantErr: "clean absolute path"},
		{name: "invalid group", body: "users {\n    user \"app\" { group = \"bad.group\" }\n  }", wantErr: "valid Alpine group"},
		{name: "absent primary group", body: `groups {
    group "app" { ensure = "absent" }
  }
  users {
    user "worker" { group = "app" }
  }`, wantErr: "primary group \"app\" declared absent"},
		{name: "absent path owner", body: `users {
    user "app" { ensure = "absent" }
  }
  directories {
    directory "/srv/app" { owner = "app" }
  }`, wantErr: "owner \"app\" declared absent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, "host \"node\" {\n  "+test.body+"\n}\n")
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

func TestCompileUserMembershipsAndAuthorizedKeysNormalizeAndDeduplicate(t *testing.T) {
	key := validTestAuthorizedKey(t)
	config, err := compileConfig(t, `
host "node" {
  groups {
    group "app" { gid = 1500 }
    group "wheel" {}
  }
  users {
    user "app" {
      group  = "app"
      groups = ["wheel", "wheel"]
      ssh_authorized_keys = [
        "`+key+` first-comment",
        "`+key+` second-comment",
      ]
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
	user := program.Hosts[0].Users[0]
	if len(user.Groups) != 1 || user.Groups[0].Group != "wheel" || user.Groups[0].Ensure != "present" {
		t.Fatalf("supplementary groups = %#v", user.Groups)
	}
	if len(user.AuthorizedKeys) != 1 || user.AuthorizedKeys[0].Fingerprint == "" || user.AuthorizedKeys[0].KeyType != "ssh-ed25519" || !strings.HasSuffix(user.AuthorizedKeys[0].Line, " first-comment") || user.AuthorizedKeys[0].Ensure != "present" {
		t.Fatalf("authorized keys = %#v", user.AuthorizedKeys)
	}
}

func TestCompileUserMembershipAndAuthorizedKeyValidation(t *testing.T) {
	key := validTestAuthorizedKey(t)
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "primary in supplementary", body: `users { user "app" {
      group = "app"
      groups = ["app"]
    } }`, wantErr: "must not contain the primary group"},
		{name: "managed primary alias in supplementary", body: `groups { group "app" { gid = 1500 } }
  users { user "app" {
    group = "1500"
    groups = ["app"]
  } }`, wantErr: "resolves to its primary group"},
		{name: "absent supplementary group", body: `groups { group "wheel" { ensure = "absent" } }
  users { user "app" { groups = ["wheel"] } }`, wantErr: "membership for user \"app\" uses group \"wheel\" declared absent"},
		{name: "numeric supplementary group", body: `users { user "app" { groups = ["1500"] } }`, wantErr: "valid Alpine group names"},
		{name: "invalid key", body: `users { user "app" { ssh_authorized_keys = ["not-a-key"] } }`, wantErr: "invalid SSH public key"},
		{name: "key options", body: `users { user "app" { ssh_authorized_keys = ["restrict ` + key + `"] } }`, wantErr: "does not support authorized_keys options"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := strings.ReplaceAll(test.body, "users { user", "users {\n    user")
			body = strings.ReplaceAll(body, "groups { group", "groups {\n    group")
			body = strings.ReplaceAll(body, " } }", " }\n  }")
			config, err := compileConfig(t, "host \"node\" {\n  "+body+"\n}\n")
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

func TestCompileRejectsProtectedAuthorizedKeyListWithoutValueDisclosure(t *testing.T) {
	key := validTestAuthorizedKey(t) + " protected-comment"
	config, err := compileConfig(t, `
variable "keys" {
  type      = list(string)
  default   = ["`+key+`"]
  sensitive = true
}
host "node" {
  users {
    user "app" { ssh_authorized_keys = var.keys }
  }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(config)
	if err == nil || !strings.Contains(err.Error(), "non-protected list of strings") || strings.Contains(err.Error(), key) {
		t.Fatalf("Compile() protected key error = %v", err)
	}
}

package merge

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
)

const artifactSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestCompileSelectsNormalizedArtifactArchitecture(t *testing.T) {
	config, err := compileConfig(t, `
component "tool" {
  type = "binary"
  source "amd64" {
    url = "https://example.invalid/tool-amd64"
    sha256 = "`+artifactSHA+`"
  }
  source "arm64" {
    url = "https://example.invalid/tool-arm64"
    sha256 = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
  }
  install { path = "/usr/local/bin/tool" }
}
host "node" {
  platform { architecture = "x86_64" }
  component "cli" { source = component.tool }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	component := program.Hosts[0].Components[0]
	if component.SelectedSource == nil || component.SelectedSource.Architecture != "amd64" || component.SelectedSource.URL != "https://example.invalid/tool-amd64" || component.Install == nil || component.Install.Mode != "0755" {
		t.Fatalf("compiled artifact = %#v", component)
	}
}

func TestCompileReportsNormalizedArchitectureWithoutMatchingSource(t *testing.T) {
	config, err := compileConfig(t, `
component "tool" {
  type = "binary"
  source "arm64" {
    url    = "https://example.invalid/tool-arm64"
    sha256 = "`+artifactSHA+`"
  }
  install { path = "/usr/local/bin/tool" }
}
host "node" {
  platform { architecture = "x86_64" }
  component "cli" { source = component.tool }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(config)
	if err == nil || !strings.Contains(err.Error(), `component["tool"].source`) || !strings.Contains(err.Error(), `host["node"].component["cli"]`) || !strings.Contains(err.Error(), `no source for normalized architecture "amd64"`) || strings.Contains(err.Error(), `normalized architecture "x86_64"`) {
		t.Fatalf("Compile() error = %v, want normalized amd64 source-selection diagnostic", err)
	}
}

func TestCompileScriptsResolveReferencesAndRedactPayloads(t *testing.T) {
	secret := "not-a-real-script-secret"
	config, err := compileConfig(t, `
variable "token" {
  type      = string
  default   = "`+secret+`"
  sensitive = true
}
script "refresh" {
  commands = [["/usr/local/bin/refresh", var.token]]
  outputs  = ["/run/refreshed"]
}
component "first" {
  type = "file"
  source {
    url    = "https://example.invalid/first"
    sha256 = "`+artifactSHA+`"
  }
  install {
    path      = "/etc/first"
    on_change = global.script.refresh
  }
}
component "second" {
  type = "file"
  source {
    url    = "https://example.invalid/second"
    sha256 = "`+artifactSHA+`"
  }
  install {
    path      = "/etc/second"
    on_change = script.refresh
  }
}
host "node" {
  component "first" { source = component.first }
  component "second" { source = component.second }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	root := program.Hosts[0].Scripts["refresh"]
	if !root.Executable || !root.Sensitive || len(root.Commands) != 1 || root.Commands[0][1] != secret || root.DeclarationID != `script["refresh"]` {
		t.Fatalf("root script = %#v", root)
	}
	for _, component := range program.Hosts[0].Components {
		if component.Install == nil || component.Install.OnChange == nil || component.Install.OnChange.Scope != "root" || component.Install.OnChange.DeclarationID != root.DeclarationID {
			t.Fatalf("component reference = %#v", component.Install)
		}
	}
	data, err := json.Marshal(program)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("program JSON leaked script payload: %s", data)
	}
}

func TestCompileComponentLocalScriptUsesInputContext(t *testing.T) {
	config, err := compileConfig(t, `
component "worker" {
  input "service" {
    type    = string
    default = "worker"
  }
  script "reload" {
    content = "rc-service ${input.service} reload"
  }
  type = "file"
  source {
    url    = "https://example.invalid/worker"
    sha256 = "`+artifactSHA+`"
  }
  install {
    path      = "/etc/worker"
    on_change = script.reload
  }
}
host "node" {
  component "worker" { source = component.worker }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	component := program.Hosts[0].Components[0]
	script := component.Scripts["reload"]
	if script.Content != "rc-service worker reload" || script.DeclarationID != `component.worker.script["reload"]` || component.Install.OnChange.Scope != "component" || component.Install.OnChange.DeclarationID != script.DeclarationID {
		t.Fatalf("component script = %#v, reference = %#v", script, component.Install.OnChange)
	}
}

func TestCompileComponentComposesNativeDomains(t *testing.T) {
	config, err := compileConfig(t, `
script "refresh" { commands = [["rc-service", "worker", "reload"]] }
component "worker" {
  input "port" {
    type    = number
    default = 9000
  }
  groups {
    group "worker" { system = true }
  }
  users {
    user "worker" {
      group  = "worker"
      home   = "/var/lib/worker"
      shell  = "/sbin/nologin"
      system = true
    }
  }
  directories {
    directory "/var/lib/worker" {
      owner = "worker"
      group = "worker"
    }
  }
  files {
    file "/etc/worker.conf" {
      content   = "PORT=${input.port}\n"
      on_change = global.script.refresh
    }
  }
  packages {
    package "busybox-extras" {}
  }
  openrc {
    service "worker" {
      command            = "/usr/local/bin/worker"
      command_background = true
      pidfile            = "/run/worker.pid"
    }
  }
  services {
    service "worker" {
      enabled   = true
      state     = "running"
      operation = "restarted"
      package   = "busybox-extras"
      user      = "worker"
      group     = "worker"
    }
  }
}
host "node" {
  component "app" { source = component.worker }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	component := program.Hosts[0].Components[0]
	if len(component.Groups) != 1 || len(component.Users) != 1 || len(component.Directories) != 1 || len(component.Packages) != 1 || len(component.Services) != 1 || len(component.OpenRC) != 1 {
		t.Fatalf("component domains = %#v", component)
	}
	var configFile ir.ManagedFileSpec
	for _, file := range component.Files {
		if file.Path == "/etc/worker.conf" {
			configFile = file
		}
	}
	if configFile.Content != "PORT=9000\n" || configFile.OnChange == nil || configFile.OnChange.Scope != "root" {
		t.Fatalf("component file = %#v", configFile)
	}
}

func TestCompileArtifactValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "offline architecture", body: `
component "tool" {
  type = "file"
  source "amd64" {
    url    = "https://example.invalid/file"
    sha256 = "` + artifactSHA + `"
  }
  install { path = "/etc/tool" }
}
host "node" {
  component "tool" { source = component.tool }
}
`, want: "declare platform.architecture for offline source selection"},
		{name: "checksum", body: `
component "tool" {
  type = "file"
  source {
    url    = "https://example.invalid/file"
    sha256 = "bad"
  }
  install { path = "/etc/tool" }
}
host "node" {
  component "tool" { source = component.tool }
}
`, want: "exactly 64 hexadecimal"},
		{name: "source requires build", body: `
component "tool" {
  type = "source"
  source {
    url    = "https://example.invalid/src"
    sha256 = "` + artifactSHA + `"
  }
  install { path = "/usr/local/bin/tool" }
}
host "node" {
  component "tool" { source = component.tool }
}
`, want: "source components require a build block"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, test.body)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Compile(config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compile() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCompileSourceBuildResolvesIdentityAndProtectedPayload(t *testing.T) {
	content := "int main(void) { return 0; }\n"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	config, err := compileConfig(t, `
component "tool" {
  type = "source"
  input "token" {
    type      = string
    sensitive = true
  }
  build {
    input "source" {
      content     = "`+strings.ReplaceAll(content, "\n", "\\n")+`"
      sha256      = "`+digest+`"
      destination = "main.c"
    }
    command { argv = ["cc", "-Os", "-o", "tool", "main.c"] }
    working_directory   = "."
    environment         = { BUILD_TOKEN = input.token }
    environment_version = "token-v1"
    output              = "tool"
    dependencies        = ["build-base", "musl-dev", "build-base"]
    network             = "none"
  }
  install {
    path = "/usr/local/bin/tool"
    mode = "0750"
  }
}
host "node" {
  platform {
    architecture = "x86_64"
    version      = "3.24"
  }
  component "cli" {
    source = component.tool
    inputs = { token = "top-secret" }
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
	build := program.Hosts[0].Components[0].Build
	if build == nil || len(build.Identity) != 64 || !build.Sensitive || build.Ephemeral || build.Environment["BUILD_TOKEN"] != "top-secret" {
		t.Fatalf("compiled build = %#v", build)
	}
	if got := build.Dependencies; len(got) != 3 || got[0] != "bubblewrap" || got[1] != "build-base" || got[2] != "musl-dev" {
		t.Fatalf("dependencies = %#v", got)
	}
	encoded, err := json.Marshal(program)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "top-secret") {
		t.Fatalf("program JSON leaked protected build environment: %s", encoded)
	}
}

func TestCompileArchiveAndCACertificateArtifacts(t *testing.T) {
	config, err := compileConfig(t, `
component "bundle" {
  type = "archive"
  source {
    url    = "https://example.invalid/bundle.tar.gz"
    sha256 = "`+artifactSHA+`"
  }
  extract { strip_components = 1 }
  install { path = "/opt/bundle" }
}
component "root_ca" {
  type = "ca_certificate"
  source {
    url    = "https://example.invalid/root.crt"
    sha256 = "`+artifactSHA+`"
  }
  install { path = "/usr/local/share/ca-certificates/example-root.crt" }
}
host "node" {
  component "bundle" { source = component.bundle }
  component "root_ca" { source = component.root_ca }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	archive := program.Hosts[0].Components[0]
	certificate := program.Hosts[0].Components[1]
	if archive.ArtifactType != "archive" || archive.Extract == nil || archive.Extract.Format != "tar.gz" || archive.Extract.StripComponents != 1 {
		t.Fatalf("archive = %#v", archive)
	}
	if certificate.ArtifactType != "ca_certificate" || certificate.Install == nil || certificate.Install.Mode != "0644" {
		t.Fatalf("certificate = %#v", certificate)
	}
	if len(certificate.Packages) != 1 || certificate.Packages[0].Name != "ca-certificates" || certificate.Packages[0].Ensure != "present" {
		t.Fatalf("certificate packages = %#v", certificate.Packages)
	}
}

func TestCompileResolvesPrebuiltArtifactSourcesPerInstance(t *testing.T) {
	shaAlpha := strings.Repeat("A", 64)
	shaBeta := strings.Repeat("B", 64)
	tests := []struct {
		name       string
		kind       string
		urlAlpha   string
		urlBeta    string
		extra      string
		install    string
		wantFormat string
	}{
		{name: "binary", kind: "binary", urlAlpha: "https://alpha.invalid/tool", urlBeta: "https://beta.invalid/tool", install: `install { path = "/usr/local/bin/tool" }`},
		{name: "file", kind: "file", urlAlpha: "https://alpha.invalid/tool.conf", urlBeta: "https://beta.invalid/tool.conf", install: `install { path = "/etc/tool.conf" }`},
		{name: "archive", kind: "archive", urlAlpha: "https://alpha.invalid/tool.tar.gz?mirror=alpha", urlBeta: "https://beta.invalid/tool.tgz?mirror=beta", extra: `extract { strip_components = 1 }`, install: `install { path = "/opt/tool" }`, wantFormat: "tar.gz"},
		{name: "ca certificate", kind: "ca_certificate", urlAlpha: "https://alpha.invalid/root.crt", urlBeta: "https://beta.invalid/root.crt", install: `install { path = "/usr/local/share/ca-certificates/root.crt" }`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, fmt.Sprintf(`
component "payload" {
  input "url" {
    type      = string
    sensitive = true
  }
  input "sha256" {
    type      = string
    ephemeral = true
  }
  type = %q
  source {
    url    = input.url
    sha256 = input.sha256
  }
  %s
  %s
}
host "alpha" {
  component "payload" {
    source = component.payload
    inputs = { url = %q, sha256 = %q }
  }
}
host "beta" {
  component "payload" {
    source = component.payload
    inputs = { url = %q, sha256 = %q }
  }
}
`, test.kind, test.extra, test.install, test.urlAlpha, shaAlpha, test.urlBeta, shaBeta))
			if err != nil {
				t.Fatal(err)
			}
			program, err := Compile(config)
			if err != nil {
				t.Fatal(err)
			}
			if len(program.Hosts) != 2 {
				t.Fatalf("hosts = %#v", program.Hosts)
			}
			wantURLs := map[string]string{"alpha": test.urlAlpha, "beta": test.urlBeta}
			wantSHAs := map[string]string{"alpha": strings.ToLower(shaAlpha), "beta": strings.ToLower(shaBeta)}
			for _, host := range program.Hosts {
				component := host.Components[0]
				source := component.SelectedSource
				if source == nil || source.URL != wantURLs[host.Name] || source.SHA256 != wantSHAs[host.Name] {
					t.Fatalf("host %s source = %#v", host.Name, source)
				}
				if !source.URLSensitive || source.URLEphemeral || source.SHA256Sensitive || !source.SHA256Ephemeral {
					t.Fatalf("host %s source marks = %#v", host.Name, source)
				}
				if source.URLSource.Path != `component["payload"].source.url` || source.SHA256Source.Path != `component["payload"].source.sha256` {
					t.Fatalf("host %s source refs = %#v / %#v", host.Name, source.URLSource, source.SHA256Source)
				}
				if test.wantFormat != "" && (component.Extract == nil || component.Extract.Format != test.wantFormat) {
					t.Fatalf("host %s extract = %#v", host.Name, component.Extract)
				}
			}
			templateSource := program.Components["payload"].Sources[""]
			if templateSource.URL != "" || templateSource.SHA256 != "" || templateSource.URLSource.Path == "" || templateSource.SHA256Source.Path == "" {
				t.Fatalf("unresolved template source = %#v", templateSource)
			}
		})
	}
}

func TestCompileProtectedArtifactSourcesShareOneHostDownloaderPackage(t *testing.T) {
	config, err := compileConfig(t, `
component "first" {
  input "url" {
    type      = string
    sensitive = true
  }
  type = "binary"
  source {
    url    = input.url
    sha256 = "`+artifactSHA+`"
  }
  install { path = "/usr/local/bin/first" }
}
component "second" {
  input "sha256" {
    type      = string
    ephemeral = true
  }
  type = "file"
  source {
    url    = "https://example.invalid/second"
    sha256 = input.sha256
  }
  install { path = "/etc/second" }
}
host "node" {
  component "first" {
    source = component.first
    inputs = { url = "https://example.invalid/first?token=protected" }
  }
  component "second" {
    source = component.second
    inputs = { sha256 = "`+artifactSHA+`" }
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
	host := program.Hosts[0]
	if len(host.Packages) != 1 || host.Packages[0].Name != "wget" || host.Packages[0].WorldIntent != "wget" || host.Packages[0].Ensure != "present" || host.Packages[0].RepositoryTag != "" {
		t.Fatalf("protected artifact downloader packages = %#v", host.Packages)
	}
	for _, component := range host.Components {
		for _, pkg := range component.Packages {
			if pkg.Name == "wget" {
				t.Fatalf("protected artifact downloader duplicated in component %q: %#v", component.Name, component.Packages)
			}
		}
	}
}

func TestCompileProtectedArtifactSourceReusesComponentDownloaderPackage(t *testing.T) {
	config, err := compileConfig(t, `
component "tool" {
  input "url" {
    type      = string
    sensitive = true
  }
  type = "binary"
  source {
    url    = input.url
    sha256 = "`+artifactSHA+`"
  }
  install { path = "/usr/local/bin/tool" }
  packages {
    package "wget" {}
  }
}
host "node" {
  component "tool" {
    source = component.tool
    inputs = { url = "https://example.invalid/tool?token=protected" }
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
	host := program.Hosts[0]
	if len(host.Packages) != 0 || len(host.Components) != 1 || len(host.Components[0].Packages) != 1 || host.Components[0].Packages[0].Name != "wget" || host.Components[0].Packages[0].Ensure != "present" {
		t.Fatalf("reused protected artifact downloader = host %#v, component %#v", host.Packages, host.Components)
	}
}

func TestCompileProtectedArtifactSourceRejectsAbsentDownloaderPackage(t *testing.T) {
	tests := []struct {
		name       string
		component  string
		host       string
		wantSource string
	}{
		{name: "host declaration", host: `packages {
    package "wget" {
      ensure = "absent"
    }
  }`, wantSource: `host["node"].packages.package["wget"]`},
		{name: "component declaration", component: `packages {
    package "wget" {
      ensure = "absent"
    }
  }`, wantSource: `component["tool"].packages.package["wget"]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, `
component "tool" {
  input "url" {
    type      = string
    sensitive = true
  }
  type = "binary"
  source {
    url    = input.url
    sha256 = "`+artifactSHA+`"
  }
  install { path = "/usr/local/bin/tool" }
  `+test.component+`
}
host "node" {
  `+test.host+`
  component "tool" {
    source = component.tool
    inputs = { url = "https://example.invalid/tool?token=protected" }
  }
}
`)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Compile(config)
			if err == nil || !strings.Contains(err.Error(), "protected component artifact sources require APK package wget to be present") || !strings.Contains(err.Error(), test.wantSource) {
				t.Fatalf("Compile() error = %v, want absent downloader diagnostic at %q", err, test.wantSource)
			}
		})
	}
}

func TestCompileSelectsResolvedSourcesForOfflineAndObservedArchitectures(t *testing.T) {
	compileBody := func(platformAlpha, platformBeta string) string {
		return `
component "tool" {
  input "base_url" { type = string }
  type = "binary"
  source "amd64" {
    url    = "${input.base_url}/tool-amd64"
    sha256 = "` + artifactSHA + `"
  }
  source "arm64" {
    url    = "${input.base_url}/tool-arm64"
    sha256 = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
  }
  install { path = "/usr/local/bin/tool" }
}
host "alpha" {
  ` + platformAlpha + `
  component "tool" {
    source = component.tool
    inputs = { base_url = "https://alpha.invalid" }
  }
}
host "beta" {
  ` + platformBeta + `
  component "tool" {
    source = component.tool
    inputs = { base_url = "https://beta.invalid" }
  }
}
`
	}
	tests := []struct {
		name          string
		platformAlpha string
		platformBeta  string
		facts         map[string]ir.HostFacts
	}{
		{
			name:          "offline declared",
			platformAlpha: `platform { architecture = "x86_64" }`,
			platformBeta:  `platform { architecture = "aarch64" }`,
		},
		{
			name: "online observed",
			facts: map[string]ir.HostFacts{
				"alpha": {OSID: "alpine", Version: "3.24.1", Branch: "v3.24", Architecture: "amd64", NativeArchitecture: "x86_64", KernelArchitecture: "x86_64", Libc: "musl"},
				"beta":  {OSID: "alpine", Version: "3.24.1", Branch: "v3.24", Architecture: "arm64", NativeArchitecture: "aarch64", KernelArchitecture: "aarch64", Libc: "musl"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, compileBody(test.platformAlpha, test.platformBeta))
			if err != nil {
				t.Fatal(err)
			}
			program, err := CompileWithOptions(config, CompileOptions{HostFacts: test.facts})
			if err != nil {
				t.Fatal(err)
			}
			wantArchitecture := map[string]string{"alpha": "amd64", "beta": "arm64"}
			for _, host := range program.Hosts {
				source := host.Components[0].SelectedSource
				wantURL := "https://" + host.Name + ".invalid/tool-" + wantArchitecture[host.Name]
				if source == nil || source.Architecture != wantArchitecture[host.Name] || source.URL != wantURL {
					t.Fatalf("host %s selected source = %#v", host.Name, source)
				}
			}
		})
	}
}

func TestCompileArtifactSourceDiagnosticsIncludeTemplateFieldAndMount(t *testing.T) {
	const sentinel = "not-a-real-protected-artifact-sentinel"
	configBody := func(inputs, urlExpression, shaExpression, mountedInputs string) string {
		return fmt.Sprintf(`
component "tool" {
  %s
  type = "file"
  source {
    url    = %s
    sha256 = %s
  }
  install { path = "/etc/tool" }
}
host "node" {
  component "mounted" {
    source = component.tool
    %s
  }
}
`, inputs, urlExpression, shaExpression, mountedInputs)
	}
	validURL := `"https://example.invalid/tool"`
	validSHA := `"` + artifactSHA + `"`
	tests := []struct {
		name          string
		inputs        string
		urlExpression string
		shaExpression string
		mountedInputs string
		field         string
		want          string
	}{
		{name: "missing required input", inputs: `input "value" { type = string }`, urlExpression: `input.value`, shaExpression: validSHA, field: `component["tool"].input["value"]`, want: `input "value" is required`},
		{name: "mistyped URL", inputs: `input "value" { type = any }`, urlExpression: `input.value`, shaExpression: validSHA, mountedInputs: `inputs = { value = 42 }`, field: `component["tool"].source.url`, want: "url must be a string"},
		{name: "null URL", inputs: `input "value" { type = string }`, urlExpression: `input.value`, shaExpression: validSHA, mountedInputs: `inputs = { value = null }`, field: `component["tool"].source.url`, want: "url must not be null"},
		{name: "empty URL", inputs: `input "value" { type = string }`, urlExpression: `input.value`, shaExpression: validSHA, mountedInputs: `inputs = { value = "" }`, field: `component["tool"].source.url`, want: "url must be a non-empty string"},
		{name: "mistyped SHA", inputs: `input "value" { type = any }`, urlExpression: validURL, shaExpression: `input.value`, mountedInputs: `inputs = { value = 42 }`, field: `component["tool"].source.sha256`, want: "sha256 must be a string"},
		{name: "null SHA", inputs: `input "value" { type = string }`, urlExpression: validURL, shaExpression: `input.value`, mountedInputs: `inputs = { value = null }`, field: `component["tool"].source.sha256`, want: "sha256 must not be null"},
		{name: "empty SHA", inputs: `input "value" { type = string }`, urlExpression: validURL, shaExpression: `input.value`, mountedInputs: `inputs = { value = "" }`, field: `component["tool"].source.sha256`, want: "sha256 must be a non-empty string"},
		{name: "unknown traversal", urlExpression: `input.missing`, shaExpression: validSHA, field: `component["tool"].source.url`, want: "artifact source expression failed to evaluate"},
		{name: "malformed URL", inputs: `input "value" { type = string }`, urlExpression: `input.value`, shaExpression: validSHA, mountedInputs: `inputs = { value = "not-an-http-url" }`, field: `component["tool"].source.url`, want: "absolute http(s) URL"},
		{name: "malformed SHA", inputs: `input "value" { type = string }`, urlExpression: validURL, shaExpression: `input.value`, mountedInputs: `inputs = { value = "bad" }`, field: `component["tool"].source.sha256`, want: "exactly 64 hexadecimal"},
		{name: "nested protected failure", inputs: `input "payload" {
  type      = object({ token = string })
  sensitive = true
}`, urlExpression: `tonumber(input.payload.token)`, shaExpression: validSHA, mountedInputs: `inputs = { payload = { token = "` + sentinel + `" } }`, field: `component["tool"].source.url`, want: "protected artifact source expression failed to evaluate"},
		{name: "dynamic protected failure", inputs: `input "secrets" {
  type      = map(string)
  ephemeral = true
}
input "key" { type = string }`, urlExpression: `tonumber(input.secrets[input.key])`, shaExpression: validSHA, mountedInputs: `inputs = { secrets = { token = "` + sentinel + `" }, key = "token" }`, field: `component["tool"].source.url`, want: "protected artifact source expression failed to evaluate"},
		{name: "validation before source", inputs: `input "value" {
  type      = string
  sensitive = true
  validation {
    condition     = startswith(input.value, "https://")
    error_message = "must use https"
  }
}`, urlExpression: `input.value`, shaExpression: `"bad"`, mountedInputs: `inputs = { value = "` + sentinel + `" }`, field: `component["tool"].input["value"]`, want: "validation failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, configBody(test.inputs, test.urlExpression, test.shaExpression, test.mountedInputs))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Compile(config)
			if err == nil || !strings.Contains(err.Error(), test.field) || !strings.Contains(err.Error(), `host["node"].component["mounted"]`) || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), sentinel) {
				t.Fatalf("Compile() error = %v, want field %q, mount, and %q without sentinel", err, test.field, test.want)
			}
		})
	}
}

func TestCompileRedactsProtectedComponentAssertionEvaluationFailure(t *testing.T) {
	const sentinel = "not-a-real-protected-assertion-sentinel"
	config, err := compileConfig(t, `
component "tool" {
  input "token" {
    type      = string
    sensitive = true
  }
  assert {
    condition     = tonumber(input.token) > 0
    error_message = "token must be numeric"
  }
  type = "file"
  source {
    url    = "not-an-http-url"
    sha256 = "`+artifactSHA+`"
  }
  install { path = "/etc/tool" }
}
host "node" {
  component "mounted" {
    source = component.tool
    inputs = { token = "`+sentinel+`" }
  }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(config)
	if err == nil || !strings.Contains(err.Error(), `component["tool"].assert.condition`) || !strings.Contains(err.Error(), `host["node"].component["mounted"]`) || !strings.Contains(err.Error(), "protected component assertion condition failed to evaluate") || strings.Contains(err.Error(), sentinel) || strings.Contains(err.Error(), "absolute http(s) URL") {
		t.Fatalf("Compile() error = %v", err)
	}
}

func TestCompileAllowsUnmountedArtifactInputTemplateAndChecksStaticShape(t *testing.T) {
	config, err := compileConfig(t, `
component "tool" {
  input "url" { type = string }
  input "sha256" { type = string }
  type = "file"
  source {
    url    = input.url
    sha256 = input.sha256
  }
  install { path = "/etc/tool" }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	source := program.Components["tool"].Sources[""]
	if source.URL != "" || source.SHA256 != "" || source.URLSource.Path != `component["tool"].source.url` || source.SHA256Source.Path != `component["tool"].source.sha256` {
		t.Fatalf("unmounted template source = %#v", source)
	}

	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "missing URL", body: `sha256 = "` + artifactSHA + `"`, want: `component["tool"].source.url`},
		{name: "missing SHA", body: `url = "https://example.invalid/tool"`, want: `component["tool"].source.sha256`},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, `
component "tool" {
  type = "file"
  source { `+test.body+` }
  install { path = "/etc/tool" }
}
`)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Compile(config)
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "is required") {
				t.Fatalf("Compile() error = %v, want static field %q", err, test.want)
			}
		})
	}
}

func TestCompileValidatesEagerArtifactSourcesOnUnmountedTemplates(t *testing.T) {
	tests := []struct {
		name      string
		prefix    string
		url       string
		sha256    string
		fieldName string
		want      string
	}{
		{
			name: "literal URL", url: `"relative/tool"`, sha256: `"` + artifactSHA + `"`,
			fieldName: "url", want: "source URL must be an absolute http(s) URL without credentials or a fragment",
		},
		{
			name: "eager local URL", prefix: `locals { artifact_url = "https://user@example.invalid/tool" }`, url: `local.artifact_url`, sha256: `"` + artifactSHA + `"`,
			fieldName: "url", want: "source URL must be an absolute http(s) URL without credentials or a fragment",
		},
		{
			name: "literal SHA", url: `"https://example.invalid/tool"`, sha256: `"bad"`,
			fieldName: "sha256", want: "source SHA-256 must be exactly 64 hexadecimal characters",
		},
		{
			name: "deferred URL does not defer eager SHA", url: `input.url`, sha256: `"bad"`,
			fieldName: "sha256", want: "source SHA-256 must be exactly 64 hexadecimal characters",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, test.prefix+`
component "tool" {
  input "url" {
    type    = string
    default = "relative/default"
  }
  type = "file"
  source {
    url    = `+test.url+`
    sha256 = `+test.sha256+`
  }
  install { path = "/etc/tool" }
}
`)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Compile(config)
			field := componentArtifactSourceField(config.Components["tool"].Sources[""], test.fieldName)
			want := fmt.Sprintf("%s:%d:%s: %s", field.File, field.Line, field.Path, test.want)
			if err == nil || err.Error() != want {
				t.Fatalf("Compile() error = %v, want %q", err, want)
			}
		})
	}

	config, err := compileConfig(t, `
component "tool" {
  input "url" {
    type    = string
    default = "relative/default"
  }
  input "sha256" {
    type    = string
    default = "bad"
  }
  type = "file"
  source {
    url    = input.url
    sha256 = lower(input.sha256)
  }
  install { path = "/etc/tool" }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(config); err != nil {
		t.Fatalf("Compile() rejected unmounted input-dependent source: %v", err)
	}
}

func TestCompileValidatesAllResolvedSourcesBeforeSelection(t *testing.T) {
	tests := []struct {
		name  string
		arm64 string
		field string
		want  string
	}{
		{name: "unselected URL", arm64: `url    = "not-an-http-url"
sha256 = "` + artifactSHA + `"`, field: `source["arm64"].url`, want: "absolute http(s) URL"},
		{name: "unselected SHA", arm64: `url    = "https://example.invalid/tool-arm64"
sha256 = "bad"`, field: `source["arm64"].sha256`, want: "exactly 64 hexadecimal"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, `
component "tool" {
  type = "binary"
  source "amd64" {
    url    = "https://example.invalid/tool-amd64"
    sha256 = "`+artifactSHA+`"
  }
  source "arm64" {
    `+test.arm64+`
  }
  install { path = "/usr/local/bin/tool" }
}
host "node" {
  platform { architecture = "x86_64" }
  component "mounted" { source = component.tool }
}
`)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Compile(config)
			if err == nil || !strings.Contains(err.Error(), test.field) || !strings.Contains(err.Error(), `host["node"].component["mounted"]`) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compile() error = %v, want %q and %q", err, test.field, test.want)
			}
		})
	}
}

func TestCompileInfersArchiveFormatOnlyFromSelectedResolvedSource(t *testing.T) {
	config, err := compileConfig(t, `
component "bundle" {
  type = "archive"
  source "amd64" {
    url    = "https://example.invalid/bundle.tar.gz?mirror=amd64"
    sha256 = "`+artifactSHA+`"
  }
  source "arm64" {
    url    = "https://example.invalid/unselected.zip"
    sha256 = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
  }
  extract { strip_components = 1 }
  install { path = "/opt/bundle" }
}
host "node" {
  platform { architecture = "x86_64" }
  component "bundle" { source = component.bundle }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	component := program.Hosts[0].Components[0]
	if component.SelectedSource == nil || component.SelectedSource.Architecture != "amd64" || component.Extract == nil || component.Extract.Format != "tar.gz" {
		t.Fatalf("compiled archive = %#v", component)
	}
}

func TestCompileSortsUnknownMountedInputs(t *testing.T) {
	config, err := compileConfig(t, `
component "tool" {
  type = "file"
  source {
    url    = "https://example.invalid/tool"
    sha256 = "`+artifactSHA+`"
  }
  install { path = "/etc/tool" }
}
host "node" {
  component "tool" {
    source = component.tool
    inputs = { zeta = 1, alpha = 2 }
  }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(config)
	if err == nil || !strings.Contains(err.Error(), `unknown input "alpha"`) {
		t.Fatalf("Compile() error = %v, want alphabetically first unknown input", err)
	}
}

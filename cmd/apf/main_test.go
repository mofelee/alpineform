package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	corebackend "github.com/mofelee/alpineform/internal/core/backend"
	coreengine "github.com/mofelee/alpineform/internal/core/engine"
	coregraph "github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

func TestVersionUsesAPFProductName(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"version"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "apf ") {
		t.Fatalf("version output = %q", output.String())
	}
}

func TestHelpHasOnlyAlpineFormNames(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"help"}, &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{"AlpineForm", "apf validate", "*.apf.hcl"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help output missing %q:\n%s", want, text)
		}
	}
	for _, forbidden := range []string{"DebianForm", "dbf", ".dbf.hcl"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("help output contains %q:\n%s", forbidden, text)
		}
	}
}

func TestApplyRejectsPositionalArguments(t *testing.T) {
	err := runApplyWithRuntime([]string{"unexpected"}, &bytes.Buffer{}, t.TempDir(), nil, defaultOnlineRuntime())
	if err == nil || !strings.Contains(err.Error(), "does not accept positional arguments") {
		t.Fatalf("apply argument error = %v", err)
	}
}

func TestOnlineCommandsRejectInvalidParallelism(t *testing.T) {
	dir := t.TempDir()
	for _, test := range []struct {
		name string
		run  func() error
	}{
		{name: "plan", run: func() error {
			return runPlanWithRuntime([]string{"--parallel", "0"}, &bytes.Buffer{}, dir, nil, defaultOnlineRuntime())
		}},
		{name: "apply", run: func() error {
			return runApplyWithRuntime([]string{"--parallel", "0"}, &bytes.Buffer{}, dir, nil, defaultOnlineRuntime())
		}},
		{name: "check", run: func() error {
			return runCheckWithRuntime([]string{"--parallel", "0"}, &bytes.Buffer{}, dir, nil, defaultOnlineRuntime())
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); err == nil || !strings.Contains(err.Error(), "parallelism must be at least 1") {
				t.Fatalf("invalid parallelism error = %v", err)
			}
		})
	}
}

func TestValidateLoadsAlpineFormVariableInputs(t *testing.T) {
	dir := t.TempDir()
	config := `
variable "region" {
  type = string
  validation {
    condition     = var.region == "cli"
    error_message = "precedence is incorrect"
  }
}
locals {
  selected = var.region
}
`
	for name, content := range map[string]string{
		"main.apf.hcl":       config,
		"alpineform.apfvars": `region = "default-file"`,
		"10.auto.apfvars":    `region = "automatic"`,
		"prod.apfvars":       `region = "explicit"`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	err := runValidate(
		[]string{"-var-file", "prod.apfvars", "-var", "region=cli"},
		&output,
		dir,
		[]string{"APF_VAR_region=environment", "DBF_VAR_region=foreign"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "Configuration is valid: 1 file(s), 1 variable(s), 1 local(s).\n" {
		t.Fatalf("validate output = %q", got)
	}
}

func TestValidateDoesNotPrintSensitiveValues(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.apf.hcl"), []byte(`
variable "token" {
  type      = number
  sensitive = true
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	secret := "not-a-real-sensitive-value"
	var output bytes.Buffer
	err := runValidate([]string{"-var", "token=" + secret}, &output, dir, nil)
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(output.String(), secret) {
		t.Fatalf("validate error = %v, output = %q", err, output.String())
	}
}

func TestFmtValidatesBeforeWritingAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	validPath := filepath.Join(dir, "valid.apf.hcl")
	valid := "variable \"region\"{type=string}\nlocals{selected=var.region}\n"
	if err := os.WriteFile(validPath, []byte(valid), 0640); err != nil {
		t.Fatal(err)
	}
	var first bytes.Buffer
	if err := runFmt([]string{"-f", validPath}, &first, dir); err != nil {
		t.Fatal(err)
	}
	if got := first.String(); got != "formatted 1 file(s)\n" {
		t.Fatalf("first fmt output = %q", got)
	}
	formatted, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(formatted, []byte(valid)) || !bytes.Contains(formatted, []byte("type = string")) {
		t.Fatalf("formatted file = %q", formatted)
	}
	info, err := os.Stat(validPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0640 {
		t.Fatalf("formatted mode = %o, want 640", got)
	}
	var second bytes.Buffer
	if err := runFmt([]string{"-f", validPath}, &second, dir); err != nil {
		t.Fatal(err)
	}
	if got := second.String(); got != "formatted 0 file(s)\n" {
		t.Fatalf("second fmt output = %q", got)
	}

	invalidPath := filepath.Join(dir, "invalid.apf.hcl")
	invalid := []byte("apt {}\n")
	if err := os.WriteFile(invalidPath, invalid, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runFmt([]string{"-f", invalidPath}, &bytes.Buffer{}, dir); err == nil || !strings.Contains(err.Error(), "unknown top-level block") {
		t.Fatalf("invalid fmt error = %v", err)
	}
	after, err := os.ReadFile(invalidPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, invalid) {
		t.Fatalf("fmt changed semantically invalid input: %q", after)
	}

	compileInvalidPath := filepath.Join(dir, "compile-invalid.apf.hcl")
	compileInvalid := []byte("host \"node\" {\n  component \"app\" { source = component.missing }\n}\n")
	if err := os.WriteFile(compileInvalidPath, compileInvalid, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runFmt([]string{"-f", compileInvalidPath}, &bytes.Buffer{}, dir); err == nil || !strings.Contains(err.Error(), "unknown component.missing") {
		t.Fatalf("compile-invalid fmt error = %v", err)
	}
	after, err = os.ReadFile(compileInvalidPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, compileInvalid) {
		t.Fatalf("fmt changed compiler-invalid input: %q", after)
	}
}

func TestVariableInspectIsStableAndRedactsProtectedDefaults(t *testing.T) {
	dir := t.TempDir()
	config := `
variable "zeta" {
  type        = list(number)
  default     = [443, 80]
  description = "public ports"
}

variable "secret" {
  type      = string
  default   = "not-a-real-secret-token"
  sensitive = true
}
variable "session" {
  type      = string
  default   = "not-a-real-ephemeral-token"
  ephemeral = true
}
variable "alpha" {
  type       = string
  nullable   = false
  deprecated = "use zeta instead"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.apf.hcl"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runVariableInspect([]string{"inspect"}, &output, dir, nil); err == nil {
		t.Fatal("runVariableInspect unexpectedly accepted nested inspect argument")
	}
	output.Reset()
	if err := runVariable([]string{"inspect"}, &output, dir, nil); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, secret := range []string{"not-a-real-secret-token", "not-a-real-ephemeral-token"} {
		if strings.Contains(text, secret) {
			t.Fatalf("inspect leaked %q:\n%s", secret, text)
		}
	}
	for _, want := range []string{`"default": "<sensitive>"`, `"default": "<ephemeral>"`, `"deprecated": "use zeta instead"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("inspect output missing %q:\n%s", want, text)
		}
	}
	var decoded struct {
		Variables []struct {
			Name string `json:"name"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{"alpha", "secret", "session", "zeta"}
	for i, want := range wantOrder {
		if decoded.Variables[i].Name != want {
			t.Fatalf("variable order = %#v", decoded.Variables)
		}
	}
}

func TestValidateRunsModelCompiler(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.apf.hcl"), []byte(`
host "node" {
  component "app" { source = component.missing }
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	err := runValidate(nil, &bytes.Buffer{}, dir, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown component.missing") {
		t.Fatalf("validate error = %v", err)
	}
}

func TestComponentInspectRedactsProtectedDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.apf.hcl"), []byte(`
component "app" {
  description = "Example application."
  input "port" {
    type    = number
    default = 8080
  }
  input "token" {
    type      = string
    default   = "not-a-real-component-secret"
    sensitive = true
  }
  input "session" {
    type      = string
    default   = "not-a-real-ephemeral-secret"
    ephemeral = true
  }
}

`), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runComponent([]string{"inspect", "-f", "main.apf.hcl", "app"}, &output, dir); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, secret := range []string{"not-a-real-component-secret", "not-a-real-ephemeral-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("component inspect leaked %q:\n%s", secret, text)
		}
	}
	for _, want := range []string{`"name": "app"`, `"default": 8080`, `"default": "<sensitive>"`, `"default": "<ephemeral>"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("component inspect missing %q:\n%s", want, text)
		}
	}
}

func TestOfflinePlanIsStableRedactedAndWritesHTML(t *testing.T) {
	dir := t.TempDir()
	config := `
component "app" {
  input "token" {
    type      = string
    default   = "not-a-real-plan-secret"
    sensitive = true
    ephemeral = true
  }
}
host "node" {
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }
  component "app" { source = component.app }
}
`
	configPath := filepath.Join(dir, "main.apf.hcl")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	htmlPath := filepath.Join(dir, "out", "plan.html")
	args := []string{"--offline", "--format", "json", "--html", htmlPath}
	var first bytes.Buffer
	if err := runPlan(args, &first, dir, nil); err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if err := runPlan(args, &second, dir, nil); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatalf("offline JSON plan drifted:\n%s\n%s", first.String(), second.String())
	}
	if strings.Contains(first.String(), "not-a-real-plan-secret") || strings.Contains(first.String(), "\x1b[") {
		t.Fatalf("JSON plan leaked protected/color content:\n%s", first.String())
	}
	var document struct {
		FormatVersion string `json:"format_version"`
		Summary       struct {
			Managed int `json:"managed_resources"`
			Nodes   int `json:"graph_nodes"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(first.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.FormatVersion != "alpineform.plan.alpha1" || document.Summary.Managed != 0 || document.Summary.Nodes != 3 {
		t.Fatalf("offline document = %#v", document)
	}
	htmlData, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(htmlData), "not-a-real-plan-secret") || !strings.Contains(string(htmlData), "AlpineForm offline plan") || !strings.Contains(string(htmlData), "host.node.component.app") {
		t.Fatalf("HTML plan = %s", htmlData)
	}

	var colored bytes.Buffer
	if err := runPlan([]string{"--offline", "--color", "always"}, &colored, dir, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(colored.String(), "\x1b[") || strings.Contains(colored.String(), "not-a-real-plan-secret") {
		t.Fatalf("colored text plan = %q", colored.String())
	}
	var plain bytes.Buffer
	if err := runPlan([]string{"--offline", "--color", "auto"}, &plain, dir, []string{"NO_COLOR="}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "\x1b[") {
		t.Fatalf("NO_COLOR plan contains ANSI: %q", plain.String())
	}
}

func TestPlanSupportsOnlineAndDoesNotOverwriteInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.apf.hcl")
	original := []byte("host \"node\" {}\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	transport := newFakeOnlineTransport("alpine")
	var online bytes.Buffer
	if err := runPlanWithRuntime(nil, &online, dir, nil, fakeOnlineRuntime(transport, "")); err != nil {
		t.Fatalf("online plan error = %v", err)
	}
	if !strings.Contains(online.String(), "Online plan:") {
		t.Fatalf("online plan output = %s", online.String())
	}
	if err := runPlan([]string{"--offline", "--html", path}, &bytes.Buffer{}, dir, nil); err == nil || !strings.Contains(err.Error(), "would overwrite configuration input") {
		t.Fatalf("overwrite plan error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("plan changed input: %q", after)
	}
}

type fakeOnlineTransport struct {
	osID              string
	state             corestate.State
	events            []string
	stateWrites       int
	fileExists        bool
	fileContent       []byte
	fileOwner         string
	fileGroup         string
	fileMode          string
	directoryExists   bool
	directoryNonEmpty bool
	directoryOwner    string
	directoryGroup    string
	directoryMode     string
	groupExists       bool
	groupGID          string
}

func newFakeOnlineTransport(osID string) *fakeOnlineTransport {
	return &fakeOnlineTransport{osID: osID, state: corestate.Empty("node")}
}

func (transport *fakeOnlineTransport) Read(_ context.Context, command string) (string, error) {
	transport.events = append(transport.events, "facts:"+command)
	switch command {
	case "cat /etc/os-release":
		return "ID=" + transport.osID + "\nVERSION_ID=3.24.1\n", nil
	case "apk --print-arch":
		return "x86_64\n", nil
	case "uname -m":
		return "x86_64\n", nil
	default:
		return "", fmt.Errorf("unexpected fact command %q", command)
	}
}

func (transport *fakeOnlineTransport) Run(_ context.Context, command corebackend.Command) ([]byte, error) {
	transport.events = append(transport.events, command.Name)
	switch command.Name {
	case "state.read":
		return corestate.Encode(transport.state)
	case "state.write":
		written, err := corestate.Decode(command.Stdin, "node")
		if err != nil {
			return nil, err
		}
		transport.state = written
		transport.stateWrites++
		return nil, nil
	case "lock.acquire":
		return []byte("acquired\n"), nil
	case "lock.release":
		return []byte("released\n"), nil
	case "inspect.file":
		if !transport.fileExists {
			return []byte("missing\n"), nil
		}
		sum := sha256.Sum256(transport.fileContent)
		return []byte(fmt.Sprintf("file\n%s\n0\n%s\n0\n%s\n%d\n%x\n", transport.fileOwner, transport.fileGroup, strings.TrimPrefix(transport.fileMode, "0"), len(transport.fileContent), sum)), nil
	case "apply.file":
		transport.fileExists = true
		transport.fileContent = append([]byte(nil), command.Stdin...)
		transport.fileOwner = command.Arguments[1]
		transport.fileGroup = command.Arguments[2]
		transport.fileMode = command.Arguments[3]
		return nil, nil
	case "delete.file":
		transport.fileExists = false
		transport.fileContent = nil
		return nil, nil
	case "inspect.directory":
		if !transport.directoryExists {
			return []byte("missing\n"), nil
		}
		return []byte(fmt.Sprintf("directory\n%s\n0\n%s\n0\n%s\n", transport.directoryOwner, transport.directoryGroup, strings.TrimPrefix(transport.directoryMode, "0"))), nil
	case "apply.directory":
		transport.directoryExists = true
		transport.directoryOwner = command.Arguments[1]
		transport.directoryGroup = command.Arguments[2]
		transport.directoryMode = command.Arguments[3]
		return nil, nil
	case "delete.directory":
		if transport.directoryNonEmpty && command.Arguments[1] != "true" {
			return nil, fmt.Errorf("directory not empty")
		}
		transport.directoryExists = false
		transport.directoryNonEmpty = false
		return nil, nil
	case "inspect.group":
		if !transport.groupExists {
			return []byte("missing\n"), nil
		}
		return []byte("group\n" + transport.groupGID + "\n"), nil
	case "apply.group":
		transport.groupExists = true
		if command.Arguments[1] != "" {
			transport.groupGID = command.Arguments[1]
		} else if transport.groupGID == "" {
			transport.groupGID = "1000"
		}
		return nil, nil
	case "delete.group":
		transport.groupExists = false
		transport.groupGID = ""
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected backend command %q", command.Name)
	}
}

func fakeOnlineRuntime(transport *fakeOnlineTransport, input string) onlineRuntime {
	return onlineRuntime{
		Context: context.Background(),
		Stdin:   strings.NewReader(input),
		NewRunner: func(ir.HostSpec) (onlineRunner, error) {
			return transport, nil
		},
		Provider: unavailableProvider{},
	}
}

func fakeNativeRuntime(transport *fakeOnlineTransport, input string) onlineRuntime {
	runtime := fakeOnlineRuntime(transport, input)
	runtime.Provider = nil
	return runtime
}

func writeOnlineHostConfig(t *testing.T, dir string) {
	t.Helper()
	content := `
host "node" {
  ssh { host = "alpine-alias" }
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.apf.hcl"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestOnlinePlanDiscoversFactsBeforeReadingState(t *testing.T) {
	dir := t.TempDir()
	writeOnlineHostConfig(t, dir)
	transport := newFakeOnlineTransport("alpine")
	var output bytes.Buffer
	if err := runPlanWithRuntime([]string{"--format", "json"}, &output, dir, nil, fakeOnlineRuntime(transport, "")); err != nil {
		t.Fatal(err)
	}
	wantEvents := []string{
		"facts:cat /etc/os-release",
		"facts:apk --print-arch",
		"facts:uname -m",
		"state.read",
	}
	if !reflect.DeepEqual(transport.events, wantEvents) {
		t.Fatalf("online plan events = %#v, want %#v", transport.events, wantEvents)
	}
	if !strings.Contains(output.String(), `"mode": "online"`) || !strings.Contains(output.String(), `"hosts":`) || transport.stateWrites != 0 {
		t.Fatalf("online plan output = %s, writes=%d", output.String(), transport.stateWrites)
	}
}

func TestOnlinePlanRejectsUnsupportedTargetBeforeBackendAccess(t *testing.T) {
	dir := t.TempDir()
	writeOnlineHostConfig(t, dir)
	transport := newFakeOnlineTransport("debian")
	err := runPlanWithRuntime(nil, &bytes.Buffer{}, dir, nil, fakeOnlineRuntime(transport, ""))
	if err == nil || !strings.Contains(err.Error(), "unsupported target OS") {
		t.Fatalf("unsupported target error = %v", err)
	}
	if !reflect.DeepEqual(transport.events, []string{"facts:cat /etc/os-release"}) || transport.stateWrites != 0 {
		t.Fatalf("unsupported target events = %#v, writes=%d", transport.events, transport.stateWrites)
	}
}

func TestOnlinePlanRejectsPlatformMismatchBeforeBackendAccess(t *testing.T) {
	dir := t.TempDir()
	content := `
host "node" {
  platform {
    architecture = "amd64"
    version      = "3.24.2"
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.apf.hcl"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	transport := newFakeOnlineTransport("alpine")
	err := runPlanWithRuntime(nil, &bytes.Buffer{}, dir, nil, fakeOnlineRuntime(transport, ""))
	if err == nil || !strings.Contains(err.Error(), `declares "3.24.2", but detected exact version is "3.24.1"`) {
		t.Fatalf("platform mismatch error = %v", err)
	}
	wantEvents := []string{"facts:cat /etc/os-release", "facts:apk --print-arch", "facts:uname -m"}
	if !reflect.DeepEqual(transport.events, wantEvents) || transport.stateWrites != 0 {
		t.Fatalf("platform mismatch events = %#v, writes=%d", transport.events, transport.stateWrites)
	}
}

func TestApplyRebuildsUnderLockAndPersistsFactsAfterApproval(t *testing.T) {
	dir := t.TempDir()
	writeOnlineHostConfig(t, dir)
	transport := newFakeOnlineTransport("alpine")
	var output bytes.Buffer
	err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &output, dir, nil, fakeOnlineRuntime(transport, ""))
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := []string{
		"facts:cat /etc/os-release", "facts:apk --print-arch", "facts:uname -m", "state.read",
		"lock.acquire",
		"facts:cat /etc/os-release", "facts:apk --print-arch", "facts:uname -m", "state.read",
		"state.write", "lock.release",
	}
	if !reflect.DeepEqual(transport.events, wantEvents) {
		t.Fatalf("apply events = %#v, want %#v", transport.events, wantEvents)
	}
	if transport.stateWrites != 1 || transport.state.Serial != 1 || transport.state.Facts == nil || transport.state.Facts.Version != "3.24.1" {
		t.Fatalf("applied state = %#v, writes=%d", transport.state, transport.stateWrites)
	}
	if !strings.Contains(output.String(), "Preview before lock:") || !strings.Contains(output.String(), "Locked execution plan:") || !strings.Contains(output.String(), "Apply complete: 1 host(s).") {
		t.Fatalf("apply output = %s", output.String())
	}
}

func TestApplyRequiresPreviewAndLockedApproval(t *testing.T) {
	dir := t.TempDir()
	writeOnlineHostConfig(t, dir)
	transport := newFakeOnlineTransport("alpine")
	var output bytes.Buffer
	err := runApplyWithRuntime([]string{"--lock-timeout", "0"}, &output, dir, nil, fakeOnlineRuntime(transport, "yes\nno\n"))
	if err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("locked approval error = %v", err)
	}
	if strings.Count(output.String(), "Approve this plan?") != 2 || transport.stateWrites != 0 {
		t.Fatalf("approval output = %s, writes=%d", output.String(), transport.stateWrites)
	}
	if len(transport.events) == 0 || transport.events[len(transport.events)-1] != "lock.release" {
		t.Fatalf("approval rejection events = %#v", transport.events)
	}
}

func TestApplyDebugCoversRemotePhasesWithoutValues(t *testing.T) {
	dir := t.TempDir()
	writeOnlineHostConfig(t, dir)
	transport := newFakeOnlineTransport("alpine")
	var output bytes.Buffer
	err := runApplyWithRuntime([]string{"--auto-approve", "--debug", "--lock-timeout", "0"}, &output, dir, nil, fakeOnlineRuntime(transport, ""))
	if err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, phase := range []string{"facts", "state", "lock", "apply", "cleanup"} {
		if !strings.Contains(text, "debug phase="+phase+" ") {
			t.Fatalf("debug output missing %s phase:\n%s", phase, text)
		}
	}
	for _, operation := range []string{"os-release", "state.read", "lock.acquire", "preview-review", "locked-review", "state.write", "lock.release"} {
		if !strings.Contains(text, "operation="+operation+" ") {
			t.Fatalf("debug output missing %s operation:\n%s", operation, text)
		}
	}
}

type debugFailingProvider struct {
	err error
}

func (provider debugFailingProvider) Inspect(context.Context, coregraph.Node) (coreengine.ObservedResource, error) {
	return coreengine.ObservedResource{}, provider.err
}

func (provider debugFailingProvider) Apply(context.Context, coreengine.Step) (coreengine.ObservedResource, error) {
	return coreengine.ObservedResource{}, provider.err
}

func (provider debugFailingProvider) Delete(context.Context, coreengine.Step) error {
	return provider.err
}

func TestDebugProviderRedactsProtectedErrorsAndEvents(t *testing.T) {
	secret := "not-a-real-provider-error-secret"
	var output bytes.Buffer
	var outputMu sync.Mutex
	logger := debugLogger{output: &output, mu: &outputMu}
	provider := debugProvider{delegate: debugFailingProvider{err: errors.New(secret)}, events: logger}
	node := coregraph.Node{Host: "node", Address: "host.node.file.secret", Sensitive: true, Desired: map[string]any{"content": secret}}
	if _, err := provider.Inspect(context.Background(), node); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("protected inspect error = %v", err)
	}
	step := coreengine.Step{Host: "node", Address: node.Address, Action: coreengine.ActionUpdate, Node: node, Prior: &corestate.Resource{Protected: true}}
	if _, err := provider.Apply(context.Background(), step); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("protected apply error = %v", err)
	}
	if err := provider.Delete(context.Background(), step); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("protected delete error = %v", err)
	}
	text := output.String()
	if strings.Contains(text, secret) || !strings.Contains(text, "debug phase=inspect") || strings.Count(text, "debug phase=operation") != 4 || strings.Count(text, "status=failed") != 3 {
		t.Fatalf("protected debug output = %s", text)
	}
}

func TestCheckReportsOrphanedStateAsDrift(t *testing.T) {
	dir := t.TempDir()
	writeOnlineHostConfig(t, dir)
	transport := newFakeOnlineTransport("alpine")
	transport.state.Resources["host.node.file.orphan"] = corestate.Resource{Host: "node", Kind: "file", Ownership: "managed"}
	var output bytes.Buffer
	err := runCheckWithRuntime(nil, &output, dir, nil, fakeOnlineRuntime(transport, ""))
	if err == nil || !strings.Contains(err.Error(), "drift or unapplied changes") {
		t.Fatalf("drift check error = %v", err)
	}
	if !strings.Contains(output.String(), "forget host.node.file.orphan") || transport.stateWrites != 0 {
		t.Fatalf("drift output = %s, writes=%d", output.String(), transport.stateWrites)
	}
}

func TestCheckReturnsCleanWithoutWritingState(t *testing.T) {
	dir := t.TempDir()
	writeOnlineHostConfig(t, dir)
	transport := newFakeOnlineTransport("alpine")
	var output bytes.Buffer
	if err := runCheckWithRuntime(nil, &output, dir, nil, fakeOnlineRuntime(transport, "")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "No remote resource changes.") || transport.stateWrites != 0 {
		t.Fatalf("clean check output = %s, writes=%d", output.String(), transport.stateWrites)
	}
}

func TestNativeFileWorkflowCreatesDetectsDriftAndRepairs(t *testing.T) {
	dir := t.TempDir()
	secret := "not-a-real-managed-file-secret"
	content := `
host "node" {
  files {
    file "/etc/app/config" {
      content   = "` + secret + `"
      sensitive = true
      mode      = "0640"
    }
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.apf.hcl"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	transport := newFakeOnlineTransport("alpine")
	var applyOutput bytes.Buffer
	if err := runApplyWithRuntime([]string{"--auto-approve", "--debug", "--lock-timeout", "0"}, &applyOutput, dir, nil, fakeNativeRuntime(transport, "")); err != nil {
		t.Fatal(err)
	}
	address := `host.node.files.file["/etc/app/config"]`
	if !transport.fileExists || string(transport.fileContent) != secret || transport.fileMode != "0640" || transport.stateWrites != 1 {
		t.Fatalf("remote file/state = exists=%v content=%q mode=%q writes=%d", transport.fileExists, transport.fileContent, transport.fileMode, transport.stateWrites)
	}
	resource, exists := transport.state.Resources[address]
	if !exists || resource.DesiredDigest == "" || !resource.Protected || resource.Delete["path"] != "/etc/app/config" {
		t.Fatalf("file state = %#v", resource)
	}
	stateData, err := corestate.Encode(transport.state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(applyOutput.String(), secret) || strings.Contains(string(stateData), secret) {
		t.Fatalf("file workflow leaked secret:\n%s\n%s", applyOutput.String(), stateData)
	}
	for _, phase := range []string{"phase=inspect", "phase=operation"} {
		if !strings.Contains(applyOutput.String(), phase) {
			t.Fatalf("file debug output missing %s:\n%s", phase, applyOutput.String())
		}
	}

	if err := runCheckWithRuntime(nil, &bytes.Buffer{}, dir, nil, fakeNativeRuntime(transport, "")); err != nil {
		t.Fatalf("second check = %v", err)
	}
	transport.fileContent = []byte("external drift")
	var driftOutput bytes.Buffer
	if err := runCheckWithRuntime(nil, &driftOutput, dir, nil, fakeNativeRuntime(transport, "")); err == nil || !strings.Contains(err.Error(), "drift") || !strings.Contains(driftOutput.String(), "update "+address) {
		t.Fatalf("drift check error = %v, output = %s", err, driftOutput.String())
	}
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, fakeNativeRuntime(transport, "")); err != nil {
		t.Fatalf("repair apply = %v", err)
	}
	if string(transport.fileContent) != secret || transport.state.Serial != 2 {
		t.Fatalf("repaired file/state = content=%q state=%#v", transport.fileContent, transport.state)
	}
}

func TestNativeFileRemovalDefaultsToForgetAndSupportsDestroy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.apf.hcl")
	writeFileConfig := func(onRemove string) {
		t.Helper()
		attribute := ""
		if onRemove != "" {
			attribute = "      on_remove = \"" + onRemove + "\"\n"
		}
		content := "host \"node\" {\n  files {\n    file \"/etc/app/config\" {\n      content = \"managed\"\n" + attribute + "    }\n  }\n}\n"
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeEmptyHost := func() {
		t.Helper()
		if err := os.WriteFile(path, []byte("host \"node\" {}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	transport := newFakeOnlineTransport("alpine")
	runtime := fakeNativeRuntime(transport, "")
	writeFileConfig("")
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatal(err)
	}
	writeEmptyHost()
	var forgetPlan bytes.Buffer
	if err := runCheckWithRuntime(nil, &forgetPlan, dir, nil, runtime); err == nil || !strings.Contains(forgetPlan.String(), "forget host.node.files.file[\"/etc/app/config\"]") {
		t.Fatalf("forget check error = %v, output = %s", err, forgetPlan.String())
	}
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatal(err)
	}
	if !transport.fileExists || len(transport.state.Resources) != 0 {
		t.Fatalf("forget removed remote file or retained state: exists=%v state=%#v", transport.fileExists, transport.state)
	}

	writeFileConfig("destroy")
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatal(err)
	}
	writeEmptyHost()
	var destroyPlan bytes.Buffer
	if err := runCheckWithRuntime(nil, &destroyPlan, dir, nil, runtime); err == nil || !strings.Contains(destroyPlan.String(), "destroy host.node.files.file[\"/etc/app/config\"]") {
		t.Fatalf("destroy check error = %v, output = %s", err, destroyPlan.String())
	}
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatal(err)
	}
	if transport.fileExists || len(transport.state.Resources) != 0 {
		t.Fatalf("destroy result: exists=%v state=%#v", transport.fileExists, transport.state)
	}
}

func TestNativeDirectoryWorkflowConvergesAndRequiresExplicitRecursiveDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.apf.hcl")
	writeDirectoryConfig := func(mode, ensure string, recursive bool) {
		t.Helper()
		content := fmt.Sprintf(`
host "node" {
  directories {
    directory "/srv/app" {
      mode             = %q
      ensure           = %q
      recursive_delete = %t
    }
  }
}
`, mode, ensure, recursive)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	transport := newFakeOnlineTransport("alpine")
	runtime := fakeNativeRuntime(transport, "")
	writeDirectoryConfig("0750", "present", false)
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatal(err)
	}
	address := `host.node.directories.directory["/srv/app"]`
	resource, exists := transport.state.Resources[address]
	if !transport.directoryExists || transport.directoryMode != "0750" || !exists || resource.Delete["path"] != "/srv/app" || resource.Delete["recursive"] != false {
		t.Fatalf("directory/state after create = exists=%v mode=%q resource=%#v", transport.directoryExists, transport.directoryMode, resource)
	}
	if err := runCheckWithRuntime(nil, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("second check = %v", err)
	}

	transport.directoryMode = "0700"
	var driftOutput bytes.Buffer
	if err := runCheckWithRuntime(nil, &driftOutput, dir, nil, runtime); err == nil || !strings.Contains(err.Error(), "drift") || !strings.Contains(driftOutput.String(), "update "+address) {
		t.Fatalf("directory drift check error = %v, output = %s", err, driftOutput.String())
	}
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("directory repair apply = %v", err)
	}
	if transport.directoryMode != "0750" {
		t.Fatalf("repaired directory mode = %q", transport.directoryMode)
	}

	transport.directoryNonEmpty = true
	writeDirectoryConfig("0750", "absent", false)
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err == nil || !strings.Contains(err.Error(), "directory not empty") {
		t.Fatalf("safe non-empty delete error = %v", err)
	}
	if !transport.directoryExists {
		t.Fatal("safe delete failure removed the directory")
	}

	writeDirectoryConfig("0750", "absent", true)
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("recursive directory delete = %v", err)
	}
	if transport.directoryExists || len(transport.state.Resources) != 0 {
		t.Fatalf("recursive delete result: exists=%v state=%#v", transport.directoryExists, transport.state)
	}
}

func TestNativeDirectoryWorkflowAdoptsMatchingDirectory(t *testing.T) {
	dir := t.TempDir()
	content := `
host "node" {
  directories {
    directory "/srv/app" {}
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.apf.hcl"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	transport := newFakeOnlineTransport("alpine")
	transport.directoryExists = true
	transport.directoryOwner = "root"
	transport.directoryGroup = "root"
	transport.directoryMode = "0755"
	runtime := fakeNativeRuntime(transport, "")
	address := `host.node.directories.directory["/srv/app"]`
	var checkOutput bytes.Buffer
	if err := runCheckWithRuntime(nil, &checkOutput, dir, nil, runtime); err == nil || !strings.Contains(checkOutput.String(), "adopt "+address) {
		t.Fatalf("directory adoption check error = %v, output = %s", err, checkOutput.String())
	}
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatal(err)
	}
	if _, exists := transport.state.Resources[address]; !exists {
		t.Fatalf("adopted directory state = %#v", transport.state)
	}
	for _, event := range transport.events {
		if event == "apply.directory" {
			t.Fatalf("adoption changed the matching remote directory: events=%#v", transport.events)
		}
	}
	if err := runCheckWithRuntime(nil, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("adopted directory second check = %v", err)
	}
}

func TestNativeGroupWorkflowConvergesRepairsAndDeletes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.apf.hcl")
	writeGroupConfig := func(gid int, ensure string) {
		t.Helper()
		content := fmt.Sprintf(`
host "node" {
  groups {
    group "app" {
      gid       = %d
      system    = true
      ensure    = %q
      on_remove = "destroy"
    }
  }
}
`, gid, ensure)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	transport := newFakeOnlineTransport("alpine")
	runtime := fakeNativeRuntime(transport, "")
	writeGroupConfig(1500, "present")
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatal(err)
	}
	address := `host.node.groups.group["app"]`
	resource, exists := transport.state.Resources[address]
	if !transport.groupExists || transport.groupGID != "1500" || !exists || resource.Kind != "group" || resource.Delete["name"] != "app" || resource.DeleteBehavior != "destroy" {
		t.Fatalf("group/state after create = exists=%v gid=%q resource=%#v", transport.groupExists, transport.groupGID, resource)
	}
	if err := runCheckWithRuntime(nil, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("group second check = %v", err)
	}
	transport.groupGID = "1600"
	var driftOutput bytes.Buffer
	if err := runCheckWithRuntime(nil, &driftOutput, dir, nil, runtime); err == nil || !strings.Contains(err.Error(), "drift") || !strings.Contains(driftOutput.String(), "update "+address) {
		t.Fatalf("group drift check error = %v, output = %s", err, driftOutput.String())
	}
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("group repair apply = %v", err)
	}
	if transport.groupGID != "1500" {
		t.Fatalf("repaired group gid = %q", transport.groupGID)
	}
	writeGroupConfig(1500, "absent")
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("group delete apply = %v", err)
	}
	if transport.groupExists || len(transport.state.Resources) != 0 {
		t.Fatalf("group delete result: exists=%v state=%#v", transport.groupExists, transport.state)
	}
}

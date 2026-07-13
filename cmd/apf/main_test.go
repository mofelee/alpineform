package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	corebackend "github.com/mofelee/alpineform/internal/core/backend"
	coreengine "github.com/mofelee/alpineform/internal/core/engine"
	coregraph "github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
	corestate "github.com/mofelee/alpineform/internal/core/state"
	"golang.org/x/crypto/ssh"
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
	osID                    string
	state                   corestate.State
	events                  []string
	stateWrites             int
	fileExists              bool
	fileContent             []byte
	fileOwner               string
	fileGroup               string
	fileMode                string
	directoryExists         bool
	directoryNonEmpty       bool
	directoryOwner          string
	directoryGroup          string
	directoryMode           string
	groupExists             bool
	groupGID                string
	groupGIDs               map[string]string
	userExists              bool
	userUID                 string
	userGroup               string
	userGroupGID            string
	userHome                string
	userShell               string
	membershipExists        bool
	authorizedKeyExists     bool
	authorizedKeyMetadataOK bool
	apkRepositories         map[string]string
	apkKeyDigests           map[string]string
	apkUpdateFingerprint    string
	apkUpdateCount          int
	apkPackages             map[string]bool
	apkWorld                map[string]bool
	serviceExists           bool
	serviceEnabled          bool
	serviceRunlevel         string
	serviceRuntime          string
	serviceApplyCount       int
	serviceOperationCount   int
	serviceLastOperation    string
	hostnameFile            string
	hostnameRuntime         string
	timezoneLocaltime       string
	timezoneFile            string
}

func newFakeOnlineTransport(osID string) *fakeOnlineTransport {
	return &fakeOnlineTransport{
		osID: osID, state: corestate.Empty("node"), groupGIDs: map[string]string{},
		apkRepositories: map[string]string{}, apkKeyDigests: map[string]string{}, apkPackages: map[string]bool{}, apkWorld: map[string]bool{},
	}
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
		gid, exists := transport.groupGIDs[command.Arguments[0]]
		if !exists {
			return []byte("missing\n"), nil
		}
		return []byte("group\n" + gid + "\n"), nil
	case "apply.group":
		transport.groupExists = true
		if command.Arguments[1] != "" {
			transport.groupGID = command.Arguments[1]
		} else if transport.groupGID == "" {
			transport.groupGID = "1000"
		}
		transport.groupGIDs[command.Arguments[0]] = transport.groupGID
		return nil, nil
	case "delete.group":
		delete(transport.groupGIDs, command.Arguments[0])
		transport.groupExists = len(transport.groupGIDs) != 0
		if !transport.groupExists {
			transport.groupGID = ""
		}
		return nil, nil
	case "inspect.user":
		if !transport.userExists {
			return []byte("missing\n"), nil
		}
		return []byte(fmt.Sprintf("user\n%s\n%s\n%s\n%s\n%s\n", transport.userUID, transport.userGroupGID, transport.userGroup, transport.userHome, transport.userShell)), nil
	case "apply.user":
		transport.userExists = true
		if command.Arguments[1] != "" {
			transport.userUID = command.Arguments[1]
		} else if transport.userUID == "" {
			transport.userUID = "1000"
		}
		if command.Arguments[2] != "" {
			transport.userGroup = command.Arguments[2]
			transport.userGroupGID = transport.groupGIDs[command.Arguments[2]]
		} else if transport.userGroup == "" {
			transport.userGroup = command.Arguments[0]
			transport.userGroupGID = transport.userUID
		}
		if command.Arguments[3] != "" {
			transport.userHome = command.Arguments[3]
		} else if transport.userHome == "" {
			transport.userHome = "/home/" + command.Arguments[0]
		}
		if command.Arguments[4] != "" {
			transport.userShell = command.Arguments[4]
		} else if transport.userShell == "" {
			transport.userShell = "/bin/ash"
		}
		return nil, nil
	case "delete.user":
		transport.userExists = false
		transport.userUID = ""
		transport.userGroup = ""
		transport.userGroupGID = ""
		transport.userHome = ""
		transport.userShell = ""
		return nil, nil
	case "inspect.membership":
		if transport.membershipExists {
			return []byte("membership\n"), nil
		}
		return []byte("missing\n"), nil
	case "apply.membership":
		transport.membershipExists = true
		return nil, nil
	case "delete.membership":
		transport.membershipExists = false
		return nil, nil
	case "inspect.authorized_key":
		if !transport.authorizedKeyExists {
			return []byte("missing\n"), nil
		}
		return []byte(fmt.Sprintf("key\n%t\n", transport.authorizedKeyMetadataOK)), nil
	case "apply.authorized_key":
		transport.authorizedKeyExists = true
		transport.authorizedKeyMetadataOK = true
		return nil, nil
	case "delete.authorized_key":
		transport.authorizedKeyExists = false
		transport.authorizedKeyMetadataOK = false
		return nil, nil
	case "inspect.apk_repository":
		name := fakeAPKRepositoryName(command.Arguments[1])
		line, exists := transport.apkRepositories[name]
		if !exists {
			return []byte("missing\n"), nil
		}
		return []byte("repository\n" + line + "\n"), nil
	case "apply.apk_repository":
		name := fakeAPKRepositoryName(command.Arguments[1])
		transport.apkRepositories[name] = command.Arguments[3]
		return nil, nil
	case "delete.apk_repository":
		name := fakeAPKRepositoryName(command.Arguments[1])
		delete(transport.apkRepositories, name)
		return nil, nil
	case "inspect.apk_key":
		filename := filepath.Base(command.Arguments[0])
		digest, exists := transport.apkKeyDigests[filename]
		if !exists {
			return []byte("missing\n"), nil
		}
		return []byte("key\n" + digest + "\n"), nil
	case "apply.apk_key":
		filename := filepath.Base(command.Arguments[0])
		transport.apkKeyDigests[filename] = command.Arguments[1]
		return nil, nil
	case "delete.apk_key":
		delete(transport.apkKeyDigests, filepath.Base(command.Arguments[0]))
		return nil, nil
	case "inspect.apk_update":
		if transport.apkUpdateFingerprint == "" {
			return []byte("missing\n"), nil
		}
		return []byte("marker\n" + transport.apkUpdateFingerprint + "\n"), nil
	case "apply.apk_update":
		transport.apkUpdateFingerprint = command.Arguments[1]
		transport.apkUpdateCount++
		return nil, nil
	case "inspect.package":
		name := command.Arguments[0]
		intent := command.Arguments[1]
		installed := transport.apkPackages[name]
		world := transport.apkWorld[intent]
		if !installed && !world {
			return []byte("missing\n"), nil
		}
		return []byte(fmt.Sprintf("package\n%t\n%t\n%s-1.0-r0\n", installed, world, name)), nil
	case "apply.package":
		intent := command.Arguments[0]
		name := strings.SplitN(intent, "@", 2)[0]
		transport.apkPackages[name] = true
		transport.apkWorld[intent] = true
		return nil, nil
	case "delete.package":
		name := command.Arguments[0]
		delete(transport.apkPackages, name)
		for intent := range transport.apkWorld {
			if intent == name || strings.HasPrefix(intent, name+"@") {
				delete(transport.apkWorld, intent)
			}
		}
		return nil, nil
	case "inspect.service":
		if !transport.serviceExists {
			return []byte("missing\n"), nil
		}
		enabled := transport.serviceEnabled && transport.serviceRunlevel == command.Arguments[1]
		statusCode := 3
		if transport.serviceRuntime == "started" {
			statusCode = 0
		} else if transport.serviceRuntime == "crashed" {
			statusCode = 32
		}
		return []byte(fmt.Sprintf("service\n%t\n%s\n%d\n", enabled, transport.serviceRuntime, statusCode)), nil
	case "apply.service":
		if !transport.fileExists {
			return nil, fmt.Errorf("service init file is missing")
		}
		transport.serviceExists = true
		transport.serviceEnabled = command.Arguments[2] == "true"
		transport.serviceRunlevel = command.Arguments[1]
		if command.Arguments[3] == "running" {
			transport.serviceRuntime = "started"
		} else {
			transport.serviceRuntime = "stopped"
		}
		transport.serviceApplyCount++
		if command.Arguments[4] != "" {
			transport.serviceOperationCount++
			transport.serviceLastOperation = command.Arguments[4]
		}
		return nil, nil
	case "inspect.system_hostname":
		fileExists := transport.hostnameFile != ""
		return []byte(fmt.Sprintf("hostname\n%t\n%s\n%s\n", fileExists, transport.hostnameFile, transport.hostnameRuntime)), nil
	case "apply.system_hostname":
		transport.hostnameFile = command.Arguments[0]
		transport.hostnameRuntime = command.Arguments[0]
		return nil, nil
	case "inspect.system_timezone":
		timezone := command.Arguments[0]
		pathsExist := transport.timezoneLocaltime != "" || transport.timezoneFile != ""
		return []byte(fmt.Sprintf("timezone\n%t\n%t\n%t\n%t\n", transport.apkPackages["tzdata"], transport.timezoneLocaltime == timezone, transport.timezoneFile == timezone, pathsExist)), nil
	case "apply.system_timezone":
		if !transport.apkPackages["tzdata"] {
			return nil, fmt.Errorf("tzdata is not installed")
		}
		transport.timezoneLocaltime = command.Arguments[0]
		transport.timezoneFile = command.Arguments[0]
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected backend command %q", command.Name)
	}
}

func fakeAPKRepositoryName(beginMarker string) string {
	return strings.TrimPrefix(beginMarker, "# BEGIN ALPINEFORM REPOSITORY ")
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
	transport.groupGIDs["app"] = "1600"
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

func TestNativeUserWorkflowConvergesRepairsAndDeletesInDependencyOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.apf.hcl")
	writeAccountConfig := func(ensure, shell string) {
		t.Helper()
		content := fmt.Sprintf(`
host "node" {
  groups {
    group "app" {
      gid       = 1500
      ensure    = %q
      on_remove = "destroy"
    }
  }
  users {
    user "app" {
      uid       = 1500
      group     = "app"
      home      = "/srv/app"
      shell     = %q
      system    = true
      ensure    = %q
      on_remove = "destroy"
    }
  }
}
`, ensure, shell, ensure)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	transport := newFakeOnlineTransport("alpine")
	runtime := fakeNativeRuntime(transport, "")
	writeAccountConfig("present", "/sbin/nologin")
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatal(err)
	}
	address := `host.node.users.user["app"]`
	resource, exists := transport.state.Resources[address]
	groupResource := transport.state.Resources[`host.node.groups.group["app"]`]
	if !transport.userExists || transport.userUID != "1500" || transport.userGroup != "app" || transport.userGroupGID != "1500" || transport.userHome != "/srv/app" || transport.userShell != "/sbin/nologin" || !exists || resource.Kind != "user" || resource.Delete["name"] != "app" || groupResource.Order >= resource.Order {
		t.Fatalf("user/state after create = user=%#v resource=%#v", transport, resource)
	}
	if err := runCheckWithRuntime(nil, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("user second check = %v", err)
	}
	transport.userShell = "/bin/ash"
	var driftOutput bytes.Buffer
	if err := runCheckWithRuntime(nil, &driftOutput, dir, nil, runtime); err == nil || !strings.Contains(err.Error(), "drift") || !strings.Contains(driftOutput.String(), "update "+address) {
		t.Fatalf("user drift check error = %v, output = %s", err, driftOutput.String())
	}
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("user repair apply = %v", err)
	}
	if transport.userShell != "/sbin/nologin" {
		t.Fatalf("repaired user shell = %q", transport.userShell)
	}
	if err := os.WriteFile(path, []byte("host \"node\" {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	eventStart := len(transport.events)
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("account delete apply = %v", err)
	}
	deleteEvents := make([]string, 0, 2)
	for _, event := range transport.events[eventStart:] {
		if strings.HasPrefix(event, "delete.") {
			deleteEvents = append(deleteEvents, event)
		}
	}
	if !reflect.DeepEqual(deleteEvents, []string{"delete.user", "delete.group"}) {
		t.Fatalf("account deletion order = %#v", deleteEvents)
	}
	if transport.userExists || transport.groupExists || len(transport.state.Resources) != 0 {
		t.Fatalf("account delete result: user=%v group=%v state=%#v", transport.userExists, transport.groupExists, transport.state)
	}
}

func testAuthorizedKeyLine(t *testing.T) (string, string) {
	t.Helper()
	publicKey, err := ssh.NewPublicKey(ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(publicKey))) + " cli-test", ssh.FingerprintSHA256(publicKey)
}

func TestNativeMembershipAndAuthorizedKeyWorkflowRepairsDriftAndRemovesListItems(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.apf.hcl")
	key, fingerprint := testAuthorizedKeyLine(t)
	writeConfig := func(withChildren bool) {
		t.Helper()
		children := ""
		if withChildren {
			children = fmt.Sprintf("      groups = [\"wheel\"]\n      ssh_authorized_keys = [%q]\n", key)
		}
		content := fmt.Sprintf(`
host "node" {
  groups {
    group "app" { gid = 1500 }
    group "wheel" { gid = 1600 }
  }
  users {
    user "app" {
      uid   = 1500
      group = "app"
      home  = "/srv/app"
%s    }
  }
}
`, children)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	transport := newFakeOnlineTransport("alpine")
	runtime := fakeNativeRuntime(transport, "")
	writeConfig(true)
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatal(err)
	}
	membershipAddress := `host.node.users.user["app"].groups.group["wheel"]`
	keyAddress := `host.node.users.user["app"].ssh_authorized_keys.key[` + fmt.Sprintf("%q", fingerprint) + `]`
	membershipState, membershipTracked := transport.state.Resources[membershipAddress]
	keyState, keyTracked := transport.state.Resources[keyAddress]
	if !transport.membershipExists || !transport.authorizedKeyExists || !transport.authorizedKeyMetadataOK || !membershipTracked || !keyTracked || membershipState.Delete["group"] != "wheel" || keyState.Delete["key_type"] != "ssh-ed25519" {
		t.Fatalf("membership/key state = membership=%#v key=%#v transport=%#v", membershipState, keyState, transport)
	}
	stateData, err := corestate.Encode(transport.state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stateData), "cli-test") {
		t.Fatalf("authorized key comment entered state: %s", stateData)
	}
	if err := runCheckWithRuntime(nil, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("membership/key second check = %v", err)
	}

	transport.membershipExists = false
	transport.authorizedKeyExists = false
	var driftOutput bytes.Buffer
	if err := runCheckWithRuntime(nil, &driftOutput, dir, nil, runtime); err == nil || !strings.Contains(driftOutput.String(), "create "+membershipAddress) || !strings.Contains(driftOutput.String(), "create "+keyAddress) {
		t.Fatalf("membership/key drift check error = %v, output = %s", err, driftOutput.String())
	}
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("membership/key repair apply = %v", err)
	}
	transport.authorizedKeyMetadataOK = false
	var metadataOutput bytes.Buffer
	if err := runCheckWithRuntime(nil, &metadataOutput, dir, nil, runtime); err == nil || !strings.Contains(metadataOutput.String(), "update "+keyAddress) {
		t.Fatalf("authorized key metadata check error = %v, output = %s", err, metadataOutput.String())
	}
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("authorized key metadata repair = %v", err)
	}

	writeConfig(false)
	eventStart := len(transport.events)
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("managed list item removal = %v", err)
	}
	deleteEvents := []string{}
	for _, event := range transport.events[eventStart:] {
		if strings.HasPrefix(event, "delete.") {
			deleteEvents = append(deleteEvents, event)
		}
	}
	if !reflect.DeepEqual(deleteEvents, []string{"delete.authorized_key", "delete.membership"}) || transport.membershipExists || transport.authorizedKeyExists || len(transport.state.Resources) != 3 {
		t.Fatalf("managed child removal = events=%#v transport=%#v state=%#v", deleteEvents, transport, transport.state)
	}
}

func TestNativeAPKRepositoryWorkflowAggregatesUpdateAndForgetsRemovedDeclaration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.apf.hcl")
	writeConfig := func(mainEnsure string, includeCommunity bool) {
		t.Helper()
		community := ""
		if includeCommunity {
			community = `
    repository "community" {
      url = "https://dl-cdn.alpinelinux.org/alpine"
    }
`
		}
		content := fmt.Sprintf(`
host "node" {
  platform { version = "3.24.1" }
  apk {
    repository "main" {
      url    = "https://dl-cdn.alpinelinux.org/alpine"
      ensure = %q
    }
%s  }
}
`, mainEnsure, community)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	transport := newFakeOnlineTransport("alpine")
	runtime := fakeNativeRuntime(transport, "")
	writeConfig("present", true)
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatal(err)
	}
	if transport.apkUpdateCount != 1 || len(transport.apkRepositories) != 2 {
		t.Fatalf("initial APK result = repositories=%#v updates=%d", transport.apkRepositories, transport.apkUpdateCount)
	}
	if err := runCheckWithRuntime(nil, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("APK no-op check = %v", err)
	}
	if transport.apkUpdateCount != 1 {
		t.Fatalf("no-op ran apk update: %d", transport.apkUpdateCount)
	}

	transport.apkRepositories["main"] = "https://drift.example/alpine/v3.24/main"
	var drift bytes.Buffer
	if err := runCheckWithRuntime(nil, &drift, dir, nil, runtime); err == nil || !strings.Contains(drift.String(), `update host.node.apk.repository["main"]`) || !strings.Contains(drift.String(), "update host.node.apk.update") {
		t.Fatalf("APK drift check error = %v, output = %s", err, drift.String())
	}
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("APK drift repair = %v", err)
	}
	if transport.apkUpdateCount != 2 || transport.apkRepositories["main"] != "https://dl-cdn.alpinelinux.org/alpine/v3.24/main" {
		t.Fatalf("APK drift repair result = repositories=%#v updates=%d", transport.apkRepositories, transport.apkUpdateCount)
	}

	writeConfig("present", false)
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("APK declaration forget = %v", err)
	}
	communityAddress := `host.node.apk.repository["community"]`
	if _, exists := transport.apkRepositories["community"]; !exists || transport.apkUpdateCount != 2 {
		t.Fatalf("removed declaration changed remote APK state: repositories=%#v updates=%d", transport.apkRepositories, transport.apkUpdateCount)
	}
	if _, exists := transport.state.Resources[communityAddress]; exists {
		t.Fatalf("removed APK declaration remained in state: %#v", transport.state.Resources)
	}

	writeConfig("absent", false)
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("explicit APK repository removal = %v", err)
	}
	if _, exists := transport.apkRepositories["main"]; exists || transport.apkUpdateCount != 3 {
		t.Fatalf("explicit APK removal result = repositories=%#v updates=%d", transport.apkRepositories, transport.apkUpdateCount)
	}
}

func TestNativeAPKKeyWorkflowVerifiesDriftAndRequiresExplicitAbsence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.apf.hcl")
	keyContent := []byte("test-only-public-apk-key\n")
	if err := os.WriteFile(filepath.Join(dir, "vendor.rsa.pub"), keyContent, 0600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(keyContent))
	writeConfig := func(mode string) {
		t.Helper()
		keyBlock := ""
		switch mode {
		case "present":
			keyBlock = fmt.Sprintf(`
    key "vendor.rsa.pub" {
      source = "vendor.rsa.pub"
      sha256 = %q
    }
`, digest)
		case "absent":
			keyBlock = `
    key "vendor.rsa.pub" { ensure = "absent" }
`
		}
		content := fmt.Sprintf(`
host "node" {
  platform { version = "3.24.1" }
  apk {
%s  }
}
`, keyBlock)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	transport := newFakeOnlineTransport("alpine")
	runtime := fakeNativeRuntime(transport, "")
	writeConfig("present")
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatal(err)
	}
	if transport.apkKeyDigests["vendor.rsa.pub"] != digest || transport.apkUpdateCount != 1 {
		t.Fatalf("initial APK key result = keys=%#v updates=%d", transport.apkKeyDigests, transport.apkUpdateCount)
	}
	transport.apkKeyDigests["vendor.rsa.pub"] = strings.Repeat("b", 64)
	var drift bytes.Buffer
	if err := runCheckWithRuntime(nil, &drift, dir, nil, runtime); err == nil || !strings.Contains(drift.String(), `update host.node.apk.key["vendor.rsa.pub"]`) || !strings.Contains(drift.String(), "update host.node.apk.update") {
		t.Fatalf("APK key drift check error = %v, output = %s", err, drift.String())
	}
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatal(err)
	}
	if transport.apkKeyDigests["vendor.rsa.pub"] != digest || transport.apkUpdateCount != 2 {
		t.Fatalf("APK key repair = keys=%#v updates=%d", transport.apkKeyDigests, transport.apkUpdateCount)
	}

	writeConfig("removed")
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("APK key declaration forget = %v", err)
	}
	if _, exists := transport.apkKeyDigests["vendor.rsa.pub"]; !exists || transport.apkUpdateCount != 2 {
		t.Fatalf("removed key declaration changed remote state: keys=%#v updates=%d", transport.apkKeyDigests, transport.apkUpdateCount)
	}

	writeConfig("absent")
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("explicit APK key removal = %v", err)
	}
	if _, exists := transport.apkKeyDigests["vendor.rsa.pub"]; exists || transport.apkUpdateCount != 3 {
		t.Fatalf("explicit APK key removal result = keys=%#v updates=%d", transport.apkKeyDigests, transport.apkUpdateCount)
	}
}

func TestNativePackageWorkflowOwnsExplicitWorldIntentAndDefaultsToForget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.apf.hcl")
	writeConfig := func(mode string) {
		t.Helper()
		declaration := ""
		if mode == "present" || mode == "absent" {
			declaration = fmt.Sprintf("    package \"curl\" { ensure = %q }\n", mode)
		}
		content := fmt.Sprintf(`
host "node" {
  packages {
%s  }
}
`, declaration)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	transport := newFakeOnlineTransport("alpine")
	transport.apkPackages["busybox"] = true
	transport.apkWorld["busybox"] = true
	runtime := fakeNativeRuntime(transport, "")
	writeConfig("present")
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatal(err)
	}
	if !transport.apkPackages["curl"] || !transport.apkWorld["curl"] {
		t.Fatalf("package present result = packages=%#v world=%#v", transport.apkPackages, transport.apkWorld)
	}
	if err := runCheckWithRuntime(nil, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("package no-op check = %v", err)
	}
	delete(transport.apkWorld, "curl")
	var drift bytes.Buffer
	if err := runCheckWithRuntime(nil, &drift, dir, nil, runtime); err == nil || !strings.Contains(drift.String(), `update host.node.packages.package["curl"]`) {
		t.Fatalf("package world drift check error = %v, output = %s", err, drift.String())
	}
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("package world repair = %v", err)
	}

	writeConfig("removed")
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("package declaration forget = %v", err)
	}
	packageAddress := `host.node.packages.package["curl"]`
	if !transport.apkPackages["curl"] || !transport.apkWorld["curl"] {
		t.Fatalf("package forget changed remote state = packages=%#v world=%#v", transport.apkPackages, transport.apkWorld)
	}
	if _, exists := transport.state.Resources[packageAddress]; exists {
		t.Fatalf("forgotten package remained in state: %#v", transport.state.Resources)
	}

	writeConfig("absent")
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("explicit package absence = %v", err)
	}
	if transport.apkPackages["curl"] || transport.apkWorld["curl"] || !transport.apkPackages["busybox"] || !transport.apkWorld["busybox"] {
		t.Fatalf("explicit package removal affected other world intent: packages=%#v world=%#v", transport.apkPackages, transport.apkWorld)
	}
}

func TestNativeOpenRCServiceWorkflowOrdersRepairsAndForgets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.apf.hcl")
	writeConfig := func(includeService bool, enabled bool, runlevel, state, operation, version string) {
		t.Helper()
		service := ""
		if includeService {
			operationAttribute := ""
			if operation != "" {
				operationAttribute = fmt.Sprintf("      operation = %q\n", operation)
			}
			service = fmt.Sprintf(`
  services {
    service "worker" {
      enabled  = %t
      runlevel = %q
      state    = %q
%s
    }
  }
`, enabled, runlevel, state, operationAttribute)
		}
		content := fmt.Sprintf(`
host "node" {
  files {
    file "/etc/init.d/worker" {
      content = <<-EOT
        #!/sbin/openrc-run
        command='/bin/true'
        # %s
      EOT
      mode = "0755"
    }
  }
%s}
`, version, service)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	transport := newFakeOnlineTransport("alpine")
	runtime := fakeNativeRuntime(transport, "")
	writeConfig(true, true, "default", "running", "restarted", "v1")
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatal(err)
	}
	fileApply := slices.Index(transport.events, "apply.file")
	serviceApply := slices.Index(transport.events, "apply.service")
	if fileApply < 0 || serviceApply <= fileApply || !transport.serviceEnabled || transport.serviceRunlevel != "default" || transport.serviceRuntime != "started" || transport.serviceApplyCount != 1 {
		t.Fatalf("initial service result = events=%#v transport=%#v", transport.events, transport)
	}
	if err := runCheckWithRuntime(nil, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("service no-op check = %v", err)
	}
	if transport.serviceApplyCount != 1 {
		t.Fatalf("no-op triggered service operation: %d", transport.serviceApplyCount)
	}

	writeConfig(true, true, "default", "running", "restarted", "v2")
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("service init change apply = %v", err)
	}
	if transport.serviceApplyCount != 2 || transport.serviceOperationCount != 1 || transport.serviceLastOperation != "restarted" {
		t.Fatalf("service init change operation result = %#v", transport)
	}

	transport.serviceRunlevel = "boot"
	transport.serviceRuntime = "crashed"
	var drift bytes.Buffer
	if err := runCheckWithRuntime(nil, &drift, dir, nil, runtime); err == nil || !strings.Contains(drift.String(), `update host.node.services.service["worker"]`) {
		t.Fatalf("service drift check error = %v, output = %s", err, drift.String())
	}
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("service drift repair = %v", err)
	}
	if !transport.serviceEnabled || transport.serviceRunlevel != "default" || transport.serviceRuntime != "started" || transport.serviceApplyCount != 3 || transport.serviceOperationCount != 2 || transport.serviceLastOperation != "restarted" {
		t.Fatalf("service drift repair result = %#v", transport)
	}

	writeConfig(true, false, "default", "stopped", "", "v2")
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("stopped disabled service apply = %v", err)
	}
	if transport.serviceEnabled || transport.serviceRuntime != "stopped" || transport.serviceApplyCount != 4 || transport.serviceOperationCount != 2 {
		t.Fatalf("stopped disabled service result = %#v", transport)
	}

	writeConfig(false, false, "default", "stopped", "", "v2")
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("service declaration forget = %v", err)
	}
	if transport.serviceEnabled || transport.serviceRuntime != "stopped" || transport.serviceApplyCount != 4 || transport.serviceOperationCount != 2 {
		t.Fatalf("service forget changed remote state = %#v", transport)
	}
	if _, exists := transport.state.Resources[`host.node.services.service["worker"]`]; exists {
		t.Fatalf("forgotten service remained in state: %#v", transport.state.Resources)
	}
}

func TestNativeSystemWorkflowInstallsRepairsAndForgets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.apf.hcl")
	writeConfig := func(includeSystem bool) {
		t.Helper()
		system := ""
		if includeSystem {
			system = `
  system {
    hostname = "edge.example"
    timezone = "Asia/Shanghai"
  }
`
		}
		content := fmt.Sprintf("host \"node\" {\n%s}\n", system)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	transport := newFakeOnlineTransport("alpine")
	runtime := fakeNativeRuntime(transport, "")
	writeConfig(true)
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatal(err)
	}
	packageApply := slices.Index(transport.events, "apply.package")
	timezoneApply := slices.Index(transport.events, "apply.system_timezone")
	if !transport.apkPackages["tzdata"] || !transport.apkWorld["tzdata"] || transport.hostnameFile != "edge.example" || transport.hostnameRuntime != "edge.example" || transport.timezoneLocaltime != "Asia/Shanghai" || transport.timezoneFile != "Asia/Shanghai" || packageApply < 0 || timezoneApply <= packageApply {
		t.Fatalf("initial system result = events=%#v transport=%#v", transport.events, transport)
	}
	if err := runCheckWithRuntime(nil, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("system no-op check = %v", err)
	}

	transport.hostnameRuntime = "drifted"
	transport.hostnameFile = "drifted"
	transport.timezoneLocaltime = "UTC"
	transport.timezoneFile = "UTC"
	var drift bytes.Buffer
	if err := runCheckWithRuntime(nil, &drift, dir, nil, runtime); err == nil || !strings.Contains(drift.String(), "update host.node.system.hostname") || !strings.Contains(drift.String(), "update host.node.system.timezone") {
		t.Fatalf("system drift check error = %v, output = %s", err, drift.String())
	}
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("system drift repair = %v", err)
	}
	if transport.hostnameRuntime != "edge.example" || transport.timezoneLocaltime != "Asia/Shanghai" {
		t.Fatalf("system drift repair result = %#v", transport)
	}

	writeConfig(false)
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &bytes.Buffer{}, dir, nil, runtime); err != nil {
		t.Fatalf("system declaration forget = %v", err)
	}
	if transport.hostnameRuntime != "edge.example" || transport.timezoneLocaltime != "Asia/Shanghai" || !transport.apkPackages["tzdata"] || !transport.apkWorld["tzdata"] || len(transport.state.Resources) != 0 {
		t.Fatalf("system forget changed remote state = %#v", transport)
	}
}

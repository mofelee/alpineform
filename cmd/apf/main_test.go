package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestResourceCommandsFailExplicitlyInBootstrap(t *testing.T) {
	err := run([]string{"apply"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not available in the bootstrap build") {
		t.Fatalf("apply error = %v", err)
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

package main

import (
	"bytes"
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

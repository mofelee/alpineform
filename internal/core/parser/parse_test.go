package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestParseFilesEvaluatesTypedVariablesAndLocals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.apf.hcl")
	writeConfig(t, path, `
locals {
  prefix = "edge"
  label  = "${local.prefix}-${var.environment}"
}

variable "environment" {
  type        = string
  default     = "prod"
  nullable    = false
  description = "deployment environment"

  validation {
    condition     = length(var.environment) >= 3
    error_message = "environment must have at least three characters"
  }
}

variable "settings" {
  type = object({
    ports = list(number)
    mode  = optional(string, "strict")
  })
  default = {
    ports = [80, 443]
  }
}

variable "token" {
  type      = string
  sensitive = true
  ephemeral = true
  default   = "test-only-token"
}
`)

	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if got := config.VariableValues["environment"]; got.Kind != KindString || got.String != "prod" {
		t.Fatalf("environment = %#v", got)
	}
	settings := config.VariableValues["settings"]
	if got := settings.Map["mode"]; got.Kind != KindString || got.String != "strict" {
		t.Fatalf("settings.mode = %#v", got)
	}
	if got := config.Locals["label"]; got.Kind != KindString || got.String != "edge-prod" {
		t.Fatalf("local.label = %#v", got)
	}
	if got := config.VariableValues["token"]; !got.Sensitive || !got.Ephemeral {
		t.Fatalf("token marks = %#v", got)
	}
}

func TestCollectExternalVariableValuesAppliesDocumentedPrecedence(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "main.apf.hcl")
	writeConfig(t, configPath, `
variable "region" {
  type    = string
  default = "declaration"
}
`)
	writeConfig(t, filepath.Join(dir, "alpineform.apfvars"), `region = "default-file"`)
	writeConfig(t, filepath.Join(dir, "10.auto.apfvars"), `region = "auto"`)
	explicit := filepath.Join(dir, "prod.apfvars.json")
	writeConfig(t, explicit, `{"region":"explicit"}`)

	values, err := CollectExternalVariableValues(
		[]string{configPath},
		[]string{"APF_VAR_region=environment", "DBF_VAR_region=foreign"},
		[]string{explicit},
		[]string{"region=cli"},
	)
	if err != nil {
		t.Fatal(err)
	}
	config, err := ParseFilesWithOptions([]string{configPath}, ParseOptions{VariableValues: values})
	if err != nil {
		t.Fatal(err)
	}
	if got := config.VariableValues["region"]; got.String != "cli" {
		t.Fatalf("region = %#v, want CLI value", got)
	}
}

func TestParseFilesRejectsLocalCycleWithSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cycle.apf.hcl")
	writeConfig(t, path, `
locals {
  one = local.two
  two = local.one
}
`)
	_, err := ParseFiles([]string{path})
	if err == nil || !strings.Contains(err.Error(), "locals cycle detected: local.one -> local.two -> local.one") || !strings.Contains(err.Error(), path) {
		t.Fatalf("ParseFiles() error = %v", err)
	}
}

func TestParseFilesRejectsFailedValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "validation.apf.hcl")
	writeConfig(t, path, `
variable "version" {
  type    = string
  default = "edge"
  validation {
    condition     = startswith(var.version, "3.24")
    error_message = "version must select Alpine 3.24"
  }
}
`)
	_, err := ParseFiles([]string{path})
	if err == nil || !strings.Contains(err.Error(), "version must select Alpine 3.24") || !strings.Contains(err.Error(), "validation.apf.hcl") {
		t.Fatalf("ParseFiles() error = %v", err)
	}
}

func TestSensitiveExternalValueDoesNotLeakInError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sensitive.apf.hcl")
	writeConfig(t, path, `
variable "token" {
  type      = number
  sensitive = true
}
`)
	secret := "not-a-real-sensitive-value"
	_, err := ParseFilesWithOptions([]string{path}, ParseOptions{VariableValues: []ExternalVariableValue{{
		Name: "token", Value: secret, Source: ir.SourceRef{File: "cli", Line: 1, Path: "cli.var[0]"},
	}}})
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "sensitive variable") {
		t.Fatalf("ParseFilesWithOptions() error = %v", err)
	}
}

func TestParseFilesRejectsUnimplementedTopLevelBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host.apf.hcl")
	writeConfig(t, path, `host "server1" {}`)
	_, err := ParseFiles([]string{path})
	if err == nil || !strings.Contains(err.Error(), `top-level block "host" is not supported yet`) {
		t.Fatalf("ParseFiles() error = %v", err)
	}
}

func TestParseVariableFileRequiresAlpineFormSuffix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "values.dbfvars")
	writeConfig(t, path, `region = "foreign"`)
	_, err := ParseVariableFile(path)
	if err == nil || !strings.Contains(err.Error(), "*.apfvars") {
		t.Fatalf("ParseVariableFile() error = %v", err)
	}
}

package parser

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/zclconf/go-cty/cty"
)

func TestParseArtifactComponentSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact.apf.hcl")
	writeConfig(t, path, `
component "tool" {
  type    = "binary"
  version = "1.2.3"
  source "amd64" {
    url    = "https://example.invalid/tool-amd64"
    sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  }
  install {
    path = "/usr/local/bin/tool"
  }
}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	component := config.Components["tool"]
	if component.ArtifactType != "binary" || component.Version != "1.2.3" || component.Sources["amd64"].URL == "" || component.Install == nil || component.Install.Path != "/usr/local/bin/tool" {
		t.Fatalf("artifact component = %#v", component)
	}
	source := component.Sources["amd64"]
	if source.URL != "https://example.invalid/tool-amd64" || source.SHA256 != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" || source.URLExpr == nil || source.SHA256Expr == nil || source.URLSource.Line != 6 || source.SHA256Source.Line != 7 || source.URLSource.Path != `component["tool"].source["amd64"].url` || source.SHA256Source.Path != `component["tool"].source["amd64"].sha256` {
		t.Fatalf("artifact source expression metadata = %#v", source)
	}
}

func TestParseComponentArtifactSourceDefersInputExpressions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deferred.apf.hcl")
	writeConfig(t, path, `
component "tool" {
  input "url" { type = string }
  input "sha" { type = string }
  type = "binary"
  source "amd64" {
    url    = input.url
    sha256 = lower(input.sha)
  }
  install { path = "/usr/local/bin/tool" }
}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	source := config.Components["tool"].Sources["amd64"]
	if source.URLExpr == nil || source.SHA256Expr == nil || source.URL != "" || source.SHA256 != "" || source.URLSensitive || source.SHA256Sensitive || source.URLEphemeral || source.SHA256Ephemeral {
		t.Fatalf("deferred artifact source = %#v", source)
	}
	if source.URLSource.Line != 7 || source.SHA256Source.Line != 8 {
		t.Fatalf("deferred source locations = %#v / %#v", source.URLSource, source.SHA256Source)
	}
}

func TestParseComponentArtifactSourceDistinguishesMissingAndDeferred(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.apf.hcl")
	writeConfig(t, path, `
component "tool" {
  input "url" { type = string }
  type = "file"
  source { url = input.url }
  install { path = "/etc/tool" }
}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	source := config.Components["tool"].Sources[""]
	if source.URLExpr == nil || source.URL != "" || source.SHA256Expr != nil || source.SHA256Source != (ir.SourceRef{}) {
		t.Fatalf("missing/deferred artifact source = %#v", source)
	}
}

func TestParseComponentArtifactSourcePreservesMarksIndependently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "protected.apf.hcl")
	writeConfig(t, path, `
variable "url" {
  type      = string
  default   = "https://example.invalid/tool"
  sensitive = true
}
variable "sha" {
  type      = string
  default   = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  ephemeral = true
}
component "tool" {
  type = "binary"
  source {
    url    = var.url
    sha256 = var.sha
  }
  install { path = "/usr/local/bin/tool" }
}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	source := config.Components["tool"].Sources[""]
	if source.URL != "https://example.invalid/tool" || !source.URLSensitive || source.URLEphemeral {
		t.Fatalf("sensitive URL marks = %#v", source)
	}
	if source.SHA256 == "" || source.SHA256Sensitive || !source.SHA256Ephemeral {
		t.Fatalf("ephemeral SHA marks = %#v", source)
	}
}

func TestParseComponentArtifactSourceRejectsInvalidShapeAndEagerValues(t *testing.T) {
	validSHA := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "too many labels", body: `source "amd64" "extra" {
  url    = "https://example.invalid/tool"
  sha256 = "` + validSHA + `"
}`, want: "accepts at most one architecture label"},
		{name: "nested block", body: `source {
  nested {}
}`, want: "does not support nested blocks"},
		{name: "unknown attribute", body: `source {
  url    = "https://example.invalid/tool"
  sha256 = "` + validSHA + `"
  mirror = "other"
}`, want: "unsupported attribute"},
		{name: "duplicate source", body: `source {
  url    = "https://example.invalid/one"
  sha256 = "` + validSHA + `"
}
source {
  url    = "https://example.invalid/two"
  sha256 = "` + validSHA + `"
}`, want: "duplicate"},
		{name: "number", body: `source {
  url    = 42
  sha256 = "` + validSHA + `"
}`, want: "url must be a string"},
		{name: "null", body: `source {
  url    = null
  sha256 = "` + validSHA + `"
}`, want: "url must not be null"},
		{name: "empty", body: `source {
  url    = ""
  sha256 = "` + validSHA + `"
}`, want: "url must be a non-empty string"},
		{name: "unknown", body: `source {
  url    = unknown.url
  sha256 = "` + validSHA + `"
}`, want: "Unknown variable"},
		{name: "sha number", body: `source {
  url    = "https://example.invalid/tool"
  sha256 = 42
}`, want: "sha256 must be a string"},
		{name: "sha null", body: `source {
  url    = "https://example.invalid/tool"
  sha256 = null
}`, want: "sha256 must not be null"},
		{name: "sha empty", body: `source {
  url    = "https://example.invalid/tool"
  sha256 = ""
}`, want: "sha256 must be a non-empty string"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.apf.hcl")
			writeConfig(t, path, `
component "tool" {
  type = "binary"
  `+test.body+`
  install { path = "/usr/local/bin/tool" }
}
`)
			_, err := ParseFiles([]string{path})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseFiles() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestComponentArtifactInputScopeRemainsSourceOnly(t *testing.T) {
	fields := map[string]string{
		"type":    `type = input.value`,
		"version": `version = input.value`,
		"extract": `extract { format = input.value }`,
		"install": `install { path = input.value }`,
	}
	for name, field := range fields {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "scope.apf.hcl")
			writeConfig(t, path, `
component "tool" {
  input "value" { type = string }
  `+field+`
}
`)
			if _, err := ParseFiles([]string{path}); err == nil {
				t.Fatalf("ParseFiles() accepted input.* in %s", name)
			}
		})
	}
}

func TestParseSourceBuildSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "build.apf.hcl")
	writeConfig(t, path, `
component "tool" {
  type = "source"
  build {
    input "source" {
      content     = "int main(void) { return 0; }"
      sha256      = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
      destination = "src"
      extract {
        format           = "tar.gz"
        strip_components = 1
      }
    }
    command { argv = ["cc", "-o", "tool", "main.c"] }
    output = "tool"
  }
  install { path = "/usr/local/bin/tool" }
}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	build := config.Components["tool"].Build
	if build == nil || len(build.Inputs) != 1 || len(build.Commands) != 1 || build.Inputs[0].Name != "source" || build.Inputs[0].Extract == nil {
		t.Fatalf("source build = %#v", build)
	}
}

func TestEvaluateComponentArtifactStringRedactsProtectedEvaluationErrors(t *testing.T) {
	const sentinel = "not-a-real-artifact-secret"
	tests := []struct {
		name string
		expr string
		ctx  EvalContext
	}{
		{
			name: "marked variable object",
			expr: "tonumber(protected.token)",
			ctx: EvalContext{Variables: map[string]cty.Value{
				"protected": cty.ObjectVal(map[string]cty.Value{
					"token": cty.StringVal(sentinel),
				}).Mark(SensitiveMark),
			}},
		},
		{
			name: "protected local",
			expr: "tonumber(local.protected)",
			ctx: EvalContext{Locals: map[string]Value{
				"protected": {Kind: KindString, String: sentinel, Ephemeral: true},
			}},
		},
		{
			name: "indexed protected local",
			expr: `tonumber(local["protected"])`,
			ctx: EvalContext{Locals: map[string]Value{
				"protected": {Kind: KindString, String: sentinel, Sensitive: true},
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expr, diags := hclsyntax.ParseExpression([]byte(test.expr), "artifact.apf.hcl", hcl.InitialPos)
			if diags.HasErrors() {
				t.Fatal(diags.Error())
			}
			source := ir.SourceRef{File: "artifact.apf.hcl", Line: 7, Path: `component.tool.source["amd64"].url`}
			_, _, _, err := evaluateComponentArtifactString("url", expr, test.ctx, source)
			if err == nil || !strings.Contains(err.Error(), "protected artifact source expression failed to evaluate") || strings.Contains(err.Error(), sentinel) {
				t.Fatalf("evaluateComponentArtifactString() error = %v", err)
			}
		})
	}
}

func TestJSONEncodePreservesEphemeralMarkIndependently(t *testing.T) {
	value, err := jsonencodeFunction().Call([]cty.Value{cty.StringVal("value").Mark(EphemeralMark)})
	if err != nil {
		t.Fatal(err)
	}
	if containsMark(value, SensitiveMark) || !containsMark(value, EphemeralMark) {
		t.Fatalf("jsonencode() marks = %#v", value.Marks())
	}
}

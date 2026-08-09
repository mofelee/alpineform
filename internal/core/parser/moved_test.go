package parser

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseMovedComponentTraversalsPreservesSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "moved.apf.hcl")
	writeConfig(t, path, `moved {
  from = component.old_name
  to   = component.new_name
}

host "edge" {}
`)

	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Moves) != 1 {
		t.Fatalf("moves = %#v, want one declaration", config.Moves)
	}
	move := config.Moves[0]
	if move.From != "old_name" || move.To != "new_name" {
		t.Fatalf("move = %#v", move)
	}
	if move.Source.File != path || move.Source.Line != 1 || move.Source.Path != "moved[component.old_name]" {
		t.Fatalf("move source = %#v", move.Source)
	}
	if move.FromSource.File != path || move.FromSource.Line != 2 || move.FromSource.Path != "moved[component.old_name].from" {
		t.Fatalf("from source = %#v", move.FromSource)
	}
	if move.ToSource.File != path || move.ToSource.Line != 3 || move.ToSource.Path != "moved[component.old_name].to" {
		t.Fatalf("to source = %#v", move.ToSource)
	}
}

func TestParseMovedAllowsAbsentSourcesChainsAndDistinctRoots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "moved.apf.hcl")
	writeConfig(t, path, `moved {
  from = component.old
  to   = component.intermediate
}

moved {
  from = component.old_api
  to   = component.current_api
}

moved {
  from = component.intermediate
  to   = component.current
}

host "edge" {}
`)

	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(config.Moves))
	for _, move := range config.Moves {
		got = append(got, move.From+"->"+move.To)
	}
	want := []string{"intermediate->current", "old->intermediate", "old_api->current_api"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sorted moves = %#v, want %#v", got, want)
	}
}

func TestParseMovedRejectsInvalidBlocks(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "label",
			content: `moved "old" {
  from = component.old
  to   = component.new
}`,
			want: "moved block must not have labels",
		},
		{
			name: "nested block",
			content: `moved {
  from = component.old
  to   = component.new
  nested {}
}`,
			want: "moved block does not support nested blocks",
		},
		{name: "missing from", content: "moved {\n  to = component.new\n}", want: "moved.from is required"},
		{name: "missing to", content: "moved {\n  from = component.old\n}", want: "moved.to is required"},
		{
			name: "unknown attributes are deterministic",
			content: `moved {
  from  = component.old
  to    = component.new
  zeta  = true
  alpha = true
}`,
			want: "unsupported attribute moved.alpha",
		},
		{name: "string from", content: "moved {\n  from = \"component.old\"\n  to = component.new\n}", want: "moved.from must be a static component.<name> traversal"},
		{name: "string to", content: "moved {\n  from = component.old\n  to = \"component.new\"\n}", want: "moved.to must be a static component.<name> traversal"},
		{
			name: "dynamic expression",
			content: `variable "source" {
  type    = string
  default = "old"
}
moved {
  from = var.source
  to   = component.new
}`,
			want: "moved.from must be a static component.<name> traversal",
		},
		{name: "index expression", content: "moved {\n  from = component[\"old\"]\n  to = component.new\n}", want: "moved.from must be a static component.<name> traversal"},
		{name: "leaf address", content: "moved {\n  from = component.old.file.config\n  to = component.new\n}", want: "moved.from must be a static component.<name> traversal"},
		{name: "host qualified endpoint", content: "moved {\n  from = host.edge.component.old\n  to = component.new\n}", want: "moved.from must be a static component.<name> traversal"},
		{name: "self move", content: "moved {\n  from = component.same\n  to = component.same\n}", want: "component.same cannot move to itself"},
		{
			name: "duplicate",
			content: `moved {
  from = component.old
  to   = component.new
}
moved {
  from = component.old
  to   = component.new
}`,
			want: "duplicate mapping from component.old to component.new",
		},
		{
			name: "one to many",
			content: `moved {
  from = component.old
  to   = component.one
}
moved {
  from = component.old
  to   = component.two
}`,
			want: "component.old maps to both",
		},
		{
			name: "many to one",
			content: `moved {
  from = component.one
  to   = component.new
}
moved {
  from = component.two
  to   = component.new
}`,
			want: "both map to component.new",
		},
		{
			name: "cycle",
			content: `moved {
  from = component.one
  to   = component.two
}
moved {
  from = component.two
  to   = component.one
}`,
			want: "moved mappings contain a cycle",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.apf.hcl")
			writeConfig(t, path, test.content)
			_, err := ParseFiles([]string{path})
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), path) {
				t.Fatalf("ParseFiles() error = %v, want %q with source file", err, test.want)
			}
		})
	}
}

func TestParseMovedValidationIsDeterministicAcrossFileOrder(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a.apf.hcl")
	second := filepath.Join(dir, "z.apf.hcl")
	writeConfig(t, first, `moved {
  from = component.old
  to   = component.new
}`)
	writeConfig(t, second, `moved {
  from = component.old
  to   = component.new
}`)

	_, forwardErr := ParseFiles([]string{first, second})
	_, reverseErr := ParseFiles([]string{second, first})
	if forwardErr == nil || reverseErr == nil {
		t.Fatalf("ParseFiles() errors = %v / %v, want duplicate diagnostics", forwardErr, reverseErr)
	}
	if forwardErr.Error() != reverseErr.Error() {
		t.Fatalf("diagnostics differ by input order:\nforward: %s\nreverse: %s", forwardErr, reverseErr)
	}
	if !strings.Contains(forwardErr.Error(), second+":2:moved[component.old].from") || !strings.Contains(forwardErr.Error(), "first defined at "+first+":1") {
		t.Fatalf("duplicate diagnostic = %v", forwardErr)
	}
}

func TestParseMovedInvalidEndpointDoesNotLeakProtectedValue(t *testing.T) {
	secret := "not-a-real-moved-secret"
	path := filepath.Join(t.TempDir(), "protected.apf.hcl")
	writeConfig(t, path, `variable "source" {
  type      = string
  default   = "`+secret+`"
  sensitive = true
  ephemeral = true
}

moved {
  from = component[var.source]
  to   = component.new
}
`)

	_, err := ParseFiles([]string{path})
	if err == nil || !strings.Contains(err.Error(), "moved.from must be a static component.<name> traversal") || strings.Contains(err.Error(), secret) {
		t.Fatalf("ParseFiles() protected moved error = %v", err)
	}
}

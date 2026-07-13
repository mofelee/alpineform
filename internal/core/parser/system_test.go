package parser

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSystemAttributes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.apf.hcl")
	writeConfig(t, path, `
host "node" {
  system {
    hostname = "edge.example"
    timezone = "Asia/Shanghai"
  }
}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	system := config.Hosts["node"].System
	if system == nil || system.Attributes["hostname"].Source.Path != `host["node"].system.hostname` || system.Attributes["timezone"].Source.Path != `host["node"].system.timezone` {
		t.Fatalf("parsed system = %#v", system)
	}
}

func TestParseSystemRejectsLocaleAndInvalidShape(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "locale", body: `system { locale = "C.UTF-8" }`, want: "unsupported on musl Alpine"},
		{name: "unknown", body: `system { domain = "example" }`, want: "unsupported attribute"},
		{name: "nested", body: "system {\n  hostname {}\n}", want: "attribute-only block"},
		{name: "duplicate", body: "system {}\nsystem {}", want: `duplicate host["node"].system block`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.apf.hcl")
			writeConfig(t, path, "host \"node\" {\n"+test.body+"\n}\n")
			_, err := ParseFiles([]string{path})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseFiles() error = %v, want %q", err, test.want)
			}
		})
	}
}

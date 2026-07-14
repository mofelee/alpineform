package parser

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseNftablesNamedTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.apf.hcl")
	writeConfig(t, path, `
host "node" {
  nftables {
    table "edge" {
      family           = "inet"
      content          = "chain input { type filter hook input priority 0; policy accept; }"
      rollback_timeout = "45s"
      on_remove        = "delete"
      lifecycle { prevent_destroy = true }
    }
    table "edge" {
      family  = "ip6"
      content = "chain output { type filter hook output priority 0; policy accept; }"
    }
  }
}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	nftables := config.Hosts["node"].Nftables
	if nftables == nil || len(nftables.Tables) != 2 || nftables.Tables[0].Kind != ResourceNftablesTable || !nftables.Tables[0].Lifecycle.PreventDestroy {
		t.Fatalf("parsed nftables = %#v", nftables)
	}
}

func TestParseNftablesRejectsUnsafeShape(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "attribute", body: `nftables { ownership = "authoritative" }`, want: "unlabeled block containing only table blocks"},
		{name: "unknown block", body: "nftables {\n  ruleset \"all\" {}\n}", want: "unsupported block"},
		{name: "unknown attribute", body: "nftables {\n  table \"edge\" {\n    family = \"inet\"\n    command = \"flush ruleset\"\n  }\n}", want: "unsupported attribute"},
		{name: "ownership mode", body: "nftables {\n  table \"edge\" {\n    ownership = \"authoritative\"\n  }\n}", want: "unsupported attribute"},
		{name: "nested block", body: "nftables {\n  table \"edge\" {\n    rule {}\n  }\n}", want: "unsupported block"},
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

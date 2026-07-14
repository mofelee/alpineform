package plan

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/merge"
	"github.com/mofelee/alpineform/internal/core/parser"
)

func TestNftablesPlansRedactRulesContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.apf.hcl")
	secret := "not-a-real-nftables-plan-secret"
	config := `host "node" {
  nftables {
    table "edge" {
      family  = "inet"
      content = "chain input { ip saddr 192.0.2.5 accept comment \"not-a-real-nftables-plan-secret\"; }"
    }
  }
}
`
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	program, err := merge.Compile(parsed)
	if err != nil {
		t.Fatal(err)
	}
	resourceGraph, err := graph.Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	document := New(resourceGraph, Options{Files: []string{path}, Hosts: []string{"node"}})
	var textOutput, jsonOutput, htmlOutput bytes.Buffer
	PrintText(&textOutput, document, TextOptions{})
	if err := PrintJSON(&jsonOutput, document); err != nil {
		t.Fatal(err)
	}
	if err := PrintHTML(&htmlOutput, document); err != nil {
		t.Fatal(err)
	}
	for name, output := range map[string]string{"text": textOutput.String(), "json": jsonOutput.String(), "html": htmlOutput.String()} {
		if strings.Contains(output, secret) || strings.Contains(output, "192.0.2.5") {
			t.Fatalf("%s plan leaked nftables content: %s", name, output)
		}
		if !strings.Contains(output, `host.node.nftables.table`) {
			t.Fatalf("%s plan omitted stable nftables address: %s", name, output)
		}
	}
	if !strings.Contains(jsonOutput.String(), `"protected": true`) || !strings.Contains(jsonOutput.String(), `"network_disruption": 1`) || !strings.Contains(jsonOutput.String(), `"risks": [`) || !strings.Contains(jsonOutput.String(), `"network_disruption"`) {
		t.Fatalf("JSON plan did not mark nftables content protected: %s", jsonOutput.String())
	}
	if !strings.Contains(textOutput.String(), "risk: network disruption") || !strings.Contains(htmlOutput.String(), "Network disruption: 1") {
		t.Fatalf("nftables risk missing from text/HTML plan:\n%s\n%s", textOutput.String(), htmlOutput.String())
	}
}

func TestNftablesOfflinePlanIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.apf.hcl")
	content := `host "node" {
  nftables {
    table "zeta" {
      family  = "ip6"
      content = "chain output {}"
    }
    table "alpha" {
      family  = "inet"
      content = "chain input {}"
    }
  }
}
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	compile := func() string {
		parsed, err := parser.ParseFiles([]string{path})
		if err != nil {
			t.Fatal(err)
		}
		program, err := merge.Compile(parsed)
		if err != nil {
			t.Fatal(err)
		}
		resourceGraph, err := graph.Compile(program)
		if err != nil {
			t.Fatal(err)
		}
		document := New(resourceGraph, Options{Hosts: []string{"node"}})
		var output bytes.Buffer
		if err := PrintJSON(&output, document); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}
	first := compile()
	second := compile()
	if first != second {
		t.Fatalf("nftables plan changed across repeated compilation:\n%s\n%s", first, second)
	}
}

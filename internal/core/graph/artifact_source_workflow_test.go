package graph_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/merge"
	"github.com/mofelee/alpineform/internal/core/parser"
)

func TestArtifactSourceInputsCompileToHostScopedGraphPayloads(t *testing.T) {
	shaAlpha := strings.Repeat("a", 64)
	shaBeta := strings.Repeat("b", 64)
	urlAlpha := "https://alpha.invalid/tool?token=alpha-protected"
	urlBeta := "https://beta.invalid/tool?token=beta-protected"
	path := filepath.Join(t.TempDir(), "artifact-inputs.apf.hcl")
	config := `
component "tool" {
  input "url" {
    type      = string
    sensitive = true
  }
  input "sha256" {
    type      = string
    ephemeral = true
  }
  type = "binary"
  source {
    url    = input.url
    sha256 = input.sha256
  }
  install { path = "/usr/local/bin/tool" }
}
host "alpha" {
  component "cli" {
    source = component.tool
    inputs = {
      url    = "` + urlAlpha + `"
      sha256 = "` + shaAlpha + `"
    }
  }
}
host "beta" {
  component "cli" {
    source = component.tool
    inputs = {
      url    = "` + urlBeta + `"
      sha256 = "` + shaBeta + `"
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

	nodes := map[string]graph.Node{}
	for _, node := range resourceGraph.Nodes {
		nodes[node.Address] = node
	}
	wantPayloads := map[string]map[string]any{
		`host.alpha.component.cli.artifact.source["any"]`: {"url": urlAlpha, "sha256": shaAlpha},
		`host.beta.component.cli.artifact.source["any"]`:  {"url": urlBeta, "sha256": shaBeta},
	}
	intentDigests := map[string]bool{}
	for address, wantPayload := range wantPayloads {
		node, exists := nodes[address]
		if !exists {
			t.Fatalf("compiled graph lacks stable source address %q", address)
		}
		if !reflect.DeepEqual(node.Payload, wantPayload) {
			t.Fatalf("%s payload = %#v, want %#v", address, node.Payload, wantPayload)
		}
		if !node.Sensitive || !node.Ephemeral || node.Desired["url_sensitive"] != true || node.Desired["sha256_ephemeral"] != true {
			t.Fatalf("%s protected marks = sensitive:%v ephemeral:%v desired:%#v", address, node.Sensitive, node.Ephemeral, node.Desired)
		}
		if _, exists := node.Desired["url"]; exists {
			t.Fatalf("%s retained protected URL in desired state: %#v", address, node.Desired)
		}
		if _, exists := node.Desired["sha256"]; exists {
			t.Fatalf("%s retained protected checksum in desired state: %#v", address, node.Desired)
		}
		if node.ProtectedIntentDigest == "" || intentDigests[node.ProtectedIntentDigest] {
			t.Fatalf("%s protected intent digest = %q", address, node.ProtectedIntentDigest)
		}
		intentDigests[node.ProtectedIntentDigest] = true
	}
}

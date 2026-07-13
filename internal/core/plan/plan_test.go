package plan

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
)

func testDocument() Document {
	resourceGraph := &graph.ResourceGraph{Nodes: []graph.Node{
		{Host: "node", Address: "host.node", Kind: "host", Source: ir.SourceRef{File: "model.apf.hcl", Line: 1, Path: `host["node"]`}},
		{Host: "node", Address: "host.node.file.example", Kind: "file", Managed: true, Summary: "manage example file", Source: ir.SourceRef{File: "model.apf.hcl", Line: 8, Path: `host["node"].file["example"]`}, DependsOn: []string{"host.node"}, Desired: map[string]any{"content": "not-a-real-plan-secret"}, Sensitive: true},
	}}
	return New(resourceGraph, Options{Files: []string{"model.apf.hcl"}, Hosts: []string{"node"}})
}

func TestPlanRenderersMatchGoldenAndDoNotLeak(t *testing.T) {
	document := testDocument()
	var textOutput bytes.Buffer
	PrintText(&textOutput, document, TextOptions{})
	var jsonOutput bytes.Buffer
	if err := PrintJSON(&jsonOutput, document); err != nil {
		t.Fatal(err)
	}
	var htmlOutput bytes.Buffer
	if err := PrintHTML(&htmlOutput, document); err != nil {
		t.Fatal(err)
	}
	for name, output := range map[string][]byte{
		"offline-plan.golden.txt":  textOutput.Bytes(),
		"offline-plan.golden.json": jsonOutput.Bytes(),
		"offline-plan.golden.html": htmlOutput.Bytes(),
	} {
		if strings.Contains(string(output), "not-a-real-plan-secret") {
			t.Fatalf("%s leaked protected value", name)
		}
		assertGolden(t, name, output)
	}
}

func TestTextColorIsExplicit(t *testing.T) {
	var output bytes.Buffer
	PrintText(&output, testDocument(), TextOptions{Color: true})
	if !strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("colored output has no ANSI sequence: %q", output.String())
	}
	output.Reset()
	PrintText(&output, testDocument(), TextOptions{Color: false})
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("plain output contains ANSI sequence: %q", output.String())
	}
}

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s differs from golden\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

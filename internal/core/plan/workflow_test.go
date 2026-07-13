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

func TestModelFixtureWorkflowGolden(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "examples", "model.apf.hcl")
	config, err := parser.ParseFiles([]string{fixture})
	if err != nil {
		t.Fatal(err)
	}
	program, err := merge.Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	resourceGraph, err := graph.Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	document := New(resourceGraph, Options{Files: []string{fixture}, Hosts: []string{"alpine_1"}})

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
		"model-plan.golden.txt":  textOutput.Bytes(),
		"model-plan.golden.json": jsonOutput.Bytes(),
		"model-plan.golden.html": htmlOutput.Bytes(),
	} {
		if strings.Contains(string(output), "example-token") {
			t.Fatalf("%s leaked protected fixture input", name)
		}
		assertGolden(t, name, output)
	}
}

func TestMultiFileCompileOrderIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	variables := filepath.Join(dir, "10-variables.apf.hcl")
	model := filepath.Join(dir, "20-model.apf.hcl")
	if err := os.WriteFile(variables, []byte("variable \"name\" {\n  type    = string\n  default = \"node\"\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(model, []byte(`host "node" {}`), 0600); err != nil {
		t.Fatal(err)
	}
	compile := func() []byte {
		config, err := parser.ParseFiles([]string{model, variables})
		if err != nil {
			t.Fatal(err)
		}
		program, err := merge.Compile(config)
		if err != nil {
			t.Fatal(err)
		}
		resourceGraph, err := graph.Compile(program)
		if err != nil {
			t.Fatal(err)
		}
		document := New(resourceGraph, Options{Files: []string{model, variables}, Hosts: []string{"node"}})
		var output bytes.Buffer
		if err := PrintJSON(&output, document); err != nil {
			t.Fatal(err)
		}
		return output.Bytes()
	}
	first := compile()
	second := compile()
	if !bytes.Equal(first, second) {
		t.Fatalf("multi-file plan drifted:\n%s\n%s", first, second)
	}
}

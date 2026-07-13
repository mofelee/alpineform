package plan

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
	corestate "github.com/mofelee/alpineform/internal/core/state"
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

func TestOfflinePlanRendersExplicitAbsenceAsDelete(t *testing.T) {
	resourceGraph := &graph.ResourceGraph{Nodes: []graph.Node{{
		Host:    "node",
		Address: `host.node.files.file["/tmp/old"]`,
		Kind:    "file",
		Managed: true,
		Desired: map[string]any{"ensure": "absent", "path": "/tmp/old"},
		Source:  ir.SourceRef{File: "model.apf.hcl", Line: 3},
	}}}
	document := New(resourceGraph, Options{Hosts: []string{"node"}})
	if document.Summary.Create != 0 || document.Summary.Delete != 1 || document.Changes[0].Action != "delete" {
		t.Fatalf("offline absent document = %#v", document)
	}
	var output bytes.Buffer
	PrintText(&output, document, TextOptions{})
	if !strings.Contains(output.String(), "  - host.node.files.file") || !strings.Contains(output.String(), "1 to delete") {
		t.Fatalf("offline absent text = %s", output.String())
	}
}

func TestOnlinePlanRendersEveryActionWithoutProtectedValues(t *testing.T) {
	secret := "not-a-real-online-plan-secret"
	host := ir.HostSpec{Name: "node"}
	actions := []string{
		engine.ActionCreate,
		engine.ActionUpdate,
		engine.ActionAdopt,
		engine.ActionDelete,
		engine.ActionDestroy,
		engine.ActionForget,
		engine.ActionNoOp,
	}
	steps := make([]engine.Step, 0, len(actions))
	for _, action := range actions {
		node := graph.Node{
			Address:   "host.node.test." + action,
			Kind:      "test",
			Managed:   true,
			Summary:   action + " test resource",
			Desired:   map[string]any{"content": action},
			Source:    ir.SourceRef{File: "model.apf.hcl", Line: 3},
			DependsOn: []string{"host.node"},
		}
		step := engine.Step{Address: node.Address, Action: action, Summary: node.Summary, Node: node}
		if action == engine.ActionUpdate {
			step.Node.Sensitive = true
			step.Node.Desired = map[string]any{"content": secret}
			step.Observed = engine.ObservedResource{Exists: true, Values: map[string]any{"content": secret}}
		}
		if action == engine.ActionForget {
			step.Node = graph.Node{}
			step.Prior = &corestate.Resource{Kind: "test", Protected: true, Observed: map[string]any{"content": secret}}
		}
		steps = append(steps, step)
	}
	document := NewOnline(engine.Plan{Hosts: []engine.HostPlan{{Host: host, Steps: steps}}}, Options{Files: []string{"model.apf.hcl"}})
	if document.Mode != "online" || document.Summary.Create != 1 || document.Summary.Update != 1 || document.Summary.Adopt != 1 || document.Summary.Delete != 1 || document.Summary.Destroy != 1 || document.Summary.Forget != 1 || document.Summary.NoOp != 1 {
		t.Fatalf("online document summary = %#v", document.Summary)
	}
	var textOutput bytes.Buffer
	PrintText(&textOutput, document, TextOptions{Color: true})
	var jsonOutput bytes.Buffer
	if err := PrintJSON(&jsonOutput, document); err != nil {
		t.Fatal(err)
	}
	var htmlOutput bytes.Buffer
	if err := PrintHTML(&htmlOutput, document); err != nil {
		t.Fatal(err)
	}
	for name, output := range map[string]string{"text": textOutput.String(), "json": jsonOutput.String(), "html": htmlOutput.String()} {
		if strings.Contains(output, secret) {
			t.Fatalf("%s online plan leaked protected value: %s", name, output)
		}
	}
	for name, output := range map[string]string{"text": textOutput.String(), "html": htmlOutput.String()} {
		if !strings.Contains(output, "Online plan") && !strings.Contains(output, "online plan") {
			t.Fatalf("%s online plan lacks mode heading: %s", name, output)
		}
	}
	if !strings.Contains(jsonOutput.String(), `"mode": "online"`) || !strings.Contains(jsonOutput.String(), `"protected": true`) || !strings.Contains(htmlOutput.String(), ">destroy<") {
		t.Fatalf("online JSON/HTML = %s\n%s", jsonOutput.String(), htmlOutput.String())
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

package plan

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
		{Host: "node", Address: "host.node.file.example", Kind: "file", Managed: true, Summary: "manage example file", Source: ir.SourceRef{File: "model.apf.hcl", Line: 8, Path: `host["node"].file["example"]`}, DependsOn: []string{"host.node", "host.node"}, TriggeredBy: []string{"host.node", "host.node"}, Desired: map[string]any{"content": "not-a-real-plan-secret"}, Sensitive: true},
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

func TestMovedPlanRenderersAreSortedStableAndDoNotLeak(t *testing.T) {
	secret := "not-a-real-moved-state-secret"
	first := NewOnline(movedEnginePlan(false, secret), Options{Files: []string{"moved.apf.hcl"}})
	second := NewOnline(movedEnginePlan(true, secret), Options{Files: []string{"moved.apf.hcl"}})
	wantMoves := []Move{
		{Host: "alpha", From: "host.alpha.component.old.files.file.app", To: "host.alpha.component.current.files.file.app"},
		{Host: "alpha", From: "host.alpha.component.old.services.service.api", To: "host.alpha.component.current.services.service.api"},
		{Host: "zeta", From: "host.zeta.component.legacy.packages.package.tool", To: "host.zeta.component.current.packages.package.tool"},
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("moved document depends on input order:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if !reflect.DeepEqual(first.Moves, wantMoves) || !reflect.DeepEqual(first.Hosts, []string{"alpha", "zeta"}) {
		t.Fatalf("sorted moved document = %#v", first)
	}
	if first.FormatVersion != "alpineform.plan.alpha1" || first.Summary != (Summary{Move: len(wantMoves)}) || len(first.Changes) != 0 || len(first.Graph) != 0 {
		t.Fatalf("move-only plan changed format or resource counts: format=%q summary=%#v changes=%d graph=%d", first.FormatVersion, first.Summary, len(first.Changes), len(first.Graph))
	}

	firstText, firstJSON, firstHTML := renderPlanFormats(t, first)
	secondText, secondJSON, secondHTML := renderPlanFormats(t, second)
	for name, pair := range map[string][2][]byte{
		"text": {firstText, secondText},
		"json": {firstJSON, secondJSON},
		"html": {firstHTML, secondHTML},
	} {
		if !bytes.Equal(pair[0], pair[1]) {
			t.Fatalf("%s moved plan is not byte-stable:\n%s\n%s", name, pair[0], pair[1])
		}
		if strings.Contains(string(pair[0]), secret) {
			t.Fatalf("%s moved plan leaked protected prior state", name)
		}
		position := -1
		for _, move := range wantMoves {
			next := strings.Index(string(pair[0]), move.From)
			if next <= position {
				t.Fatalf("%s moves are not sorted: %#v\n%s", name, wantMoves, pair[0])
			}
			position = next
		}
	}
	if strings.Contains(string(firstText), "No remote resource changes.") || !strings.Contains(string(firstText), "Summary: 3 move, 0 create") {
		t.Fatalf("move-only text plan reads as clean:\n%s", firstText)
	}
	if !strings.Contains(string(firstHTML), "<h2>Moves</h2>") || !strings.Contains(string(firstHTML), "3 move;") {
		t.Fatalf("HTML moved plan lacks independent move rendering:\n%s", firstHTML)
	}
	var decoded struct {
		FormatVersion string   `json:"format_version"`
		Summary       Summary  `json:"summary"`
		Moves         []Move   `json:"moves"`
		Changes       []Change `json:"changes"`
	}
	if err := json.Unmarshal(firstJSON, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.FormatVersion != FormatVersion || decoded.Summary != first.Summary || !reflect.DeepEqual(decoded.Moves, wantMoves) || len(decoded.Changes) != 0 {
		t.Fatalf("JSON moved plan = %#v", decoded)
	}

	assertGolden(t, "moved-plan.golden.txt", firstText)
	assertGolden(t, "moved-plan.golden.json", firstJSON)
	assertGolden(t, "moved-plan.golden.html", firstHTML)
}

func TestMovedPlanHTMLEscapesAddresses(t *testing.T) {
	document := NewOnline(engine.Plan{Hosts: []engine.HostPlan{{
		Host: ir.HostSpec{Name: "edge"},
		Moves: []corestate.RealizedMove{{
			Host: "edge<&",
			From: `host.edge.component.old.files.file["<script>"]`,
			To:   `host.edge.component.current.files.file["a&b"]`,
		}},
	}}}, Options{})
	var output bytes.Buffer
	if err := PrintHTML(&output, document); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	if strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") || !strings.Contains(html, "a&amp;b") || !strings.Contains(html, "edge&lt;&amp;") {
		t.Fatalf("HTML moved plan did not escape structural fields:\n%s", html)
	}
}

func movedEnginePlan(reverse bool, secret string) engine.Plan {
	alphaMoves := []corestate.RealizedMove{
		{Host: "alpha", From: "host.alpha.component.old.services.service.api", To: "host.alpha.component.current.services.service.api"},
		{Host: "alpha", From: "host.alpha.component.old.files.file.app", To: "host.alpha.component.current.files.file.app"},
	}
	if reverse {
		alphaMoves[0], alphaMoves[1] = alphaMoves[1], alphaMoves[0]
	}
	prior := corestate.Empty("alpha")
	prior.ComponentIdentities["old"] = corestate.ComponentIdentity{PhysicalName: secret}
	prior.Resources["host.alpha.component.old.files.file.app"] = corestate.Resource{
		Protected: true,
		Desired:   map[string]any{"content": secret},
		Observed:  map[string]any{"content": secret},
		Delete:    map[string]any{"content": secret},
	}
	alpha := engine.HostPlan{Host: ir.HostSpec{Name: "alpha"}, Moves: alphaMoves, PriorState: prior}
	zeta := engine.HostPlan{
		Host: ir.HostSpec{Name: "zeta"},
		Moves: []corestate.RealizedMove{{
			Host: "zeta",
			From: "host.zeta.component.legacy.packages.package.tool",
			To:   "host.zeta.component.current.packages.package.tool",
		}},
	}
	if reverse {
		return engine.Plan{Hosts: []engine.HostPlan{alpha, zeta}}
	}
	return engine.Plan{Hosts: []engine.HostPlan{zeta, alpha}}
}

func renderPlanFormats(t *testing.T, document Document) ([]byte, []byte, []byte) {
	t.Helper()
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
	return textOutput.Bytes(), jsonOutput.Bytes(), htmlOutput.Bytes()
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

func TestPlanNormalizesRelationshipsWithoutMutatingGraph(t *testing.T) {
	dependsOn := []string{"host.node.z", "host.node.a", "host.node.z"}
	triggeredBy := []string{"host.node.z", "host.node.a", "host.node.a"}
	node := graph.Node{
		Address:     "host.node.service.example",
		Kind:        "service",
		Managed:     true,
		DependsOn:   dependsOn,
		TriggeredBy: triggeredBy,
	}
	wantDependsOn := []string{"host.node.a", "host.node.z"}
	wantTriggeredBy := []string{"host.node.a", "host.node.z"}

	offline := New(&graph.ResourceGraph{Nodes: []graph.Node{node}}, Options{})
	assertRelationships := func(name string, graphNode GraphNode, change Change) {
		t.Helper()
		if !reflect.DeepEqual(graphNode.DependsOn, wantDependsOn) || !reflect.DeepEqual(change.DependsOn, wantDependsOn) {
			t.Fatalf("%s depends_on = %#v / %#v", name, graphNode.DependsOn, change.DependsOn)
		}
		if !reflect.DeepEqual(graphNode.TriggeredBy, wantTriggeredBy) || !reflect.DeepEqual(change.TriggeredBy, wantTriggeredBy) {
			t.Fatalf("%s triggered_by = %#v / %#v", name, graphNode.TriggeredBy, change.TriggeredBy)
		}
	}
	assertRelationships("offline", offline.Graph[0], offline.Changes[0])

	online := NewOnline(engine.Plan{Hosts: []engine.HostPlan{{
		Host: ir.HostSpec{Name: "node"},
		Steps: []engine.Step{{
			Address:     node.Address,
			Action:      engine.ActionUpdate,
			Node:        node,
			TriggeredBy: []string{"host.node.z", "host.node.z"},
		}},
	}}}, Options{})
	if !reflect.DeepEqual(online.Graph[0].DependsOn, wantDependsOn) || !reflect.DeepEqual(online.Changes[0].DependsOn, wantDependsOn) {
		t.Fatalf("online depends_on = %#v / %#v", online.Graph[0].DependsOn, online.Changes[0].DependsOn)
	}
	if !reflect.DeepEqual(online.Graph[0].TriggeredBy, wantTriggeredBy) || !reflect.DeepEqual(online.Changes[0].TriggeredBy, []string{"host.node.z"}) {
		t.Fatalf("online structural/active triggered_by = %#v / %#v", online.Graph[0].TriggeredBy, online.Changes[0].TriggeredBy)
	}

	if !reflect.DeepEqual(dependsOn, []string{"host.node.z", "host.node.a", "host.node.z"}) || !reflect.DeepEqual(triggeredBy, []string{"host.node.z", "host.node.a", "host.node.a"}) {
		t.Fatalf("plan construction mutated graph relationships: depends_on=%#v triggered_by=%#v", dependsOn, triggeredBy)
	}
	if offline.FormatVersion != FormatVersion || online.FormatVersion != FormatVersion || FormatVersion != "alpineform.plan.alpha1" {
		t.Fatalf("format version changed: offline=%q online=%q", offline.FormatVersion, online.FormatVersion)
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
	document := NewOnline(engine.Plan{Hosts: []engine.HostPlan{{
		Host:  host,
		Steps: steps,
		Moves: []corestate.RealizedMove{{
			Host: "node",
			From: "host.node.component.old.test.resource",
			To:   "host.node.component.current.test.resource",
		}},
	}}}, Options{Files: []string{"model.apf.hcl"}})
	if document.Mode != "online" || document.Summary.Move != 1 || document.Summary.Create != 1 || document.Summary.Update != 1 || document.Summary.Adopt != 1 || document.Summary.Delete != 1 || document.Summary.Destroy != 1 || document.Summary.Forget != 1 || document.Summary.NoOp != 1 {
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

func TestAuthoritativeAPKPlanShowsCompleteRepositoryReplacement(t *testing.T) {
	node := graph.Node{
		Address: "host.node.apk.repositories", Kind: "apk_repositories", Managed: true,
		Summary: "authoritatively manage /etc/apk/repositories",
		Desired: map[string]any{
			"ownership": "authoritative",
			"lines":     []string{"https://new.example/alpine/v3.24/main", "https://new.example/alpine/v3.24/community"},
		},
	}
	step := engine.Step{
		Address: node.Address, Action: engine.ActionUpdate, Summary: node.Summary, Node: node,
		Observed: engine.ObservedResource{Exists: true, Values: map[string]any{
			"lines": []string{"# external comment", "https://old.example/alpine/v3.24/main"},
		}},
	}
	document := NewOnline(engine.Plan{Hosts: []engine.HostPlan{{Host: ir.HostSpec{Name: "node"}, Steps: []engine.Step{step}}}}, Options{})
	var output bytes.Buffer
	PrintText(&output, document, TextOptions{})
	text := output.String()
	for _, want := range []string{
		"--- observed /etc/apk/repositories",
		"- # external comment",
		"- https://old.example/alpine/v3.24/main",
		"+++ desired /etc/apk/repositories",
		"+ https://new.example/alpine/v3.24/main",
		"+ https://new.example/alpine/v3.24/community",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("authoritative repository plan missing %q:\n%s", want, text)
		}
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

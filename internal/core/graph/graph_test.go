package graph

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func source(line int) ir.SourceRef {
	return ir.SourceRef{File: "model.apf.hcl", Line: line, Path: "test"}
}

func TestValidateAndScheduleDeterministically(t *testing.T) {
	graph := &ResourceGraph{Nodes: []Node{
		{Address: "node.c", Source: source(3)},
		{Address: "node.b", DependsOn: []string{"node.a"}, Source: source(2)},
		{Address: "node.a", Source: source(1)},
	}}
	if err := graph.Validate(); err != nil {
		t.Fatal(err)
	}
	ordered, err := graph.Schedule()
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(ordered))
	for _, node := range ordered {
		got = append(got, node.Address)
	}
	if want := []string{"node.a", "node.b", "node.c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("schedule = %#v, want %#v", got, want)
	}
}

func TestValidateRejectsDuplicateUnknownAndCyclicDependencies(t *testing.T) {
	tests := []struct {
		name  string
		nodes []Node
		want  string
	}{
		{name: "duplicate", nodes: []Node{{Address: "node.a", Source: source(1)}, {Address: "node.a", Source: source(2)}}, want: `duplicate resource address "node.a"`},
		{name: "unknown", nodes: []Node{{Address: "node.a", DependsOn: []string{"node.missing"}, Source: source(1)}}, want: `depends on unknown address "node.missing"`},
		{name: "cycle", nodes: []Node{{Address: "node.a", DependsOn: []string{"node.b"}, Source: source(1)}, {Address: "node.b", DependsOn: []string{"node.a"}, Source: source(2)}}, want: "resource dependency cycle involves: node.a, node.b"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := (&ResourceGraph{Nodes: test.nodes}).Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestProtectedNodeJSONDoesNotLeakDesiredValues(t *testing.T) {
	secret := "not-a-real-graph-secret"
	data, err := json.Marshal(Node{Address: "node.secret", Managed: true, Desired: map[string]any{"content": secret}, Sensitive: true, Source: source(1)})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, secret) || strings.Contains(text, "content") || !strings.Contains(text, `"protected":true`) {
		t.Fatalf("protected node JSON = %s", text)
	}
}

func TestCompileBuildsStructuralGraph(t *testing.T) {
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name:     "node",
		Source:   source(1),
		Platform: &ir.PlatformSpec{Architecture: "amd64", Version: "3.24.1", Branch: "3.24", Libc: "musl", NativeArchitecture: "x86_64", Source: source(2)},
		Components: []ir.ComponentInstanceSpec{
			{Name: "base", Template: "app", Source: source(3)},
			{Name: "worker", Template: "app", DependsOn: []string{"base"}, Source: source(4)},
		},
	}}}
	compiled, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Nodes) != 4 || compiled.ManagedCount() != 0 {
		t.Fatalf("compiled graph = %#v", compiled)
	}
	worker := Node{}
	for _, node := range compiled.Nodes {
		if node.Address == "host.node.component.worker" {
			worker = node
		}
	}
	wantDeps := []string{"host.node", "host.node.component.base"}
	if worker.Address != "host.node.component.worker" || !reflect.DeepEqual(worker.DependsOn, wantDeps) {
		t.Fatalf("worker node = %#v", worker)
	}
}

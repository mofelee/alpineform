package engine

import (
	"context"
	"testing"

	"github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
)

func TestMovedCompletedStatePlansCleanWithRetainedAndRemovedBlock(t *testing.T) {
	host, resourceGraph, initial := movedEngineFixture(t, "node", "1")
	backend := newMemoryBackend()
	backend.states[host.Name] = initial
	provider := newConvergedMovedProvider()
	actionEngine := Engine{Backend: backend, Provider: provider}

	if _, err := actionEngine.Apply(
		context.Background(),
		sharedMovedBuild(&ir.Program{Hosts: []ir.HostSpec{host}}, resourceGraph),
		approvedMovedApply(),
	); err != nil {
		t.Fatal(err)
	}
	migrated, writes := backend.snapshot(host.Name)
	if writes != 1 || migrated.ComponentIdentities[componentRoot(host.Name, "current")].PhysicalName != "old" {
		t.Fatalf("completed migration state = %#v after %d writes", migrated, writes)
	}

	for _, test := range []struct {
		name        string
		retainBlock bool
	}{
		{name: "retained block", retainBlock: true},
		{name: "removed block", retainBlock: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			plannedHost := host
			plannedGraph := resourceGraph
			if !test.retainBlock {
				plannedHost.Moves = nil
				var err error
				plannedGraph, err = graph.Compile(&ir.Program{Hosts: []ir.HostSpec{plannedHost}})
				if err != nil {
					t.Fatal(err)
				}
			}
			build := sharedMovedBuild(&ir.Program{Hosts: []ir.HostSpec{plannedHost}}, plannedGraph)

			plan, err := actionEngine.Plan(context.Background(), build)
			if err != nil {
				t.Fatal(err)
			}
			if plan.HasChanges() || len(plan.Hosts) != 1 || len(plan.Hosts[0].Moves) != 0 || countResourceChanges(plan.Hosts[0]) != 0 {
				t.Fatalf("completed migration plan = %#v", plan)
			}
			component := plan.Hosts[0].Host.Components[0]
			if component.Name != "current" || component.PhysicalComponentName() != "old" || plannedHost.Components[0].PhysicalName != "" {
				t.Fatalf("completed migration binding: input=%#v planned=%#v", plannedHost.Components[0], component)
			}

			checked, err := actionEngine.Check(context.Background(), build)
			if err != nil || checked.HasChanges() || len(checked.Hosts[0].Moves) != 0 {
				t.Fatalf("completed migration check = %#v, error = %v", checked, err)
			}
			state, finalWrites := backend.snapshot(host.Name)
			if finalWrites != writes || state.ComponentIdentities[componentRoot(host.Name, "current")].PhysicalName != "old" {
				t.Fatalf("read-only completed migration changed state = %#v after %d writes, want %d", state, finalWrites, writes)
			}
		})
	}
}

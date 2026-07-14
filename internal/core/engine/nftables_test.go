package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
)

func TestNftablesPreventDestroyBlocksExplicitDeletion(t *testing.T) {
	host := testHost()
	host.Nftables = &ir.NftablesSpec{Tables: []ir.NftablesTableSpec{{
		Name: "edge", Family: "inet", Ensure: "absent", OnRemove: "delete", Sensitive: true,
		RollbackTimeoutSeconds: 30, Lifecycle: ir.LifecycleSpec{PreventDestroy: true},
		Source: ir.SourceRef{File: "main.apf.hcl", Line: 3, Path: `host["node"].nftables.table["edge"]`},
	}}}
	resourceGraph, err := graph.Compile(&ir.Program{Hosts: []ir.HostSpec{host}})
	if err != nil {
		t.Fatal(err)
	}
	address := `host.node.nftables.table["inet/edge"]`
	provider := newMemoryProvider()
	provider.set(address, ObservedResource{Exists: true, Values: map[string]any{"family": "inet", "name": "edge"}})
	actionEngine := Engine{Backend: newMemoryBackend(), Provider: provider}
	_, err = actionEngine.Plan(context.Background(), func(context.Context) (*ir.Program, *graph.ResourceGraph, error) {
		return &ir.Program{Hosts: []ir.HostSpec{host}}, resourceGraph, nil
	})
	if err == nil || !strings.Contains(err.Error(), "prevent_destroy") || !strings.Contains(err.Error(), address) {
		t.Fatalf("nftables prevent_destroy error = %v", err)
	}
	applied, deleted := provider.counts()
	if applied != 0 || deleted != 0 {
		t.Fatalf("provider mutated before prevent_destroy: applied=%d deleted=%d", applied, deleted)
	}
}

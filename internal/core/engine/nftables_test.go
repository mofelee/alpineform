package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

func TestNftablesPreventDestroyBlocksExplicitDeletion(t *testing.T) {
	host := testHost()
	host.Packages = []ir.PackageSpec{{Name: "nftables", WorldIntent: "nftables", Ensure: "present"}}
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
	backend := newMemoryBackend()
	backend.states["node"] = corestate.State{
		Product: corestate.Product, SchemaVersion: corestate.SchemaVersion, Host: "node",
		Resources: map[string]corestate.Resource{address: {Kind: "nftables_table", Ownership: "managed"}},
	}
	actionEngine := Engine{Backend: backend, Provider: provider}
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

func TestNftablesPlanRequiresExplicitAdoptionBeforeMutation(t *testing.T) {
	host := testHost()
	node := graph.Node{
		Host: "node", Address: `host.node.nftables.table["inet/edge"]`, Kind: "nftables_table", Managed: true,
		Desired: map[string]any{"family": "inet", "name": "edge", "ensure": "present", "adopt_existing": false},
		Source:  ir.SourceRef{File: "main.apf.hcl", Line: 3, Path: `host["node"].nftables.table["edge"]`},
	}
	provider := newMemoryProvider()
	provider.set(node.Address, ObservedResource{Exists: true, Digest: corestate.Digest(node.Desired)})
	actionEngine := Engine{Backend: newMemoryBackend(), Provider: provider}
	_, err := actionEngine.Plan(context.Background(), staticBuild(host, node))
	if err == nil || !strings.Contains(err.Error(), "adopt_existing") || !strings.Contains(err.Error(), node.Address) {
		t.Fatalf("implicit nftables adoption error = %v", err)
	}
	node.Desired["adopt_existing"] = true
	provider.set(node.Address, ObservedResource{Exists: true, Digest: corestate.Digest(node.Desired)})
	plan, err := actionEngine.Plan(context.Background(), staticBuild(host, node))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Hosts) != 1 || len(plan.Hosts[0].Steps) != 1 || plan.Hosts[0].Steps[0].Action != ActionAdopt {
		t.Fatalf("explicit nftables adoption plan = %#v", plan)
	}
}

package engine

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

func TestMovedHistoricalChainRebasesFromTrackedSource(t *testing.T) {
	for _, test := range []struct {
		name         string
		source       string
		physical     string
		movesPerNode int
	}{
		{name: "earliest source", source: "old", movesPerNode: 2},
		{name: "intermediate source", source: "middle", physical: "old", movesPerNode: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			trackedHost := movedEngineHost("node", test.source, "1")
			trackedHost.Moves = nil
			if test.physical != "" {
				trackedHost.Components[0] = trackedHost.Components[0].WithPhysicalName(test.physical)
			}
			initial, trackedNodes := stateFromManagedHost(t, trackedHost)
			if test.physical != "" {
				initial.ComponentIdentities[componentRoot("node", test.source)] = corestate.ComponentIdentity{PhysicalName: test.physical}
			}

			targetHost := movedEngineHost("node", "current", "1")
			targetHost.Moves = []ir.MovedSpec{
				movedEngineSpec("node", "old", "middle", 1),
				movedEngineSpec("node", "middle", "current", 4),
			}
			targetGraph, err := graph.Compile(&ir.Program{Hosts: []ir.HostSpec{targetHost}})
			if err != nil {
				t.Fatal(err)
			}
			backend := newMemoryBackend()
			backend.states[targetHost.Name] = initial
			provider := newConvergedMovedProvider()
			plan, err := (Engine{Backend: backend, Provider: provider}).Plan(
				context.Background(),
				sharedMovedBuild(&ir.Program{Hosts: []ir.HostSpec{targetHost}}, targetGraph),
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Hosts) != 1 || countResourceChanges(plan.Hosts[0]) != 0 || len(plan.Hosts[0].Moves) != len(trackedNodes)*test.movesPerNode {
				t.Fatalf("historical chain plan = %#v", plan)
			}
			hostPlan := plan.Hosts[0]
			lineage := moveLineage(hostPlan.Moves)
			for _, step := range hostPlan.Steps {
				prior, exists := hostPlan.PriorState.Resources[step.Address]
				if !exists || prior.DesiredDigest != corestate.Digest(step.Node.Desired) {
					t.Fatalf("rebased prior for %s = %#v, desired = %#v", step.Address, prior, step.Node.Desired)
				}
				wantSource := strings.Replace(step.Address, componentRoot("node", "current"), componentRoot("node", test.source), 1)
				if lineage[step.Address] != wantSource {
					t.Fatalf("lineage[%s] = %q, want %q", step.Address, lineage[step.Address], wantSource)
				}
			}
			identity := hostPlan.PriorState.ComponentIdentities[componentRoot("node", "current")]
			if identity.PhysicalName != "old" {
				t.Fatalf("final chain identity = %#v", hostPlan.PriorState.ComponentIdentities)
			}
			assertProviderInspectedRetainedPhysical(t, provider, "node", "old")
		})
	}
}

func TestMovedSourceBuildRetainsOwnershipWithoutHidingInputChange(t *testing.T) {
	legacyHost := movedSourceBuildHost("old", "v1")
	initial, _ := stateFromManagedHost(t, legacyHost)
	legacyNodes, err := compileManagedHostNodes(legacyHost)
	if err != nil {
		t.Fatal(err)
	}
	legacyByKind := managedNodesByKind(legacyNodes)

	unchangedHost := movedSourceBuildHost("current", "v1")
	unchangedHost.Moves = []ir.MovedSpec{movedEngineSpec("node", "old", "current", 1)}
	unchangedBase, err := compileManagedHostNodes(unchangedHost)
	if err != nil {
		t.Fatal(err)
	}
	plannedHost, plannedNodes, moved, err := prepareHostPlan(unchangedHost, unchangedBase, initial)
	if err != nil {
		t.Fatal(err)
	}
	if plannedHost.Components[0].Name != "current" || plannedHost.Components[0].PhysicalComponentName() != "old" || unchangedHost.Components[0].PhysicalName != "" {
		t.Fatalf("source-build host binding: original=%#v planned=%#v", unchangedHost.Components[0], plannedHost.Components[0])
	}
	plannedByKind := managedNodesByKind(plannedNodes)
	assertSourceBuildPhysicalFields(t, legacyByKind, plannedByKind, true)
	for _, node := range plannedNodes {
		resource, exists := moved.State.Resources[node.Address]
		if !exists || resource.DesiredDigest != corestate.Digest(node.Desired) {
			t.Fatalf("rename-only source-build digest for %s = %#v", node.Address, resource)
		}
	}

	changedHost := movedSourceBuildHost("current", "v2")
	changedHost.Moves = []ir.MovedSpec{movedEngineSpec("node", "old", "current", 1)}
	changedBase, err := compileManagedHostNodes(changedHost)
	if err != nil {
		t.Fatal(err)
	}
	_, changedNodes, changedMove, err := prepareHostPlan(changedHost, changedBase, initial)
	if err != nil {
		t.Fatal(err)
	}
	changedByKind := managedNodesByKind(changedNodes)
	assertSourceBuildPhysicalFields(t, legacyByKind, changedByKind, false)

	dependencies := changedByKind["component_build_dependencies"]
	legacyDependencies := legacyByKind["component_build_dependencies"]
	prior := changedMove.State.Resources[dependencies.Address]
	if prior.DesiredDigest != corestate.Digest(legacyDependencies.Desired) || prior.DesiredDigest == corestate.Digest(dependencies.Desired) {
		t.Fatalf("real source-build change was rebased away: prior=%q legacy=%q target=%q", prior.DesiredDigest, corestate.Digest(legacyDependencies.Desired), corestate.Digest(dependencies.Desired))
	}
	provider := newConvergedMovedProvider()
	for _, node := range changedNodes {
		resource := changedMove.State.Resources[node.Address]
		provider.observed[node.Address] = ObservedResource{Exists: true, Digest: resource.DesiredDigest}
	}
	hostPlan, err := (Engine{Provider: provider}).planHost(context.Background(), changedHost, changedNodes, changedMove.State, changedMove.Moves)
	if err != nil {
		t.Fatal(err)
	}
	if countResourceChanges(hostPlan) == 0 {
		t.Fatalf("source-build input change produced no resource updates: %#v", hostPlan)
	}
}

func movedEngineSpec(host, from, to string, line int) ir.MovedSpec {
	return ir.MovedSpec{
		From:       componentRoot(host, from),
		To:         componentRoot(host, to),
		Source:     ir.SourceRef{File: "main.apf.hcl", Line: line, Path: "moved"},
		FromSource: ir.SourceRef{File: "main.apf.hcl", Line: line + 1, Path: "moved.from"},
		ToSource:   ir.SourceRef{File: "main.apf.hcl", Line: line + 2, Path: "moved.to"},
	}
}

func stateFromManagedHost(t *testing.T, host ir.HostSpec) (corestate.State, []graph.Node) {
	t.Helper()
	nodes, err := compileManagedHostNodes(host)
	if err != nil {
		t.Fatal(err)
	}
	state := corestate.Empty(host.Name)
	state.Serial = 7
	for index, node := range nodes {
		state.Resources[node.Address] = corestate.Resource{
			Host: host.Name, Kind: node.Kind, Ownership: "managed", Order: index + 1,
			DesiredDigest: corestate.Digest(node.Desired), DeleteBehavior: stringValueForTest(node.Desired, "delete_behavior"),
		}
	}
	return state, nodes
}

func movedSourceBuildHost(componentName, version string) ir.HostSpec {
	host := testHost()
	host.Name = "node"
	host.SSH.Host = "node"
	payload := fmt.Sprintf("source-%s", version)
	payloadSHA := fmt.Sprintf("%x", sha256.Sum256([]byte(payload)))
	document := &ir.ComponentBuildIdentityDocument{
		Template: "tool", Instance: componentName,
		Inputs:           []ir.ComponentBuildInputIdentity{{Name: "source", Kind: "content", Identity: "version:" + version, Destination: "main.c"}},
		Commands:         []ir.ComponentBuildCommandIdentity{{Argv: []string{"cc", "-o", "tool", "main.c"}}},
		WorkingDirectory: ".", Output: "tool", MaxOutputBytes: 1024, Dependencies: []string{"build-base"}, Network: "none",
		Install: ir.ComponentBuildInstallIdentity{Path: "/usr/local/bin/tool", Owner: "root", Group: "root", Mode: "0755"},
	}
	host.Components = []ir.ComponentInstanceSpec{{
		Name: componentName, Template: "tool", ArtifactType: "source",
		Build: &ir.ComponentBuildSpec{
			Identity: document.DigestForInstance(componentName), IdentityDocument: document,
			WorkingDirectory: ".", Output: "tool", MaxOutputBytes: 1024, Dependencies: []string{"build-base"}, Network: "none", OnRemove: "destroy", Sensitive: true,
			Inputs: []ir.ComponentBuildInputSpec{{
				Name: "source", Kind: "content", Content: []byte(payload), ContentVersion: version,
				SHA256: payloadSHA, PayloadSHA256: payloadSHA, Destination: "main.c", Sensitive: true,
				Source: ir.SourceRef{File: "main.apf.hcl", Line: 4, Path: "component.tool.build.input.source"},
			}},
			Commands: []ir.ComponentBuildCommandSpec{{Argv: []string{"cc", "-o", "tool", "main.c"}, Source: ir.SourceRef{File: "main.apf.hcl", Line: 5, Path: "component.tool.build.command"}}},
			Source:   ir.SourceRef{File: "main.apf.hcl", Line: 3, Path: "component.tool.build"},
		},
		Install: &ir.ComponentArtifactInstallSpec{Path: "/usr/local/bin/tool", Owner: "root", Group: "root", Mode: "0755", Source: ir.SourceRef{File: "main.apf.hcl", Line: 6, Path: "component.tool.install"}},
		Source:  ir.SourceRef{File: "main.apf.hcl", Line: 2, Path: "component.tool"},
	}}
	return host
}

func managedNodesByKind(nodes []graph.Node) map[string]graph.Node {
	out := make(map[string]graph.Node, len(nodes))
	for _, node := range nodes {
		out[node.Kind] = node
	}
	return out
}

func assertSourceBuildPhysicalFields(t *testing.T, legacy, target map[string]graph.Node, unchanged bool) {
	t.Helper()
	for _, field := range []struct {
		kind string
		key  string
	}{
		{kind: "component_build_dependencies", key: "owner_id"},
		{kind: "component_build_dependencies", key: "virtual_package"},
		{kind: "component_build_dependencies", key: "marker_path"},
		{kind: "component_build_install", key: "install_marker"},
	} {
		if legacy[field.kind].Desired[field.key] != target[field.kind].Desired[field.key] {
			t.Fatalf("retained source-build %s.%s changed: %v != %v", field.kind, field.key, legacy[field.kind].Desired[field.key], target[field.kind].Desired[field.key])
		}
	}
	for _, field := range []struct {
		kind string
		key  string
	}{
		{kind: "component_build_dependencies", key: "build_identity"},
		{kind: "component_build_workspace", key: "workspace"},
		{kind: "component_build_output", key: "cache_path"},
		{kind: "component_build_output", key: "marker_path"},
		{kind: "component_build_input", key: "path"},
	} {
		equal := legacy[field.kind].Desired[field.key] == target[field.kind].Desired[field.key]
		if equal != unchanged {
			t.Fatalf("source-build %s.%s equality = %v, want %v", field.kind, field.key, equal, unchanged)
		}
	}
}

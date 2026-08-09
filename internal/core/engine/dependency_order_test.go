package engine

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

const (
	dependencyPackage = `host.node.packages.package["worker-daemon"]`
	dependencyFile    = `host.node.files.file["/etc/worker/worker.conf"]`
	dependencyService = `host.node.services.service["worker"]`
)

func TestApplyReversesAuthoredExplicitAbsence(t *testing.T) {
	packageNode := dependencyTestNode(dependencyPackage, "package", map[string]any{"ensure": "absent"})
	fileNode := dependencyTestNode(dependencyFile, "file", map[string]any{"ensure": "absent"})
	fileNode.DependsOn = []string{dependencyPackage}
	fileNode.ExplicitDependsOn = []string{dependencyPackage}

	backend := dependencyBackend(packageNode, fileNode)
	provider := newMemoryProvider()
	provider.set(dependencyPackage, ObservedResource{Exists: true})
	provider.set(dependencyFile, ObservedResource{Exists: true})
	plan, err := (Engine{Backend: backend, Provider: provider}).Apply(
		context.Background(),
		staticBuild(testHost(), packageNode, fileNode),
		acceptDependencyPlan(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := dependencyStepAddresses(plan.Hosts[0].Steps); !reflect.DeepEqual(got, []string{dependencyFile, dependencyPackage}) {
		t.Fatalf("explicit absence order = %#v", got)
	}
	if got := deletedDependencyAddresses(provider); !reflect.DeepEqual(got, []string{dependencyFile, dependencyPackage}) {
		t.Fatalf("provider deletion order = %#v", got)
	}
	state, _ := backend.snapshot("node")
	if len(state.Resources) != 0 {
		t.Fatalf("deleted resources remain in state: %#v", state.Resources)
	}
}

func TestApplyStopsServiceBeforeDeletingExplicitDependencies(t *testing.T) {
	packageNode := dependencyTestNode(dependencyPackage, "package", map[string]any{"ensure": "absent"})
	fileNode := dependencyTestNode(dependencyFile, "file", map[string]any{"ensure": "absent"})
	fileNode.DependsOn = []string{dependencyPackage}
	fileNode.ExplicitDependsOn = []string{dependencyPackage}
	serviceDesired := map[string]any{"state": "stopped", "enabled": false}
	serviceNode := dependencyTestNode(dependencyService, "service", serviceDesired)
	serviceNode.DependsOn = []string{dependencyPackage, dependencyFile}
	serviceNode.ExplicitDependsOn = []string{dependencyFile}

	backend := dependencyBackend(packageNode, fileNode, serviceNode)
	state := backend.states["node"]
	serviceState := state.Resources[dependencyService]
	serviceState.DesiredDigest = corestate.Digest(map[string]any{"state": "running", "enabled": true})
	state.Resources[dependencyService] = serviceState
	backend.states["node"] = state
	provider := newMemoryProvider()
	provider.set(dependencyPackage, ObservedResource{Exists: true})
	provider.set(dependencyFile, ObservedResource{Exists: true})
	provider.set(dependencyService, ObservedResource{Exists: true, Digest: serviceState.DesiredDigest})

	plan, err := (Engine{Backend: backend, Provider: provider}).Apply(
		context.Background(),
		staticBuild(testHost(), packageNode, fileNode, serviceNode),
		acceptDependencyPlan(),
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{dependencyService, dependencyFile, dependencyPackage}
	if got := dependencyStepAddresses(plan.Hosts[0].Steps); !reflect.DeepEqual(got, want) {
		t.Fatalf("service teardown order = %#v, want %#v", got, want)
	}
	provider.mu.Lock()
	applied := append([]Step(nil), provider.applied...)
	provider.mu.Unlock()
	if len(applied) != 1 || applied[0].Address != dependencyService {
		t.Fatalf("service teardown applies = %#v", applied)
	}
	if got := deletedDependencyAddresses(provider); !reflect.DeepEqual(got, []string{dependencyFile, dependencyPackage}) {
		t.Fatalf("service teardown deletions = %#v", got)
	}
}

func TestOrderStepsForExecutionPrefersAuthoredTeardownPathRegardlessOfInputOrder(t *testing.T) {
	packageStep := Step{
		Address: dependencyPackage,
		Action:  ActionDelete,
		Node:    graph.Node{Address: dependencyPackage},
	}
	fileStep := Step{
		Address: dependencyFile,
		Action:  ActionDelete,
		Node: graph.Node{
			Address:           dependencyFile,
			DependsOn:         []string{dependencyPackage},
			ExplicitDependsOn: []string{dependencyPackage},
		},
	}
	serviceStep := Step{
		Address: dependencyService,
		Action:  ActionUpdate,
		Node: graph.Node{
			Address:           dependencyService,
			DependsOn:         []string{dependencyPackage, dependencyFile},
			ExplicitDependsOn: []string{dependencyFile},
		},
	}

	ordered, err := orderStepsForExecution([]Step{serviceStep, fileStep, packageStep})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{dependencyService, dependencyFile, dependencyPackage}
	if got := dependencyStepAddresses(ordered); !reflect.DeepEqual(got, want) {
		t.Fatalf("service teardown order = %#v, want %#v", got, want)
	}
}

func TestApplyOrdersOrphanBeforeCurrentDependencyRemoval(t *testing.T) {
	packageNode := dependencyTestNode(dependencyPackage, "package", map[string]any{"ensure": "absent"})
	fileNode := dependencyTestNode(dependencyFile, "file", map[string]any{"ensure": "absent"})
	fileNode.DependsOn = []string{dependencyPackage}
	fileNode.ExplicitDependsOn = []string{dependencyPackage}
	backend := dependencyBackend(packageNode, fileNode)
	state := backend.states["node"]
	state.Resources[dependencyService] = corestate.Resource{
		Host: "node", Kind: "service", Order: 99, DependsOn: []string{dependencyFile},
	}
	backend.states["node"] = state
	provider := newMemoryProvider()
	provider.set(dependencyPackage, ObservedResource{Exists: true})
	provider.set(dependencyFile, ObservedResource{Exists: true})

	plan, err := (Engine{Backend: backend, Provider: provider}).Apply(
		context.Background(),
		staticBuild(testHost(), packageNode, fileNode),
		acceptDependencyPlan(),
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{dependencyService, dependencyFile, dependencyPackage}
	if got := dependencyStepAddresses(plan.Hosts[0].Steps); !reflect.DeepEqual(got, want) {
		t.Fatalf("mixed orphan/current order = %#v, want %#v", got, want)
	}
	if plan.Hosts[0].Steps[0].Action != ActionForget {
		t.Fatalf("orphan action = %q, want forget", plan.Hosts[0].Steps[0].Action)
	}
	if got := deletedDependencyAddresses(provider); !reflect.DeepEqual(got, []string{dependencyFile, dependencyPackage}) {
		t.Fatalf("mixed provider deletion order = %#v", got)
	}
	if applied, deleted := provider.counts(); applied != 0 || deleted != 2 {
		t.Fatalf("provider calls: applied=%d deleted=%d", applied, deleted)
	}
}

func TestApplyOrdersAllOrphansWithoutChangingForgetPolicy(t *testing.T) {
	for _, test := range []struct {
		name            string
		packageBehavior string
		fileBehavior    string
		serviceBehavior string
		wantDeleted     []string
	}{
		{name: "default forget", wantDeleted: []string{}},
		{
			name: "destroy", packageBehavior: ActionDestroy, fileBehavior: ActionDestroy, serviceBehavior: ActionDestroy,
			wantDeleted: []string{dependencyService, dependencyFile, dependencyPackage},
		},
		{
			name: "mixed forget and destroy", packageBehavior: ActionDestroy, fileBehavior: ActionDestroy,
			wantDeleted: []string{dependencyFile, dependencyPackage},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := newMemoryBackend()
			backend.states["node"] = corestate.State{
				Product: corestate.Product, SchemaVersion: corestate.SchemaVersion, Host: "node",
				Resources: map[string]corestate.Resource{
					dependencyPackage: {Host: "node", Kind: "package", Order: 3, DeleteBehavior: test.packageBehavior},
					dependencyFile: {
						Host: "node", Kind: "file", Order: 2, DeleteBehavior: test.fileBehavior,
						DependsOn: []string{dependencyPackage},
					},
					dependencyService: {
						Host: "node", Kind: "service", Order: 1, DeleteBehavior: test.serviceBehavior,
						DependsOn: []string{dependencyFile},
					},
				},
			}
			provider := newMemoryProvider()
			plan, err := (Engine{Backend: backend, Provider: provider}).Apply(
				context.Background(), staticBuild(testHost()), acceptDependencyPlan(),
			)
			if err != nil {
				t.Fatal(err)
			}
			wantOrder := []string{dependencyService, dependencyFile, dependencyPackage}
			if got := dependencyStepAddresses(plan.Hosts[0].Steps); !reflect.DeepEqual(got, wantOrder) {
				t.Fatalf("all-orphan order = %#v, want %#v", got, wantOrder)
			}
			if got := deletedDependencyAddresses(provider); !reflect.DeepEqual(got, test.wantDeleted) {
				t.Fatalf("provider deletions = %#v, want %#v", got, test.wantDeleted)
			}
			state, _ := backend.snapshot("node")
			if len(state.Resources) != 0 {
				t.Fatalf("orphan resources remain in state: %#v", state.Resources)
			}
		})
	}
}

func TestApplySynchronizesExplicitDependenciesWithoutProviderWork(t *testing.T) {
	packageDesired := map[string]any{"ensure": "present", "name": "worker-daemon"}
	fileDesired := map[string]any{"ensure": "present", "path": "/etc/worker/worker.conf"}
	packageNode := dependencyTestNode(dependencyPackage, "package", packageDesired)
	fileNode := dependencyTestNode(dependencyFile, "file", fileDesired)
	fileNode.DependsOn = []string{dependencyPackage}
	fileNode.ExplicitDependsOn = []string{dependencyPackage, dependencyPackage}
	backend := dependencyBackend(packageNode, fileNode)
	state := backend.states["node"]
	packageState := state.Resources[dependencyPackage]
	packageState.DependsOn = []string{"host.node.packages.package[\"stale\"]"}
	state.Resources[dependencyPackage] = packageState
	fileState := state.Resources[dependencyFile]
	fileState.DependsOn = []string{"host.node.packages.package[\"stale\"]"}
	state.Resources[dependencyFile] = fileState
	backend.states["node"] = state
	provider := newMemoryProvider()
	provider.set(dependencyPackage, ObservedResource{Exists: true, Digest: corestate.Digest(packageDesired)})
	provider.set(dependencyFile, ObservedResource{Exists: true, Digest: corestate.Digest(fileDesired)})

	plan, err := (Engine{Backend: backend, Provider: provider}).Apply(
		context.Background(), staticBuild(testHost(), packageNode, fileNode), acceptDependencyPlan(),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range plan.Hosts[0].Steps {
		if step.Action != ActionNoOp {
			t.Fatalf("metadata-only step = %#v", step)
		}
	}
	if applied, deleted := provider.counts(); applied != 0 || deleted != 0 {
		t.Fatalf("metadata synchronization caused provider work: applied=%d deleted=%d", applied, deleted)
	}
	got, _ := backend.snapshot("node")
	if dependencies := got.Resources[dependencyPackage].DependsOn; len(dependencies) != 0 {
		t.Fatalf("stale package dependencies remain: %#v", dependencies)
	}
	if want := []string{dependencyPackage}; !reflect.DeepEqual(got.Resources[dependencyFile].DependsOn, want) {
		t.Fatalf("file dependencies = %#v, want %#v", got.Resources[dependencyFile].DependsOn, want)
	}
}

func TestApplyReconcilesDependenciesAgainstFinalTrackedState(t *testing.T) {
	t.Run("new target", func(t *testing.T) {
		packageDesired := map[string]any{"ensure": "present", "name": "worker-daemon"}
		fileDesired := map[string]any{"ensure": "present", "path": "/etc/worker/worker.conf"}
		packageNode := dependencyTestNode(dependencyPackage, "package", packageDesired)
		fileNode := dependencyTestNode(dependencyFile, "file", fileDesired)
		fileNode.DependsOn = []string{dependencyPackage}
		fileNode.ExplicitDependsOn = []string{dependencyPackage}
		backend := dependencyBackend(fileNode)
		provider := newMemoryProvider()
		provider.set(dependencyFile, ObservedResource{Exists: true, Digest: corestate.Digest(fileDesired)})

		if _, err := (Engine{Backend: backend, Provider: provider}).Apply(
			context.Background(), staticBuild(testHost(), packageNode, fileNode), acceptDependencyPlan(),
		); err != nil {
			t.Fatal(err)
		}
		got, _ := backend.snapshot("node")
		if want := []string{dependencyPackage}; !reflect.DeepEqual(got.Resources[dependencyFile].DependsOn, want) {
			t.Fatalf("dependency on newly tracked target = %#v, want %#v", got.Resources[dependencyFile].DependsOn, want)
		}
	})

	t.Run("removed target", func(t *testing.T) {
		packageNode := dependencyTestNode(dependencyPackage, "package", map[string]any{"ensure": "absent"})
		fileDesired := map[string]any{"ensure": "present", "path": "/etc/worker/worker.conf"}
		fileNode := dependencyTestNode(dependencyFile, "file", fileDesired)
		fileNode.DependsOn = []string{dependencyPackage}
		fileNode.ExplicitDependsOn = []string{dependencyPackage}
		backend := dependencyBackend(packageNode, fileNode)
		provider := newMemoryProvider()
		provider.set(dependencyPackage, ObservedResource{Exists: true})
		provider.set(dependencyFile, ObservedResource{Exists: true, Digest: corestate.Digest(fileDesired)})

		if _, err := (Engine{Backend: backend, Provider: provider}).Apply(
			context.Background(), staticBuild(testHost(), packageNode, fileNode), acceptDependencyPlan(),
		); err != nil {
			t.Fatal(err)
		}
		got, _ := backend.snapshot("node")
		if _, exists := got.Resources[dependencyPackage]; exists {
			t.Fatal("removed dependency target remains tracked")
		}
		if dependencies := got.Resources[dependencyFile].DependsOn; len(dependencies) != 0 {
			t.Fatalf("removed target was reintroduced: %#v", dependencies)
		}
	})
}

func TestMoveOnlyApplyRebasesExplicitDependencyStateWithoutProviderWork(t *testing.T) {
	legacy := movedEngineHost("node", "old", "1")
	addMovedDependencyResources(&legacy)
	legacyGraph, err := graph.Compile(&ir.Program{Hosts: []ir.HostSpec{legacy}})
	if err != nil {
		t.Fatal(err)
	}
	initial := corestate.Empty("node")
	initial.Serial = 7
	order := 0
	for _, node := range legacyGraph.Nodes {
		if !node.Managed {
			continue
		}
		order++
		initial.Resources[node.Address] = corestate.Resource{
			Host: node.Host, Kind: node.Kind, Ownership: "managed", Order: order,
			DesiredDigest: corestate.Digest(node.Desired), DependsOn: append([]string(nil), node.ExplicitDependsOn...),
		}
	}

	current := movedEngineHost("node", "current", "1")
	addMovedDependencyResources(&current)
	current.Moves = []ir.MovedSpec{{
		From: componentRoot("node", "old"), To: componentRoot("node", "current"),
		Source:     ir.SourceRef{File: "main.apf.hcl", Line: 1, Path: "moved"},
		FromSource: ir.SourceRef{File: "main.apf.hcl", Line: 2, Path: "moved.from"},
		ToSource:   ir.SourceRef{File: "main.apf.hcl", Line: 3, Path: "moved.to"},
	}}
	currentGraph, err := graph.Compile(&ir.Program{Hosts: []ir.HostSpec{current}})
	if err != nil {
		t.Fatal(err)
	}
	backend := newMemoryBackend()
	backend.states["node"] = initial
	provider := newConvergedMovedProvider()
	plan, err := (Engine{Backend: backend, Provider: provider}).Apply(
		context.Background(),
		sharedMovedBuild(&ir.Program{Hosts: []ir.HostSpec{current}}, currentGraph),
		acceptDependencyPlan(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Hosts) != 1 || len(plan.Hosts[0].Moves) == 0 || countResourceChanges(plan.Hosts[0]) != 0 {
		t.Fatalf("move-only dependency plan = %#v", plan)
	}
	if applied, deleted := provider.mutationCounts(); applied != 0 || deleted != 0 {
		t.Fatalf("move-only dependency apply mutated provider: applied=%d deleted=%d", applied, deleted)
	}
	state, writes := backend.snapshot("node")
	if writes != 1 {
		t.Fatalf("move-only dependency writes = %d, want 1", writes)
	}
	currentPackage := componentRoot("node", "current") + `.packages.package["worker-daemon"]`
	currentFile := componentRoot("node", "current") + `.files.file["/etc/worker/worker.conf"]`
	if want := []string{currentPackage}; !reflect.DeepEqual(state.Resources[currentFile].DependsOn, want) {
		t.Fatalf("moved dependency state = %#v, want %#v", state.Resources[currentFile].DependsOn, want)
	}
	for address, resource := range state.Resources {
		if stringsHasComponentPrefix(address, componentRoot("node", "old")) {
			t.Fatalf("legacy resource remains after move: %s", address)
		}
		for _, dependency := range resource.DependsOn {
			if stringsHasComponentPrefix(dependency, componentRoot("node", "old")) {
				t.Fatalf("legacy dependency remains after move: %s -> %s", address, dependency)
			}
		}
	}
}

func TestOrderStepsForExecutionPreservesInferredAbsenceEdges(t *testing.T) {
	child := Step{Address: "host.node.file.child", Action: ActionDelete, Node: graph.Node{Address: "host.node.file.child"}}
	parent := Step{
		Address: "host.node.directory.parent", Action: ActionDelete,
		Node: graph.Node{Address: "host.node.directory.parent", DependsOn: []string{child.Address}},
	}
	ordered, err := orderStepsForExecution([]Step{child, parent})
	if err != nil {
		t.Fatal(err)
	}
	if got := dependencyStepAddresses(ordered); !reflect.DeepEqual(got, []string{child.Address, parent.Address}) {
		t.Fatalf("inferred absence order = %#v", got)
	}
}

func TestOrderStepsForExecutionReportsDeterministicActionCycle(t *testing.T) {
	a := Step{Address: "node.a", Action: ActionDestroy, Prior: &corestate.Resource{DependsOn: []string{"node.b"}}}
	b := Step{Address: "node.b", Action: ActionDestroy, Prior: &corestate.Resource{DependsOn: []string{"node.a"}}}
	_, err := orderStepsForExecution([]Step{b, a})
	want := "resource action dependency cycle: node.a -> node.b -> node.a"
	if err == nil || err.Error() != want {
		t.Fatalf("action cycle error = %v, want %q", err, want)
	}
}

func TestRemoveStateDependencyReferencesRemovesDuplicatesWithoutAliasing(t *testing.T) {
	removed := "host.node.package.removed"
	retained := "host.node.file.retained"
	original := []string{removed, "host.node.package.kept", removed}
	resources := map[string]corestate.Resource{retained: {DependsOn: original}}
	removeStateDependencyReferences(resources, removed)
	if want := []string{"host.node.package.kept"}; !reflect.DeepEqual(resources[retained].DependsOn, want) {
		t.Fatalf("dependencies after removal = %#v, want %#v", resources[retained].DependsOn, want)
	}
	if !reflect.DeepEqual(original, []string{removed, "host.node.package.kept", removed}) {
		t.Fatalf("dependency removal mutated shared input: %#v", original)
	}
}

func TestPlanFingerprintIncludesCanonicalPriorDependencies(t *testing.T) {
	step := Step{Address: dependencyService, Action: ActionForget, Prior: &corestate.Resource{DependsOn: []string{dependencyFile, dependencyPackage}}}
	fingerprint := func(value Step) string {
		return planFingerprint(HostPlan{Host: testHost(), Steps: []Step{value}})
	}
	want := fingerprint(step)
	permuted := step
	copyPrior := *step.Prior
	copyPrior.DependsOn = []string{dependencyPackage, dependencyFile, dependencyPackage}
	permuted.Prior = &copyPrior
	if got := fingerprint(permuted); got != want {
		t.Fatalf("prior dependency permutation changed fingerprint: %q != %q", got, want)
	}
	changed := step
	changedPrior := *step.Prior
	changedPrior.DependsOn = []string{dependencyPackage}
	changed.Prior = &changedPrior
	if got := fingerprint(changed); got == want {
		t.Fatalf("prior dependency change did not affect fingerprint %q", got)
	}
}

func dependencyTestNode(address, kind string, desired map[string]any) graph.Node {
	return graph.Node{Host: "node", Address: address, Kind: kind, Managed: true, Desired: desired, DigestSafe: true}
}

func addMovedDependencyResources(host *ir.HostSpec) {
	component := &host.Components[0]
	prefix := componentRoot(host.Name, component.Name)
	component.Packages = []ir.PackageSpec{{
		Name: "worker-daemon", WorldIntent: "worker-daemon", Ensure: "present",
		Source: ir.SourceRef{File: "main.apf.hcl", Line: 20, Path: "component.tool.package.worker-daemon"},
	}}
	component.Files = []ir.ManagedFileSpec{{
		Path: "/etc/worker/worker.conf", Ensure: "present",
		Source: ir.SourceRef{File: "main.apf.hcl", Line: 21, Path: "component.tool.file.worker"},
	}}
	component.ExplicitDependencies = []ir.ResourceDependencySpec{{
		From:      prefix + `.files.file["/etc/worker/worker.conf"]`,
		DependsOn: prefix + `.packages.package["worker-daemon"]`,
		Source:    ir.SourceRef{File: "main.apf.hcl", Line: 22, Path: "component.tool.file.worker.depends_on[0]"},
	}}
}

func stringsHasComponentPrefix(address, prefix string) bool {
	return address == prefix || strings.HasPrefix(address, prefix+".")
}

func dependencyBackend(nodes ...graph.Node) *memoryBackend {
	backend := newMemoryBackend()
	state := corestate.State{
		Product: corestate.Product, SchemaVersion: corestate.SchemaVersion, Host: "node",
		Resources: map[string]corestate.Resource{},
	}
	for index, node := range nodes {
		state.Resources[node.Address] = corestate.Resource{
			Host: node.Host, Kind: node.Kind, Ownership: "managed", DesiredDigest: corestate.Digest(node.Desired), Order: index + 1,
		}
	}
	backend.states["node"] = state
	return backend
}

func acceptDependencyPlan() ApplyOptions {
	return ApplyOptions{
		ReviewPreview: func(context.Context, Plan) error { return nil },
		ReviewLocked:  func(context.Context, Plan, Plan, bool) error { return nil },
	}
}

func dependencyStepAddresses(steps []Step) []string {
	addresses := make([]string, 0, len(steps))
	for _, step := range steps {
		addresses = append(addresses, step.Address)
	}
	return addresses
}

func deletedDependencyAddresses(provider *memoryProvider) []string {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	addresses := make([]string, 0, len(provider.deleted))
	for _, step := range provider.deleted {
		addresses = append(addresses, step.Address)
	}
	return addresses
}

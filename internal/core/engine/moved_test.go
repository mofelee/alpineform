package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

var errMovedStateWrite = errors.New("injected moved-state write failure")

type convergedMovedProvider struct {
	mu        sync.Mutex
	observed  map[string]ObservedResource
	inspected []graph.Node
	applied   []Step
	deleted   []Step
	applyErr  error
}

func newConvergedMovedProvider() *convergedMovedProvider {
	return &convergedMovedProvider{observed: map[string]ObservedResource{}}
}

func (provider *convergedMovedProvider) Inspect(_ context.Context, node graph.Node) (ObservedResource, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.inspected = append(provider.inspected, node)
	if observed, exists := provider.observed[node.Address]; exists {
		return observed, nil
	}
	return ObservedResource{Exists: true, Digest: corestate.Digest(node.Desired)}, nil
}

func (provider *convergedMovedProvider) Apply(_ context.Context, step Step) (ObservedResource, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.applied = append(provider.applied, step)
	if provider.applyErr != nil {
		return ObservedResource{}, provider.applyErr
	}
	return ObservedResource{Exists: true, Digest: corestate.Digest(step.Node.Desired)}, nil
}

func (provider *convergedMovedProvider) Delete(_ context.Context, step Step) error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.deleted = append(provider.deleted, step)
	return nil
}

func (provider *convergedMovedProvider) mutationCounts() (int, int) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return len(provider.applied), len(provider.deleted)
}

func (provider *convergedMovedProvider) inspectionSnapshot() []graph.Node {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]graph.Node(nil), provider.inspected...)
}

type failMovedWriteBackend struct {
	*memoryBackend
	mu       sync.Mutex
	failHost string
	failures int
}

func (backend *failMovedWriteBackend) Write(ctx context.Context, host ir.HostSpec, st corestate.State) (corestate.State, error) {
	backend.mu.Lock()
	if host.Name == backend.failHost && backend.failures > 0 {
		backend.failures--
		backend.mu.Unlock()
		return corestate.State{}, errMovedStateWrite
	}
	backend.mu.Unlock()
	return backend.memoryBackend.Write(ctx, host, st)
}

func TestMovedPlanCheckAndMoveOnlyApplyAreReadOnlyThenAtomic(t *testing.T) {
	currentHost, currentGraph, initial := movedEngineFixture(t, "node", "1")
	program := &ir.Program{Hosts: []ir.HostSpec{currentHost}}
	build := sharedMovedBuild(program, currentGraph)
	backend := newMemoryBackend()
	backend.states[currentHost.Name] = initial
	provider := newConvergedMovedProvider()
	actionEngine := Engine{Backend: backend, Provider: provider}

	preview, err := actionEngine.Plan(context.Background(), build)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.HasChanges() || len(preview.Hosts) != 1 || len(preview.Hosts[0].Moves) != len(initial.Resources) || countResourceChanges(preview.Hosts[0]) != 0 {
		t.Fatalf("move-only preview = %#v", preview)
	}
	if _, writes := backend.snapshot(currentHost.Name); writes != 0 {
		t.Fatalf("plan writes = %d, want 0", writes)
	}
	checked, err := actionEngine.Check(context.Background(), build)
	if err == nil || !strings.Contains(err.Error(), "drift or unapplied changes") || len(checked.Hosts[0].Moves) != len(initial.Resources) {
		t.Fatalf("check plan/error = %#v / %v", checked, err)
	}
	if _, writes := backend.snapshot(currentHost.Name); writes != 0 {
		t.Fatalf("check writes = %d, want 0", writes)
	}

	var locked HostPlan
	actual, err := actionEngine.Apply(context.Background(), build, ApplyOptions{
		ReviewPreview: func(_ context.Context, plan Plan) error {
			if !plan.HasChanges() || len(plan.Hosts[0].Moves) == 0 {
				t.Fatalf("preview review did not contain moves: %#v", plan)
			}
			return nil
		},
		ReviewLocked: func(_ context.Context, _, plan Plan, changed bool) error {
			if changed {
				t.Fatal("unchanged locked move plan was reported as changed")
			}
			if !backend.isLocked(currentHost.Name) {
				t.Fatal("locked review ran without the host lease")
			}
			if _, writes := backend.snapshot(currentHost.Name); writes != 0 {
				t.Fatalf("state was written before locked review: %d", writes)
			}
			locked = plan.Hosts[0]
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(actual.Hosts) != 1 || len(actual.Hosts[0].Moves) != len(initial.Resources) {
		t.Fatalf("applied plan = %#v", actual)
	}
	applied, deleted := provider.mutationCounts()
	state, writes := backend.snapshot(currentHost.Name)
	if writes != 1 || applied != 0 || deleted != 0 || state.Serial != initial.Serial+1 {
		t.Fatalf("move-only result: writes=%d applied=%d deleted=%d serial=%d", writes, applied, deleted, state.Serial)
	}
	assertMovedState(t, state, currentHost.Name, len(initial.Resources))
	assertRetainedPhysicalGraph(t, locked, currentHost.Name)
	assertProviderInspectedRetainedPhysical(t, provider, currentHost.Name, "old")
	if program.Hosts[0].Components[0].PhysicalName != "" {
		t.Fatalf("shared program was mutated: %#v", program.Hosts[0].Components[0])
	}
	assertUnboundSharedGraph(t, currentGraph, currentHost.Name)
}

func TestMovedCancellationAfterLockedReviewPreventsPrewrite(t *testing.T) {
	host, resourceGraph, initial := movedEngineFixture(t, "node", "1")
	backend := newMemoryBackend()
	backend.states[host.Name] = initial
	provider := newConvergedMovedProvider()
	actionEngine := Engine{Backend: backend, Provider: provider}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := actionEngine.Apply(ctx, sharedMovedBuild(&ir.Program{Hosts: []ir.HostSpec{host}}, resourceGraph), ApplyOptions{
		ReviewPreview: func(context.Context, Plan) error { return nil },
		ReviewLocked: func(context.Context, Plan, Plan, bool) error {
			cancel()
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Apply() error = %v, want context cancellation", err)
	}
	applied, deleted := provider.mutationCounts()
	state, writes := backend.snapshot(host.Name)
	if writes != 0 || applied != 0 || deleted != 0 || state.Serial != initial.Serial || !hasComponentPrefix(state, componentRoot(host.Name, "old")) {
		t.Fatalf("cancelled apply mutated state/provider: writes=%d applied=%d deleted=%d state=%#v", writes, applied, deleted, state)
	}
}

func TestMovedSetParticipatesInLockedFingerprintAndApproval(t *testing.T) {
	host, resourceGraph, initial := movedEngineFixture(t, "node", "1")
	program := &ir.Program{Hosts: []ir.HostSpec{host}}
	backend := newMemoryBackend()
	backend.states[host.Name] = initial
	provider := newConvergedMovedProvider()
	actionEngine := Engine{Backend: backend, Provider: provider}
	migrated := migratedEngineState(t, host, initial)
	backend.lockHook = func() {
		backend.mu.Lock()
		backend.states[host.Name] = migrated
		backend.mu.Unlock()
	}
	rejected := errors.New("review changed move set")
	_, err := actionEngine.Apply(context.Background(), sharedMovedBuild(program, resourceGraph), ApplyOptions{
		ReviewPreview: func(_ context.Context, plan Plan) error {
			if len(plan.Hosts[0].Moves) == 0 {
				t.Fatal("preview has no pending moves")
			}
			return nil
		},
		ReviewLocked: func(_ context.Context, preview, locked Plan, changed bool) error {
			if !changed || len(preview.Hosts[0].Moves) == 0 || len(locked.Hosts[0].Moves) != 0 {
				t.Fatalf("locked move divergence: changed=%v preview=%#v locked=%#v", changed, preview, locked)
			}
			return rejected
		},
	})
	if !errors.Is(err, rejected) {
		t.Fatalf("Apply() error = %v, want locked review rejection", err)
	}
	if _, writes := backend.snapshot(host.Name); writes != 0 {
		t.Fatalf("review rejection wrote state %d time(s)", writes)
	}
}

func TestMovedPrewriteFailureCallsNoProviderAndPreservesState(t *testing.T) {
	host, resourceGraph, initial := movedEngineFixture(t, "node", "2")
	backend := &failMovedWriteBackend{memoryBackend: newMemoryBackend(), failHost: host.Name, failures: 1}
	backend.states[host.Name] = initial
	provider := newConvergedMovedProvider()
	forceMovedDefinitionUpdate(t, provider, host.Name, initial)
	actionEngine := Engine{Backend: backend, Provider: provider}

	_, err := actionEngine.Apply(context.Background(), sharedMovedBuild(&ir.Program{Hosts: []ir.HostSpec{host}}, resourceGraph), approvedMovedApply())
	if !errors.Is(err, errMovedStateWrite) {
		t.Fatalf("Apply() error = %v, want moved-state write failure", err)
	}
	applied, deleted := provider.mutationCounts()
	state, writes := backend.snapshot(host.Name)
	if applied != 0 || deleted != 0 || writes != 0 || state.Serial != initial.Serial || !hasComponentPrefix(state, componentRoot(host.Name, "old")) {
		t.Fatalf("failed prewrite mutated execution: applied=%d deleted=%d writes=%d state=%#v", applied, deleted, writes, state)
	}
}

func TestMovedStateRemainsCommittedAfterProviderFailureAndRetry(t *testing.T) {
	host, resourceGraph, initial := movedEngineFixture(t, "node", "2")
	backend := newMemoryBackend()
	backend.states[host.Name] = initial
	provider := newConvergedMovedProvider()
	forceMovedDefinitionUpdate(t, provider, host.Name, initial)
	provider.applyErr = errors.New("injected provider failure")
	actionEngine := Engine{Backend: backend, Provider: provider}
	build := sharedMovedBuild(&ir.Program{Hosts: []ir.HostSpec{host}}, resourceGraph)

	var firstLocked HostPlan
	options := approvedMovedApply()
	options.ReviewLocked = func(_ context.Context, _, locked Plan, _ bool) error {
		firstLocked = locked.Hosts[0]
		return nil
	}
	_, err := actionEngine.Apply(context.Background(), build, options)
	if err == nil || !strings.Contains(err.Error(), "injected provider failure") {
		t.Fatalf("Apply() error = %v, want provider failure", err)
	}
	if len(firstLocked.Moves) == 0 || countResourceChanges(firstLocked) == 0 {
		t.Fatalf("mixed locked plan = %#v", firstLocked)
	}
	afterFailure, writes := backend.snapshot(host.Name)
	if writes != 1 || afterFailure.Serial != initial.Serial+1 || hasComponentPrefix(afterFailure, componentRoot(host.Name, "old")) {
		t.Fatalf("move was not committed before provider failure: writes=%d state=%#v", writes, afterFailure)
	}

	provider.mu.Lock()
	provider.applyErr = nil
	provider.mu.Unlock()
	var retryLocked HostPlan
	options.ReviewLocked = func(_ context.Context, _, locked Plan, _ bool) error {
		retryLocked = locked.Hosts[0]
		return nil
	}
	if _, err := actionEngine.Apply(context.Background(), build, options); err != nil {
		t.Fatal(err)
	}
	final, writes := backend.snapshot(host.Name)
	if len(retryLocked.Moves) != 0 || writes != 2 || final.Serial != initial.Serial+2 {
		t.Fatalf("retry result: plan=%#v writes=%d state=%#v", retryLocked, writes, final)
	}
}

func TestMovedMultiHostPartialSuccessRetryIsDeterministic(t *testing.T) {
	first, _, firstState := movedEngineFixture(t, "a", "1")
	second, _, secondState := movedEngineFixture(t, "b", "1")
	program := &ir.Program{Hosts: []ir.HostSpec{first, second}}
	resourceGraph, err := graph.Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	backend := &failMovedWriteBackend{memoryBackend: newMemoryBackend(), failHost: second.Name, failures: 1}
	backend.states[first.Name] = firstState
	backend.states[second.Name] = secondState
	provider := newConvergedMovedProvider()
	actionEngine := Engine{Backend: backend, Provider: provider, Parallel: 1}
	options := approvedMovedApply()
	options.Parallel = 1

	partial, err := actionEngine.Apply(context.Background(), sharedMovedBuild(program, resourceGraph), options)
	if !errors.Is(err, errMovedStateWrite) || len(partial.Hosts) != 1 || partial.Hosts[0].Host.Name != first.Name {
		t.Fatalf("partial apply = %#v, error = %v", partial, err)
	}
	firstAfter, _ := backend.snapshot(first.Name)
	secondAfter, _ := backend.snapshot(second.Name)
	if firstAfter.Serial != firstState.Serial+1 || secondAfter.Serial != secondState.Serial || hasComponentPrefix(firstAfter, componentRoot(first.Name, "old")) || !hasComponentPrefix(secondAfter, componentRoot(second.Name, "old")) {
		t.Fatalf("partial host states: first=%#v second=%#v", firstAfter, secondAfter)
	}

	var retry Plan
	options.ReviewLocked = func(_ context.Context, _, locked Plan, _ bool) error {
		retry.Hosts = append(retry.Hosts, locked.Hosts...)
		return nil
	}
	if _, err := actionEngine.Apply(context.Background(), sharedMovedBuild(program, resourceGraph), options); err != nil {
		t.Fatal(err)
	}
	if len(retry.Hosts) != 2 || len(retry.Hosts[0].Moves) != 0 || len(retry.Hosts[1].Moves) == 0 || retry.Hosts[0].Host.Name != "a" || retry.Hosts[1].Host.Name != "b" {
		t.Fatalf("retry locked plans = %#v", retry.Hosts)
	}
	firstFinal, _ := backend.snapshot(first.Name)
	secondFinal, _ := backend.snapshot(second.Name)
	if firstFinal.Serial != firstState.Serial+2 || secondFinal.Serial != secondState.Serial+1 || hasComponentPrefix(secondFinal, componentRoot(second.Name, "old")) {
		t.Fatalf("retry host states: first=%#v second=%#v", firstFinal, secondFinal)
	}
}

func TestMovedParallelHostsKeepPhysicalBindingsIsolated(t *testing.T) {
	first, _, firstState := movedEngineNamedFixture(t, "a", "legacy_a", "current", "1")
	second, _, secondState := movedEngineNamedFixture(t, "b", "legacy_b", "current", "1")
	program := &ir.Program{Hosts: []ir.HostSpec{first, second}}
	resourceGraph, err := graph.Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	backend := newMemoryBackend()
	backend.states[first.Name] = firstState
	backend.states[second.Name] = secondState
	provider := newConvergedMovedProvider()
	actionEngine := Engine{Backend: backend, Provider: provider, Parallel: 2}
	build := sharedMovedBuild(program, resourceGraph)

	preview, err := actionEngine.Plan(context.Background(), build)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Hosts) != 2 || len(preview.Hosts[0].Moves) == 0 || len(preview.Hosts[1].Moves) == 0 || countResourceChanges(preview.Hosts[0]) != 0 || countResourceChanges(preview.Hosts[1]) != 0 {
		t.Fatalf("parallel preview = %#v", preview)
	}
	if _, err := actionEngine.Apply(context.Background(), build, ApplyOptions{
		Parallel:      2,
		ReviewPreview: func(context.Context, Plan) error { return nil },
		ReviewLocked:  func(context.Context, Plan, Plan, bool) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	firstFinal, _ := backend.snapshot(first.Name)
	secondFinal, _ := backend.snapshot(second.Name)
	if firstFinal.ComponentIdentities[componentRoot(first.Name, "current")].PhysicalName != "legacy_a" || secondFinal.ComponentIdentities[componentRoot(second.Name, "current")].PhysicalName != "legacy_b" {
		t.Fatalf("parallel physical identities: first=%#v second=%#v", firstFinal.ComponentIdentities, secondFinal.ComponentIdentities)
	}
	assertProviderInspectedRetainedPhysical(t, provider, first.Name, "legacy_a")
	assertProviderInspectedRetainedPhysical(t, provider, second.Name, "legacy_b")
	for _, host := range program.Hosts {
		if host.Components[0].PhysicalName != "" {
			t.Fatalf("parallel planning mutated shared host %q: %#v", host.Name, host.Components[0])
		}
	}
}

func approvedMovedApply() ApplyOptions {
	return ApplyOptions{
		ReviewPreview: func(context.Context, Plan) error { return nil },
		ReviewLocked:  func(context.Context, Plan, Plan, bool) error { return nil },
	}
}

func movedEngineFixture(t *testing.T, hostName, version string) (ir.HostSpec, *graph.ResourceGraph, corestate.State) {
	t.Helper()
	return movedEngineNamedFixture(t, hostName, "old", "current", version)
}

func movedEngineNamedFixture(t *testing.T, hostName, legacyName, targetName, version string) (ir.HostSpec, *graph.ResourceGraph, corestate.State) {
	t.Helper()
	legacy := movedEngineHost(hostName, legacyName, "1")
	legacy.Moves = nil
	legacyGraph, err := graph.Compile(&ir.Program{Hosts: []ir.HostSpec{legacy}})
	if err != nil {
		t.Fatal(err)
	}
	initial := corestate.Empty(hostName)
	initial.Serial = 7
	order := 0
	for _, node := range legacyGraph.Nodes {
		if !node.Managed {
			continue
		}
		order++
		initial.Resources[node.Address] = corestate.Resource{
			Host: hostName, Kind: node.Kind, Ownership: "managed", Order: order,
			DesiredDigest: corestate.Digest(node.Desired), DeleteBehavior: stringValueForTest(node.Desired, "delete_behavior"),
		}
	}
	current := movedEngineHost(hostName, targetName, version)
	current.Moves = []ir.MovedSpec{{
		From: componentRoot(hostName, legacyName), To: componentRoot(hostName, targetName),
		Source:     ir.SourceRef{File: "main.apf.hcl", Line: 1, Path: "moved"},
		FromSource: ir.SourceRef{File: "main.apf.hcl", Line: 2, Path: "moved.from"},
		ToSource:   ir.SourceRef{File: "main.apf.hcl", Line: 3, Path: "moved.to"},
	}}
	currentGraph, err := graph.Compile(&ir.Program{Hosts: []ir.HostSpec{current}})
	if err != nil {
		t.Fatal(err)
	}
	return current, currentGraph, initial
}

func movedEngineHost(hostName, componentName, version string) ir.HostSpec {
	host := testHost()
	host.Name = hostName
	host.SSH.Host = hostName
	declarationID := fmt.Sprintf(`component.%s.script["refresh"]`, componentName)
	component := ir.ComponentInstanceSpec{
		Name: componentName, Template: "tool", ArtifactType: "binary", Version: version,
		SelectedSource: &ir.ComponentArtifactSourceSpec{
			Architecture: "amd64", URL: "https://example.invalid/tool", SHA256: strings.Repeat("a", 64),
			Source: ir.SourceRef{File: "main.apf.hcl", Line: 10, Path: "component.tool.source"},
		},
		Install: &ir.ComponentArtifactInstallSpec{
			Path: "/usr/local/bin/tool", Owner: "root", Group: "root", Mode: "0755",
			OnChange: &ir.ScriptReferenceSpec{Name: "refresh", Scope: "component", DeclarationID: declarationID},
			Source:   ir.SourceRef{File: "main.apf.hcl", Line: 11, Path: "component.tool.install"},
		},
		Scripts: map[string]ir.ScriptSpec{"refresh": {
			Name: "refresh", DeclarationID: declarationID, Commands: [][]string{{"true"}}, ScriptDigest: "script-v1",
			Source: ir.SourceRef{File: "main.apf.hcl", Line: 12, Path: "component.tool.script.refresh"},
		}},
		Services: []ir.ServiceSpec{{
			Name: "tool", Enabled: true, Runlevel: "default", State: "running",
			Source: ir.SourceRef{File: "main.apf.hcl", Line: 13, Path: "component.tool.service.tool"},
		}},
		Source: ir.SourceRef{File: "main.apf.hcl", Line: 9, Path: "component.tool"},
	}
	host.Components = []ir.ComponentInstanceSpec{component}
	return host
}

func sharedMovedBuild(program *ir.Program, resourceGraph *graph.ResourceGraph) BuildFunc {
	return func(context.Context) (*ir.Program, *graph.ResourceGraph, error) {
		return program, resourceGraph, nil
	}
}

func migratedEngineState(t *testing.T, host ir.HostSpec, initial corestate.State) corestate.State {
	t.Helper()
	bound := host
	bound.Components = append([]ir.ComponentInstanceSpec(nil), host.Components...)
	bound.Components[0] = bound.Components[0].WithPhysicalName("old")
	boundGraph, err := graph.Compile(&ir.Program{Hosts: []ir.HostSpec{bound}})
	if err != nil {
		t.Fatal(err)
	}
	state := corestate.Empty(host.Name)
	state.Serial = initial.Serial
	state.ComponentIdentities[componentRoot(host.Name, "current")] = corestate.ComponentIdentity{PhysicalName: "old"}
	order := 0
	for _, node := range boundGraph.Nodes {
		if !node.Managed {
			continue
		}
		order++
		state.Resources[node.Address] = corestate.Resource{Host: host.Name, Kind: node.Kind, Ownership: "managed", Order: order, DesiredDigest: corestate.Digest(node.Desired)}
	}
	return state
}

func forceMovedDefinitionUpdate(t *testing.T, provider *convergedMovedProvider, hostName string, initial corestate.State) {
	t.Helper()
	legacyAddress := componentRoot(hostName, "old") + `.artifact.install["/usr/local/bin/tool"]`
	targetAddress := componentRoot(hostName, "current") + `.artifact.install["/usr/local/bin/tool"]`
	legacy, exists := initial.Resources[legacyAddress]
	if !exists {
		t.Fatalf("legacy install state missing: %#v", initial.Resources)
	}
	provider.observed[targetAddress] = ObservedResource{Exists: true, Digest: legacy.DesiredDigest}
}

func assertMovedState(t *testing.T, state corestate.State, hostName string, wantResources int) {
	t.Helper()
	if len(state.Resources) != wantResources || hasComponentPrefix(state, componentRoot(hostName, "old")) || !hasComponentPrefix(state, componentRoot(hostName, "current")) {
		t.Fatalf("moved state resources = %#v", state.Resources)
	}
	identity, exists := state.ComponentIdentities[componentRoot(hostName, "current")]
	if !exists || identity.PhysicalName != "old" {
		t.Fatalf("moved physical identity = %#v", state.ComponentIdentities)
	}
}

func assertRetainedPhysicalGraph(t *testing.T, plan HostPlan, hostName string) {
	t.Helper()
	var sourcePath, declarationID, markerPath string
	for _, step := range plan.Steps {
		switch step.Node.Kind {
		case "component_artifact_source":
			sourcePath, _ = step.Node.Desired["path"].(string)
		case "component_script":
			declarationID, _ = step.Node.Desired["declaration_id"].(string)
			markerPath, _ = step.Node.Desired["marker_path"].(string)
		}
	}
	legacy := movedEngineHost(hostName, "old", "1")
	legacyGraph, err := graph.Compile(&ir.Program{Hosts: []ir.HostSpec{legacy}})
	if err != nil {
		t.Fatal(err)
	}
	var legacyMarker string
	for _, node := range legacyGraph.Nodes {
		if node.Kind == "component_script" {
			legacyMarker, _ = node.Desired["marker_path"].(string)
		}
	}
	if !strings.Contains(sourcePath, "/components/old/") || declarationID != `component.current.script["refresh"]` || markerPath == "" || markerPath != legacyMarker {
		t.Fatalf("bound graph identity: source=%q declaration=%q marker=%q legacy_marker=%q", sourcePath, declarationID, markerPath, legacyMarker)
	}
}

func assertProviderInspectedRetainedPhysical(t *testing.T, provider *convergedMovedProvider, hostName, physicalName string) {
	t.Helper()
	wantPath := "/components/" + physicalName + "/"
	legacyGraph, err := graph.Compile(&ir.Program{Hosts: []ir.HostSpec{movedEngineHost(hostName, physicalName, "1")}})
	if err != nil {
		t.Fatal(err)
	}
	wantMarker := ""
	for _, node := range legacyGraph.Nodes {
		if node.Kind == "component_script" {
			wantMarker, _ = node.Desired["marker_path"].(string)
		}
	}
	foundArtifact := false
	foundScript := false
	for _, node := range provider.inspectionSnapshot() {
		if node.Host != hostName {
			continue
		}
		switch node.Kind {
		case "component_artifact_source":
			path, _ := node.Desired["path"].(string)
			if !strings.Contains(path, wantPath) {
				t.Fatalf("provider inspected host %q with crossed physical path %q, want %q", hostName, path, wantPath)
			}
			foundArtifact = true
		case "component_script":
			marker, _ := node.Desired["marker_path"].(string)
			declaration, _ := node.Desired["declaration_id"].(string)
			if marker != wantMarker || declaration != `component.current.script["refresh"]` {
				t.Fatalf("provider inspected host %q script identity marker=%q declaration=%q, want marker=%q", hostName, marker, declaration, wantMarker)
			}
			foundScript = true
		}
	}
	if !foundArtifact || !foundScript {
		t.Fatalf("provider inspections for host %q: artifact=%v script=%v", hostName, foundArtifact, foundScript)
	}
}

func assertUnboundSharedGraph(t *testing.T, resourceGraph *graph.ResourceGraph, hostName string) {
	t.Helper()
	for _, node := range resourceGraph.Nodes {
		if node.Host == hostName && node.Kind == "component_artifact_source" {
			path, _ := node.Desired["path"].(string)
			if !strings.Contains(path, "/components/current/") {
				t.Fatalf("shared graph was mutated: %q", path)
			}
			return
		}
	}
	t.Fatal("shared graph has no component artifact source")
}

func countResourceChanges(plan HostPlan) int {
	count := 0
	for _, step := range plan.Steps {
		if step.Action != ActionNoOp {
			count++
		}
	}
	return count
}

func hasComponentPrefix(state corestate.State, prefix string) bool {
	for address := range state.Resources {
		if address == prefix || strings.HasPrefix(address, prefix+".") {
			return true
		}
	}
	return false
}

func stringValueForTest(values map[string]any, name string) string {
	value, _ := values[name].(string)
	return value
}

func TestMoveLineageCollapsesChainsToActualStateSource(t *testing.T) {
	moves := []corestate.RealizedMove{
		{Host: "node", From: "host.node.component.old.file", To: "host.node.component.middle.file"},
		{Host: "node", From: "host.node.component.middle.file", To: "host.node.component.current.file"},
	}
	want := map[string]string{"host.node.component.current.file": "host.node.component.old.file"}
	if got := moveLineage(moves); !reflect.DeepEqual(got, want) {
		t.Fatalf("move lineage = %#v, want %#v", got, want)
	}
}

func TestMovedPlanFingerprintIncludesMoveSet(t *testing.T) {
	host := testHost()
	without := planFingerprint(HostPlan{Host: host})
	with := planFingerprint(HostPlan{Host: host, Moves: []corestate.RealizedMove{{Host: host.Name, From: "old", To: "current"}}})
	if without == with {
		t.Fatal("move set did not change host plan fingerprint")
	}
	reversed := planFingerprint(HostPlan{Host: host, Moves: []corestate.RealizedMove{
		{Host: host.Name, From: "z", To: "current"},
		{Host: host.Name, From: "a", To: "current"},
	}})
	sorted := planFingerprint(HostPlan{Host: host, Moves: []corestate.RealizedMove{
		{Host: host.Name, From: "a", To: "current"},
		{Host: host.Name, From: "z", To: "current"},
	}})
	if reversed != sorted {
		t.Fatal("move fingerprint depends on input ordering")
	}
}

func TestBindHostComponentIdentitiesDoesNotMutateSharedProgram(t *testing.T) {
	host := movedEngineHost("node", "current", "1")
	original := host.Components[0]
	bound, err := bindHostComponentIdentities(host, map[string]corestate.ComponentIdentity{
		componentRoot(host.Name, "current"): {PhysicalName: "old"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if host.Components[0].PhysicalName != "" || !reflect.DeepEqual(host.Components[0], original) || bound.Components[0].PhysicalComponentName() != "old" {
		t.Fatalf("host-local binding mutated input: input=%#v bound=%#v", host.Components[0], bound.Components[0])
	}
}

func TestHostWithLogicalComponentNamesRewritesExplicitDependencies(t *testing.T) {
	host := movedEngineHost("node", "current", "1")
	component := &host.Components[0]
	component.Packages = []ir.PackageSpec{{Name: "tool", Ensure: "present", Source: ir.SourceRef{File: "main.apf.hcl", Line: 20, Path: "component.tool.package.tool"}}}
	component.Files = []ir.ManagedFileSpec{{Path: "/etc/tool.conf", Ensure: "present", Source: ir.SourceRef{File: "main.apf.hcl", Line: 21, Path: "component.tool.file.config"}}}
	currentPrefix := componentRoot(host.Name, "current")
	component.ExplicitDependencies = []ir.ResourceDependencySpec{
		{
			From: currentPrefix + `.files.file["/etc/tool.conf"]`, DependsOn: currentPrefix + `.packages.package["tool"]`,
			Source: ir.SourceRef{File: "main.apf.hcl", Line: 22, Path: "component.tool.file.config.depends_on[0]"},
		},
		{
			From: currentPrefix + `.services.service["tool"]`, DependsOn: currentPrefix + `.files.file["/etc/tool.conf"]`,
			Source: ir.SourceRef{File: "main.apf.hcl", Line: 23, Path: "component.tool.service.tool.depends_on[0]"},
		},
	}
	original := append([]ir.ResourceDependencySpec(nil), component.ExplicitDependencies...)

	legacy := hostWithLogicalComponentNames(host, map[string]string{"current": "old"})
	oldPrefix := componentRoot(host.Name, "old")
	want := []ir.ResourceDependencySpec{
		{From: oldPrefix + `.files.file["/etc/tool.conf"]`, DependsOn: oldPrefix + `.packages.package["tool"]`, Source: original[0].Source},
		{From: oldPrefix + `.services.service["tool"]`, DependsOn: oldPrefix + `.files.file["/etc/tool.conf"]`, Source: original[1].Source},
	}
	if !reflect.DeepEqual(legacy.Components[0].ExplicitDependencies, want) {
		t.Fatalf("legacy explicit dependencies = %#v, want %#v", legacy.Components[0].ExplicitDependencies, want)
	}
	if !reflect.DeepEqual(host.Components[0].ExplicitDependencies, original) {
		t.Fatalf("legacy rewrite mutated input dependencies: %#v", host.Components[0].ExplicitDependencies)
	}
	if _, err := graph.Compile(&ir.Program{Hosts: []ir.HostSpec{legacy}}); err != nil {
		t.Fatalf("compile legacy component dependencies: %v", err)
	}
}

func TestMovedApplyPrewriteUsesReturnedSerial(t *testing.T) {
	host, resourceGraph, initial := movedEngineFixture(t, "node", "2")
	backend := newMemoryBackend()
	backend.states[host.Name] = initial
	provider := newConvergedMovedProvider()
	forceMovedDefinitionUpdate(t, provider, host.Name, initial)
	actionEngine := Engine{Backend: backend, Provider: provider}
	if _, err := actionEngine.Apply(context.Background(), sharedMovedBuild(&ir.Program{Hosts: []ir.HostSpec{host}}, resourceGraph), ApplyOptions{
		LockTimeout:   time.Second,
		ReviewPreview: func(context.Context, Plan) error { return nil },
		ReviewLocked:  func(context.Context, Plan, Plan, bool) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	state, writes := backend.snapshot(host.Name)
	if writes != 2 || state.Serial != initial.Serial+2 {
		t.Fatalf("mixed move/apply serial = %d after %d writes, want %d after 2", state.Serial, writes, initial.Serial+2)
	}
}

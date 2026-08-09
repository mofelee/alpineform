package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

type memoryBackend struct {
	mu       sync.Mutex
	states   map[string]corestate.State
	locked   map[string]bool
	writes   int
	lockHook func()
}

type blockingBackend struct {
	*memoryBackend
	mu      sync.Mutex
	active  int
	maximum int
	started chan string
	release chan struct{}
}

func (backend *blockingBackend) WithLease(ctx context.Context, host ir.HostSpec, timeout time.Duration, work func(context.Context) error) error {
	return backend.memoryBackend.WithLease(ctx, host, timeout, func(leaseContext context.Context) error {
		backend.mu.Lock()
		backend.active++
		if backend.active > backend.maximum {
			backend.maximum = backend.active
		}
		backend.mu.Unlock()
		defer func() {
			backend.mu.Lock()
			backend.active--
			backend.mu.Unlock()
		}()
		backend.started <- host.Name
		select {
		case <-backend.release:
			return work(leaseContext)
		case <-leaseContext.Done():
			return leaseContext.Err()
		}
	})
}

func newMemoryBackend() *memoryBackend {
	return &memoryBackend{states: map[string]corestate.State{}, locked: map[string]bool{}}
}

func (backend *memoryBackend) Read(_ context.Context, host ir.HostSpec) (corestate.State, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	state, exists := backend.states[host.Name]
	if !exists {
		return corestate.Empty(host.Name), nil
	}
	return cloneState(state), nil
}

func (backend *memoryBackend) Write(_ context.Context, host ir.HostSpec, state corestate.State) (corestate.State, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	prepared, err := corestate.PrepareWrite(state, host.Name, time.Unix(100, 0))
	if err != nil {
		return corestate.State{}, err
	}
	backend.states[host.Name] = cloneState(prepared)
	backend.writes++
	return prepared, nil
}

func (backend *memoryBackend) WithLease(ctx context.Context, host ir.HostSpec, _ time.Duration, work func(context.Context) error) error {
	backend.mu.Lock()
	if backend.locked[host.Name] {
		backend.mu.Unlock()
		return fmt.Errorf("already locked")
	}
	backend.locked[host.Name] = true
	hook := backend.lockHook
	backend.mu.Unlock()
	defer func() {
		backend.mu.Lock()
		backend.locked[host.Name] = false
		backend.mu.Unlock()
	}()
	if hook != nil {
		hook()
	}
	return work(ctx)
}

func (backend *memoryBackend) isLocked(host string) bool {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.locked[host]
}

func (backend *memoryBackend) snapshot(host string) (corestate.State, int) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return cloneState(backend.states[host]), backend.writes
}

func cloneState(input corestate.State) corestate.State {
	out := input
	out.ComponentIdentities = make(map[string]corestate.ComponentIdentity, len(input.ComponentIdentities))
	for root, identity := range input.ComponentIdentities {
		out.ComponentIdentities[root] = identity
	}
	out.Resources = make(map[string]corestate.Resource, len(input.Resources))
	for address, resource := range input.Resources {
		out.Resources[address] = resource
	}
	if input.Facts != nil {
		facts := *input.Facts
		out.Facts = &facts
	}
	return out
}

type memoryProvider struct {
	mu       sync.Mutex
	observed map[string]ObservedResource
	applied  []Step
	deleted  []Step
}

type failingProvider struct {
	inspectError error
	applyError   error
	deleteError  error
	observed     ObservedResource
}

type protectedResultProvider struct {
	applied ObservedResource
}

func (provider protectedResultProvider) Inspect(context.Context, graph.Node) (ObservedResource, error) {
	return ObservedResource{}, nil
}

func (provider protectedResultProvider) Apply(context.Context, Step) (ObservedResource, error) {
	return provider.applied, nil
}

func (protectedResultProvider) Delete(context.Context, Step) error {
	return nil
}

func (provider failingProvider) Inspect(context.Context, graph.Node) (ObservedResource, error) {
	return provider.observed, provider.inspectError
}

func (provider failingProvider) Apply(context.Context, Step) (ObservedResource, error) {
	return ObservedResource{}, provider.applyError
}

func (provider failingProvider) Delete(context.Context, Step) error {
	return provider.deleteError
}

func newMemoryProvider() *memoryProvider {
	return &memoryProvider{observed: map[string]ObservedResource{}}
}

func (provider *memoryProvider) Inspect(_ context.Context, node graph.Node) (ObservedResource, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.observed[node.Address], nil
}

func (provider *memoryProvider) Apply(_ context.Context, step Step) (ObservedResource, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.applied = append(provider.applied, step)
	observed := ObservedResource{Exists: true, Values: step.Node.Desired, Digest: corestate.Digest(step.Node.Desired)}
	provider.observed[step.Address] = observed
	return observed, nil
}

func (provider *memoryProvider) Delete(_ context.Context, step Step) error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.deleted = append(provider.deleted, step)
	delete(provider.observed, step.Address)
	return nil
}

func (provider *memoryProvider) set(address string, observed ObservedResource) {
	provider.mu.Lock()
	provider.observed[address] = observed
	provider.mu.Unlock()
}

func (provider *memoryProvider) counts() (int, int) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return len(provider.applied), len(provider.deleted)
}

func testHost() ir.HostSpec {
	facts := &ir.HostFacts{OSID: "alpine", Version: "3.24.1", Branch: "v3.24", Architecture: "amd64", NativeArchitecture: "x86_64", KernelArchitecture: "x86_64", Libc: "musl"}
	return ir.HostSpec{Name: "node", SSH: ir.SSHSpec{Host: "node", User: "root"}, State: ir.StateSpec{Path: "/var/lib/alpineform/state.json", LockPath: "/run/lock/alpineform/lock"}, Facts: facts}
}

func testNode(desired map[string]any) graph.Node {
	return graph.Node{Host: "node", Address: "host.node.test.item", Kind: "test", Managed: true, Summary: "manage test item", Desired: desired, Source: ir.SourceRef{File: "main.apf.hcl", Line: 1, Path: "test.item"}}
}

func staticBuild(host ir.HostSpec, nodes ...graph.Node) BuildFunc {
	return func(context.Context) (*ir.Program, *graph.ResourceGraph, error) {
		return &ir.Program{Hosts: []ir.HostSpec{host}}, &graph.ResourceGraph{Nodes: nodes}, nil
	}
}

func multiHostBuild(hosts ...ir.HostSpec) BuildFunc {
	return func(context.Context) (*ir.Program, *graph.ResourceGraph, error) {
		return &ir.Program{Hosts: hosts}, &graph.ResourceGraph{}, nil
	}
}

func TestPlanNodeActionModel(t *testing.T) {
	desired := map[string]any{"value": "new"}
	node := testNode(desired)
	digest := corestate.Digest(desired)
	old := corestate.Resource{DesiredDigest: corestate.Digest(map[string]any{"value": "old"})}
	matching := corestate.Resource{DesiredDigest: digest}
	tests := []struct {
		name     string
		node     graph.Node
		prior    corestate.Resource
		hasPrior bool
		observed ObservedResource
		want     string
	}{
		{name: "create new", node: node, want: ActionCreate},
		{name: "adopt existing", node: node, observed: ObservedResource{Exists: true, Digest: digest}, want: ActionAdopt},
		{name: "update untracked existing", node: node, observed: ObservedResource{Exists: true, Digest: "different"}, want: ActionUpdate},
		{name: "write-only untracked existing", node: testNode(map[string]any{"content_write_only": true, "content_version": "v1"}), observed: ObservedResource{Exists: true, Digest: corestate.Digest(map[string]any{"content_write_only": true, "content_version": "v1"})}, want: ActionUpdate},
		{name: "repair missing", node: node, prior: matching, hasPrior: true, want: ActionCreate},
		{name: "adopt desired already converged", node: node, prior: old, hasPrior: true, observed: ObservedResource{Exists: true, Digest: digest}, want: ActionAdopt},
		{name: "repair drift", node: node, prior: matching, hasPrior: true, observed: ObservedResource{Exists: true, Digest: "different"}, want: ActionUpdate},
		{name: "no-op", node: node, prior: matching, hasPrior: true, observed: ObservedResource{Exists: true, Digest: digest}, want: ActionNoOp},
		{name: "delete present", node: testNode(map[string]any{"ensure": "absent"}), observed: ObservedResource{Exists: true}, want: ActionDelete},
		{name: "absent no-op", node: testNode(map[string]any{"ensure": "absent"}), want: ActionNoOp},
		{name: "adopt stale absent state", node: testNode(map[string]any{"ensure": "absent"}), prior: matching, hasPrior: true, want: ActionAdopt},
		{name: "rewrite changed write-only version", node: testNode(map[string]any{"content_write_only": true, "content_version": "v2"}), prior: corestate.Resource{DesiredDigest: corestate.Digest(map[string]any{"content_write_only": true, "content_version": "v1"})}, hasPrior: true, observed: ObservedResource{Exists: true, Digest: corestate.Digest(map[string]any{"content_write_only": true, "content_version": "v2"})}, want: ActionUpdate},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := planNode(test.node, test.prior, test.hasPrior, test.observed).Action; got != test.want {
				t.Fatalf("action = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSourceBuildPlanDistinguishesRebuildAndRepair(t *testing.T) {
	address := "host.node.component.tool.build.install[\"/usr/local/bin/tool\"]"
	desired := map[string]any{"build_identity": "new", "path": "/usr/local/bin/tool"}
	node := graph.Node{Host: "node", Address: address, Kind: "component_build_install", Managed: true, Summary: "install source-build output", Desired: desired}
	tests := []struct {
		name           string
		priorDigest    string
		observedDigest string
		want           string
	}{
		{name: "definition drift", priorDigest: corestate.Digest(map[string]any{"build_identity": "old", "path": "/usr/local/bin/tool"}), observedDigest: "drifted", want: "rebuild:"},
		{name: "installed drift", priorDigest: corestate.Digest(desired), observedDigest: "drifted", want: "repair:"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := newMemoryBackend()
			backend.states["node"] = corestate.State{Product: corestate.Product, SchemaVersion: corestate.SchemaVersion, Host: "node", Resources: map[string]corestate.Resource{
				address: {DesiredDigest: test.priorDigest},
			}}
			provider := newMemoryProvider()
			provider.set(address, ObservedResource{Exists: true, Digest: test.observedDigest})
			plan, err := (Engine{Backend: backend, Provider: provider}).Plan(context.Background(), staticBuild(testHost(), node))
			if err != nil {
				t.Fatal(err)
			}
			step := plan.Hosts[0].Steps[0]
			if step.Action != ActionUpdate || !strings.HasPrefix(step.Summary, test.want) {
				t.Fatalf("step = %#v", step)
			}
		})
	}
}

func TestPlanAggregatesChangedTriggersIntoOneDependentStep(t *testing.T) {
	first := graph.Node{Host: "node", Address: "host.node.file.init", Kind: "file", Managed: true, Desired: map[string]any{"value": "init"}}
	second := graph.Node{Host: "node", Address: "host.node.file.conf", Kind: "file", Managed: true, Desired: map[string]any{"value": "conf"}}
	service := graph.Node{
		Host: "node", Address: "host.node.service.worker", Kind: "service", Managed: true,
		Desired:   map[string]any{"state": "running", "operation": "restarted"},
		DependsOn: []string{first.Address, second.Address}, TriggeredBy: []string{first.Address, second.Address},
	}
	backend := newMemoryBackend()
	backend.states["node"] = corestate.State{
		Product: corestate.Product, SchemaVersion: corestate.SchemaVersion, Host: "node",
		Resources: map[string]corestate.Resource{
			first.Address:   {DesiredDigest: corestate.Digest(first.Desired)},
			second.Address:  {DesiredDigest: corestate.Digest(second.Desired)},
			service.Address: {DesiredDigest: corestate.Digest(service.Desired)},
		},
	}
	provider := newMemoryProvider()
	provider.set(first.Address, ObservedResource{Exists: true, Digest: "drifted-init"})
	provider.set(second.Address, ObservedResource{Exists: true, Digest: "drifted-conf"})
	provider.set(service.Address, ObservedResource{Exists: true, Digest: corestate.Digest(service.Desired)})
	plan, err := (Engine{Backend: backend, Provider: provider}).Plan(context.Background(), staticBuild(testHost(), first, second, service))
	if err != nil {
		t.Fatal(err)
	}
	var serviceSteps []Step
	for _, step := range plan.Hosts[0].Steps {
		if step.Address == service.Address {
			serviceSteps = append(serviceSteps, step)
		}
	}
	if len(serviceSteps) != 1 || serviceSteps[0].Action != ActionUpdate || !reflect.DeepEqual(serviceSteps[0].TriggeredBy, []string{first.Address, second.Address}) {
		t.Fatalf("triggered service steps = %#v", serviceSteps)
	}
}

func TestPlanOrphanActionsAndPreventDestroy(t *testing.T) {
	backend := newMemoryBackend()
	backend.states["node"] = corestate.State{Product: corestate.Product, SchemaVersion: corestate.SchemaVersion, Host: "node", Resources: map[string]corestate.Resource{
		"orphan.delete":  {DeleteBehavior: ActionDelete},
		"orphan.destroy": {DeleteBehavior: ActionDestroy},
		"orphan.forget":  {},
	}}
	engine := Engine{Backend: backend, Provider: newMemoryProvider()}
	plan, err := engine.Plan(context.Background(), staticBuild(testHost()))
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]string{}
	for _, step := range plan.Hosts[0].Steps {
		actions[step.Address] = step.Action
	}
	want := map[string]string{"orphan.delete": ActionDelete, "orphan.destroy": ActionDestroy, "orphan.forget": ActionForget}
	if !reflect.DeepEqual(actions, want) {
		t.Fatalf("actions = %#v, want %#v", actions, want)
	}
	state := backend.states["node"]
	state.Resources["orphan.destroy"] = corestate.Resource{DeleteBehavior: ActionDestroy, PreventDestroy: true}
	backend.states["node"] = state
	if _, err := engine.Plan(context.Background(), staticBuild(testHost())); err == nil || !strings.Contains(err.Error(), "prevent_destroy") {
		t.Fatalf("prevent_destroy error = %v", err)
	}
}

func TestPlanOrdersOrphanedResourcesInReverseRecordedDependencyOrder(t *testing.T) {
	backend := newMemoryBackend()
	backend.states["node"] = corestate.State{Product: corestate.Product, SchemaVersion: corestate.SchemaVersion, Host: "node", Resources: map[string]corestate.Resource{
		"host.node.groups.group.app":        {Order: 1, DeleteBehavior: ActionDestroy},
		"host.node.users.user.app":          {Order: 2, DeleteBehavior: ActionDestroy},
		"host.node.directories.directory.a": {Order: 3, DeleteBehavior: ActionDestroy},
	}}
	engine := Engine{Backend: backend, Provider: newMemoryProvider()}
	plan, err := engine.Plan(context.Background(), staticBuild(testHost()))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(plan.Hosts[0].Steps))
	for _, step := range plan.Hosts[0].Steps {
		got = append(got, step.Address)
	}
	want := []string{
		"host.node.directories.directory.a",
		"host.node.users.user.app",
		"host.node.groups.group.app",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("orphan order = %#v, want %#v", got, want)
	}
}

func TestCheckDistinguishesNoOpAndDrift(t *testing.T) {
	node := testNode(map[string]any{"value": "expected"})
	digest := corestate.Digest(node.Desired)
	backend := newMemoryBackend()
	backend.states["node"] = corestate.State{Product: corestate.Product, SchemaVersion: corestate.SchemaVersion, Host: "node", Resources: map[string]corestate.Resource{node.Address: {DesiredDigest: digest}}}
	provider := newMemoryProvider()
	provider.set(node.Address, ObservedResource{Exists: true, Digest: digest})
	engine := Engine{Backend: backend, Provider: provider}
	if _, err := engine.Check(context.Background(), staticBuild(testHost(), node)); err != nil {
		t.Fatal(err)
	}
	provider.set(node.Address, ObservedResource{Exists: true, Digest: "drift"})
	if _, err := engine.Check(context.Background(), staticBuild(testHost(), node)); err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("drift Check() error = %v", err)
	}
}

func TestApplyRejectsChangedLockedPlanBeforeWrites(t *testing.T) {
	node := testNode(map[string]any{"value": "expected"})
	digest := corestate.Digest(node.Desired)
	backend := newMemoryBackend()
	provider := newMemoryProvider()
	backend.lockHook = func() {
		provider.set(node.Address, ObservedResource{Exists: true, Digest: digest})
	}
	engine := Engine{Backend: backend, Provider: provider}
	rejected := errors.New("locked plan rejected")
	previewCalls := 0
	lockedCalls := 0
	_, err := engine.Apply(context.Background(), staticBuild(testHost(), node), ApplyOptions{
		ReviewPreview: func(_ context.Context, plan Plan) error {
			previewCalls++
			if backend.isLocked("node") || plan.Hosts[0].Steps[0].Action != ActionCreate {
				t.Fatalf("preview state = locked %v, plan %#v", backend.isLocked("node"), plan)
			}
			return nil
		},
		ReviewLocked: func(_ context.Context, preview, locked Plan, changed bool) error {
			lockedCalls++
			_, writes := backend.snapshot("node")
			applied, deleted := provider.counts()
			if !backend.isLocked("node") || writes != 0 || applied != 0 || deleted != 0 || !changed {
				t.Fatalf("locked review invariants: locked=%v writes=%d applied=%d deleted=%d changed=%v", backend.isLocked("node"), writes, applied, deleted, changed)
			}
			if preview.Hosts[0].Steps[0].Action != ActionCreate || locked.Hosts[0].Steps[0].Action != ActionAdopt {
				t.Fatalf("preview/locked = %#v / %#v", preview, locked)
			}
			return rejected
		},
	})
	if !errors.Is(err, rejected) || previewCalls != 1 || lockedCalls != 1 {
		t.Fatalf("Apply() error = %v, preview=%d locked=%d", err, previewCalls, lockedCalls)
	}
	_, writes := backend.snapshot("node")
	applied, deleted := provider.counts()
	if writes != 0 || applied != 0 || deleted != 0 || backend.isLocked("node") {
		t.Fatalf("mutations after rejection: writes=%d applied=%d deleted=%d locked=%v", writes, applied, deleted, backend.isLocked("node"))
	}
}

func TestApplyExecutesOnlyReviewedLockedPlan(t *testing.T) {
	node := testNode(map[string]any{"value": "expected"})
	backend := newMemoryBackend()
	provider := newMemoryProvider()
	engine := Engine{Backend: backend, Provider: provider}
	lockedChanged := true
	actual, err := engine.Apply(context.Background(), staticBuild(testHost(), node), ApplyOptions{
		ReviewPreview: func(context.Context, Plan) error { return nil },
		ReviewLocked: func(_ context.Context, _, locked Plan, changed bool) error {
			lockedChanged = changed
			if locked.Hosts[0].Steps[0].Action != ActionCreate {
				t.Fatalf("locked plan = %#v", locked)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if lockedChanged || len(actual.Hosts) != 1 {
		t.Fatalf("actual = %#v, changed=%v", actual, lockedChanged)
	}
	state, writes := backend.snapshot("node")
	applied, _ := provider.counts()
	if writes != 1 || applied != 1 || state.Resources[node.Address].DesiredDigest != corestate.Digest(node.Desired) {
		t.Fatalf("state=%#v writes=%d applied=%d", state, writes, applied)
	}
	if len(actual.Hosts[0].PriorState.Resources) != 0 {
		t.Fatalf("execution mutated prior state: %#v", actual.Hosts[0].PriorState)
	}
}

func TestApplyRunsHostsWithBoundedParallelismAndStableResults(t *testing.T) {
	first := testHost()
	first.Name = "a"
	first.SSH.Host = "a"
	second := testHost()
	second.Name = "b"
	second.SSH.Host = "b"
	backend := &blockingBackend{
		memoryBackend: newMemoryBackend(),
		started:       make(chan string, 2),
		release:       make(chan struct{}),
	}
	actionEngine := Engine{Backend: backend, Provider: newMemoryProvider(), Parallel: 2}
	result := make(chan struct {
		plan Plan
		err  error
	}, 1)
	go func() {
		plan, err := actionEngine.Apply(context.Background(), multiHostBuild(first, second), ApplyOptions{
			Parallel:      2,
			ReviewPreview: func(context.Context, Plan) error { return nil },
			ReviewLocked:  func(context.Context, Plan, Plan, bool) error { return nil },
		})
		result <- struct {
			plan Plan
			err  error
		}{plan: plan, err: err}
	}()
	for range 2 {
		select {
		case <-backend.started:
		case <-time.After(2 * time.Second):
			t.Fatal("parallel apply did not acquire two host leases")
		}
	}
	close(backend.release)
	got := <-result
	if got.err != nil {
		t.Fatal(got.err)
	}
	if backend.maximum != 2 {
		t.Fatalf("maximum parallel leases = %d, want 2", backend.maximum)
	}
	if names := planHostNames(got.plan); !reflect.DeepEqual(names, []string{"a", "b"}) || got.plan.Hosts[0].Host.Name != "a" {
		t.Fatalf("parallel apply result = %#v", got.plan.Hosts)
	}
}

func TestBoundedWorkCancelsSiblingOnFailure(t *testing.T) {
	want := errors.New("host failed")
	started := make(chan struct{}, 2)
	bothStarted := make(chan struct{})
	var once sync.Once
	err := runBounded(context.Background(), 2, 2, func(ctx context.Context, index int) error {
		started <- struct{}{}
		if len(started) == 2 {
			once.Do(func() { close(bothStarted) })
		}
		select {
		case <-bothStarted:
		case <-time.After(2 * time.Second):
			return errors.New("workers did not start together")
		}
		if index == 0 {
			return want
		}
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, want) {
		t.Fatalf("runBounded() error = %v", err)
	}
}

func TestApplyHiddenDesiredChangeRequiresReviewAgain(t *testing.T) {
	backend := newMemoryBackend()
	provider := newMemoryProvider()
	engine := Engine{Backend: backend, Provider: provider}
	buildCalls := 0
	build := func(context.Context) (*ir.Program, *graph.ResourceGraph, error) {
		buildCalls++
		value := "preview"
		if buildCalls > 1 {
			value = "locked-hidden-change"
		}
		node := testNode(map[string]any{"sensitive": true, "content": value})
		node.Sensitive = true
		return &ir.Program{Hosts: []ir.HostSpec{testHost()}}, &graph.ResourceGraph{Nodes: []graph.Node{node}}, nil
	}
	changedSeen := false
	rejected := errors.New("review again")
	_, err := engine.Apply(context.Background(), build, ApplyOptions{
		ReviewPreview: func(context.Context, Plan) error { return nil },
		ReviewLocked: func(_ context.Context, _, _ Plan, changed bool) error {
			changedSeen = changed
			return rejected
		},
	})
	if !errors.Is(err, rejected) || !changedSeen {
		t.Fatalf("Apply() error = %v, changed=%v", err, changedSeen)
	}
	_, writes := backend.snapshot("node")
	if writes != 0 {
		t.Fatalf("writes after hidden change rejection = %d", writes)
	}
}

func TestApplyHiddenObservedChangeRequiresReviewAgain(t *testing.T) {
	node := testNode(map[string]any{"content": "desired"})
	node.Sensitive = true
	backend := newMemoryBackend()
	provider := newMemoryProvider()
	provider.set(node.Address, ObservedResource{Exists: true, Values: map[string]any{"content": "preview"}})
	backend.lockHook = func() {
		provider.set(node.Address, ObservedResource{Exists: true, Values: map[string]any{"content": "locked"}})
	}
	engine := Engine{Backend: backend, Provider: provider}
	changedSeen := false
	rejected := errors.New("review again")
	_, err := engine.Apply(context.Background(), staticBuild(testHost(), node), ApplyOptions{
		ReviewPreview: func(context.Context, Plan) error { return nil },
		ReviewLocked: func(_ context.Context, _, _ Plan, changed bool) error {
			changedSeen = changed
			return rejected
		},
	})
	if !errors.Is(err, rejected) || !changedSeen {
		t.Fatalf("Apply() error = %v, changed=%v", err, changedSeen)
	}
}

func TestApplyPersistsFactsOnlyAfterNoOpReview(t *testing.T) {
	backend := newMemoryBackend()
	provider := newMemoryProvider()
	engine := Engine{Backend: backend, Provider: provider}
	reviewed := false
	_, err := engine.Apply(context.Background(), staticBuild(testHost()), ApplyOptions{
		ReviewPreview: func(context.Context, Plan) error { return nil },
		ReviewLocked: func(_ context.Context, _, _ Plan, changed bool) error {
			if changed {
				t.Fatal("no-op plan unexpectedly changed")
			}
			_, writes := backend.snapshot("node")
			if writes != 0 {
				t.Fatalf("facts written before review: %d", writes)
			}
			reviewed = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	state, writes := backend.snapshot("node")
	if !reviewed || writes != 1 || state.Facts == nil || state.Facts.Version != "3.24.1" {
		t.Fatalf("reviewed=%v writes=%d state=%#v", reviewed, writes, state)
	}
}

func TestApplyRequiresReviewCallbacksAndStableHostIdentity(t *testing.T) {
	engine := Engine{Backend: newMemoryBackend(), Provider: newMemoryProvider()}
	if _, err := engine.Apply(context.Background(), staticBuild(testHost()), ApplyOptions{}); err == nil || !strings.Contains(err.Error(), "requires preview and locked-plan review") {
		t.Fatalf("missing review error = %v", err)
	}
	buildCalls := 0
	build := func(context.Context) (*ir.Program, *graph.ResourceGraph, error) {
		buildCalls++
		host := testHost()
		if buildCalls > 1 {
			host.SSH.Host = "changed-alias"
		}
		return &ir.Program{Hosts: []ir.HostSpec{host}}, &graph.ResourceGraph{}, nil
	}
	lockedReviewed := false
	_, err := engine.Apply(context.Background(), build, ApplyOptions{
		ReviewPreview: func(context.Context, Plan) error { return nil },
		ReviewLocked: func(context.Context, Plan, Plan, bool) error {
			lockedReviewed = true
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "SSH or state identity changed") || lockedReviewed {
		t.Fatalf("identity change error = %v, lockedReviewed=%v", err, lockedReviewed)
	}
}

func TestProtectedApplyStateDoesNotRetainObservedContent(t *testing.T) {
	secret := "not-a-real-provider-secret"
	secretDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(secret)))
	deletePath := "/var/cache/alpineform/components/retained/protected/amd64/artifact"
	node := testNode(map[string]any{
		"path": deletePath, "verified": true, "url_ephemeral": true, "sha256_ephemeral": true,
		"ensure": "present", "delete_behavior": ActionDelete, "delete": map[string]any{"path": deletePath},
	})
	node.Payload = map[string]any{"url": "https://example.invalid/tool?token=" + secret, "sha256": secretDigest}
	node.Ephemeral = true
	node.DigestSafe = true
	node.ProtectedIntentDigest = "not-a-serialized-protected-intent"
	backend := newMemoryBackend()
	provider := protectedResultProvider{applied: ObservedResource{
		Exists: true, Protected: true,
		Values: map[string]any{"verified": true, "url": secret, "sha256": secretDigest},
	}}
	engine := Engine{Backend: backend, Provider: provider}
	_, err := engine.Apply(context.Background(), staticBuild(testHost(), node), ApplyOptions{
		ReviewPreview: func(context.Context, Plan) error { return nil },
		ReviewLocked:  func(context.Context, Plan, Plan, bool) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	state, _ := backend.snapshot("node")
	resource := state.Resources[node.Address]
	if resource.Observed != nil || !resource.Ephemeral || !resource.Protected || resource.DesiredDigest != corestate.Digest(node.Desired) || !reflect.DeepEqual(resource.Delete, map[string]any{"path": deletePath}) {
		t.Fatalf("protected state resource = %#v", resource)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{secret, secretDigest, node.ProtectedIntentDigest} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("protected state leaked %q: %s", forbidden, encoded)
		}
	}
	decoded, err := corestate.Decode(encoded, "node")
	if err != nil {
		t.Fatal(err)
	}
	decodedResource := decoded.Resources[node.Address]
	if decoded.SchemaVersion != 2 || !decodedResource.Protected || decodedResource.DesiredDigest != resource.DesiredDigest || decodedResource.Observed != nil || !reflect.DeepEqual(decodedResource.Delete, resource.Delete) {
		t.Fatalf("decoded protected state = %#v", decoded)
	}
}

func TestProtectedPlanJSONDoesNotRetainObservedContent(t *testing.T) {
	secret := "not-a-real-observed-secret"
	secretDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(secret)))
	node := testNode(map[string]any{"verified": true, "url_sensitive": true, "sha256_sensitive": true})
	node.Sensitive = true
	node.Payload = map[string]any{"url": "https://example.invalid/tool?token=" + secret, "sha256": secretDigest}
	node.ProtectedIntentDigest = "not-a-serialized-protected-intent"
	provider := newMemoryProvider()
	provider.set(node.Address, ObservedResource{Exists: true, Values: map[string]any{"content": secret}})
	engine := Engine{Backend: newMemoryBackend(), Provider: provider}
	plan, err := engine.Plan(context.Background(), staticBuild(testHost(), node))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{secret, secretDigest, node.ProtectedIntentDigest, plan.Hosts[0].Fingerprint} {
		if forbidden != "" && strings.Contains(string(data), forbidden) {
			t.Fatalf("protected online plan JSON leaked %q: %s", forbidden, data)
		}
	}
	if !strings.Contains(string(data), `"protected":true`) || strings.Contains(string(data), `"Fingerprint"`) || strings.Contains(string(data), `"fingerprint"`) {
		t.Fatalf("protected online plan JSON = %s", data)
	}
}

func TestProtectedIntentChangeIsDurableNoOpButRequiresLockedReview(t *testing.T) {
	publicSHA := strings.Repeat("a", 64)
	desired := map[string]any{
		"path":   "/var/cache/alpineform/components/retained/protected/amd64/artifact",
		"sha256": publicSHA, "verified": true, "url_sensitive": true,
		"ensure": "present", "delete_behavior": ActionDelete,
	}
	desiredDigest := corestate.Digest(desired)
	backend := newMemoryBackend()
	backend.states["node"] = corestate.State{
		Product: corestate.Product, SchemaVersion: corestate.SchemaVersion, Host: "node",
		Resources: map[string]corestate.Resource{
			"host.node.component.cli.artifact.source[\"amd64\"]": {DesiredDigest: desiredDigest, Protected: true},
		},
	}
	provider := newMemoryProvider()
	address := `host.node.component.cli.artifact.source["amd64"]`
	provider.set(address, ObservedResource{Exists: true, Digest: desiredDigest, Protected: true})
	buildCalls := 0
	build := func(context.Context) (*ir.Program, *graph.ResourceGraph, error) {
		buildCalls++
		mirror := "alpha"
		intent := "protected-intent-alpha"
		if buildCalls > 1 {
			mirror = "beta"
			intent = "protected-intent-beta"
		}
		node := graph.Node{
			Host: "node", Address: address, Kind: "component_artifact_source", Managed: true, Desired: desired,
			Payload: map[string]any{"url": "https://" + mirror + ".invalid/tool"}, Sensitive: true, DigestSafe: true,
			ProtectedIntentDigest: intent, Source: ir.SourceRef{File: "main.apf.hcl", Line: 1},
		}
		return &ir.Program{Hosts: []ir.HostSpec{testHost()}}, &graph.ResourceGraph{Nodes: []graph.Node{node}}, nil
	}
	rejected := errors.New("review protected intent again")
	_, err := (Engine{Backend: backend, Provider: provider}).Apply(context.Background(), build, ApplyOptions{
		ReviewPreview: func(_ context.Context, preview Plan) error {
			if preview.Hosts[0].Steps[0].Action != ActionNoOp {
				t.Fatalf("preview mirror action = %s", preview.Hosts[0].Steps[0].Action)
			}
			return nil
		},
		ReviewLocked: func(_ context.Context, preview, locked Plan, changed bool) error {
			if !changed || preview.Hosts[0].Steps[0].Action != ActionNoOp || locked.Hosts[0].Steps[0].Action != ActionNoOp {
				t.Fatalf("protected mirror locked review: changed=%v preview=%#v locked=%#v", changed, preview, locked)
			}
			return rejected
		},
	})
	if !errors.Is(err, rejected) || buildCalls != 2 {
		t.Fatalf("protected mirror apply = %v, builds=%d", err, buildCalls)
	}
	_, writes := backend.snapshot("node")
	if writes != 0 {
		t.Fatalf("protected mirror review rejection wrote state %d time(s)", writes)
	}
}

func TestProtectedSourceRepairTriggersOtherwiseCleanInstall(t *testing.T) {
	sourceAddress := `host.node.component.cli.artifact.source["amd64"]`
	installAddress := `host.node.component.cli.artifact.install["/usr/local/bin/tool"]`
	source := graph.Node{
		Host: "node", Address: sourceAddress, Kind: "component_artifact_source", Managed: true,
		Desired:   map[string]any{"path": "/var/cache/alpineform/components/cli/protected/amd64/artifact", "verified": true},
		Sensitive: true, DigestSafe: true,
	}
	install := graph.Node{
		Host: "node", Address: installAddress, Kind: "component_binary", Managed: true,
		Desired:   map[string]any{"path": "/usr/local/bin/tool", "content_verified": true},
		DependsOn: []string{sourceAddress}, TriggeredBy: []string{sourceAddress}, Sensitive: true, DigestSafe: true,
	}
	backend := newMemoryBackend()
	backend.states["node"] = corestate.State{
		Product: corestate.Product, SchemaVersion: corestate.SchemaVersion, Host: "node",
		Resources: map[string]corestate.Resource{
			sourceAddress:  {DesiredDigest: corestate.Digest(source.Desired), Protected: true},
			installAddress: {DesiredDigest: corestate.Digest(install.Desired), Protected: true},
		},
	}
	provider := newMemoryProvider()
	provider.set(installAddress, ObservedResource{Exists: true, Digest: corestate.Digest(install.Desired), Protected: true})
	plan, err := (Engine{Backend: backend, Provider: provider}).Plan(context.Background(), staticBuild(testHost(), source, install))
	if err != nil {
		t.Fatal(err)
	}
	steps := map[string]Step{}
	for _, step := range plan.Hosts[0].Steps {
		steps[step.Address] = step
	}
	if steps[sourceAddress].Action != ActionCreate || steps[installAddress].Action != ActionUpdate || !reflect.DeepEqual(steps[installAddress].TriggeredBy, []string{sourceAddress}) {
		t.Fatalf("protected source repair plan = %#v", plan.Hosts[0].Steps)
	}
}

func TestProtectedOrphanDeletionUsesOnlyRecordedStablePaths(t *testing.T) {
	tests := []struct {
		name           string
		kind           string
		address        string
		deleteBehavior string
		delete         map[string]any
	}{
		{
			name: "source", kind: "component_artifact_source", address: `host.node.component.cli.artifact.source["amd64"]`, deleteBehavior: ActionDelete,
			delete: map[string]any{"path": "/var/cache/alpineform/components/retained/protected/amd64/artifact"},
		},
		{
			name: "binary", kind: "component_binary", address: `host.node.component.binary.artifact.install["/usr/local/bin/tool"]`, deleteBehavior: ActionDestroy,
			delete: map[string]any{"path": "/usr/local/bin/tool"},
		},
		{
			name: "file", kind: "component_file", address: `host.node.component.file.artifact.install["/etc/tool.conf"]`, deleteBehavior: ActionDestroy,
			delete: map[string]any{"path": "/etc/tool.conf"},
		},
		{
			name: "archive", kind: "component_archive", address: `host.node.component.archive.artifact.install["/opt/tool"]`, deleteBehavior: ActionDestroy,
			delete: map[string]any{"path": "/opt/tool"},
		},
		{
			name: "ca", kind: "component_ca_certificate", address: `host.node.component.ca.artifact.install["/usr/local/share/ca-certificates/root.crt"]`, deleteBehavior: ActionDestroy,
			delete: map[string]any{
				"path":         "/usr/local/share/ca-certificates/root.crt",
				"trust_marker": "/var/lib/alpineform/ca-certificates/retained/protected/amd64.updated",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := newMemoryBackend()
			backend.states["node"] = corestate.State{
				Product: corestate.Product, SchemaVersion: corestate.SchemaVersion, Host: "node",
				Resources: map[string]corestate.Resource{
					test.address: {
						Host: "node", Kind: test.kind, Ownership: "managed", Order: 1,
						Protected: true, DeleteBehavior: test.deleteBehavior, Delete: test.delete,
					},
				},
			}
			provider := newMemoryProvider()
			_, err := (Engine{Backend: backend, Provider: provider}).Apply(context.Background(), staticBuild(testHost()), ApplyOptions{
				ReviewPreview: func(_ context.Context, preview Plan) error {
					step := preview.Hosts[0].Steps[0]
					if step.Action != test.deleteBehavior || step.Node.Payload != nil || step.Prior == nil || !reflect.DeepEqual(step.Prior.Delete, test.delete) {
						t.Fatalf("protected orphan preview = %#v", step)
					}
					return nil
				},
				ReviewLocked: func(_ context.Context, _, locked Plan, changed bool) error {
					if changed || locked.Hosts[0].Steps[0].Node.Payload != nil {
						t.Fatalf("protected orphan locked plan = changed %v, %#v", changed, locked)
					}
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			provider.mu.Lock()
			deleted := append([]Step(nil), provider.deleted...)
			provider.mu.Unlock()
			if len(deleted) != 1 || deleted[0].Prior == nil || !reflect.DeepEqual(deleted[0].Prior.Delete, test.delete) || deleted[0].Node.Payload != nil {
				t.Fatalf("protected orphan delete step = %#v", deleted)
			}
			state, _ := backend.snapshot("node")
			if _, exists := state.Resources[test.address]; exists {
				t.Fatalf("protected orphan remained in state: %#v", state.Resources[test.address])
			}
		})
	}
}

func TestProtectedProviderErrorsNeverExposeDetails(t *testing.T) {
	secret := "not-a-real-provider-error-secret"
	node := testNode(map[string]any{"content": secret})
	node.Sensitive = true
	actionEngine := Engine{Backend: newMemoryBackend(), Provider: failingProvider{inspectError: errors.New(secret)}}
	if _, err := actionEngine.Plan(context.Background(), staticBuild(testHost(), node)); err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "inspect protected resource") {
		t.Fatalf("protected inspect error = %v", err)
	}

	provider := failingProvider{observed: ObservedResource{}, applyError: errors.New(secret)}
	actionEngine.Provider = provider
	_, err := actionEngine.Apply(context.Background(), staticBuild(testHost(), node), ApplyOptions{
		ReviewPreview: func(context.Context, Plan) error { return nil },
		ReviewLocked:  func(context.Context, Plan, Plan, bool) error { return nil },
	})
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "create protected resource") {
		t.Fatalf("protected apply error = %v", err)
	}

	backend := newMemoryBackend()
	backend.states["node"] = corestate.State{Product: corestate.Product, SchemaVersion: corestate.SchemaVersion, Host: "node", Resources: map[string]corestate.Resource{
		"orphan": {Protected: true, DeleteBehavior: ActionDestroy},
	}}
	actionEngine = Engine{Backend: backend, Provider: failingProvider{deleteError: errors.New(secret)}}
	_, err = actionEngine.Apply(context.Background(), staticBuild(testHost()), ApplyOptions{
		ReviewPreview: func(context.Context, Plan) error { return nil },
		ReviewLocked:  func(context.Context, Plan, Plan, bool) error { return nil },
	})
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "destroy protected resource") {
		t.Fatalf("protected delete error = %v", err)
	}
}

func TestComponentScriptTriggerDedupAndNoOpExecution(t *testing.T) {
	first := graph.Node{Host: "node", Address: "host.node.first", Kind: "file", Managed: true, Desired: map[string]any{"value": "first"}, Source: ir.SourceRef{File: "main.apf.hcl", Line: 1}}
	second := graph.Node{Host: "node", Address: "host.node.second", Kind: "file", Managed: true, Desired: map[string]any{"value": "second"}, Source: ir.SourceRef{File: "main.apf.hcl", Line: 2}}
	scriptDesired := map[string]any{"name": "refresh", "script_digest": "digest", "outputs": []string{}, "ensure": "present"}
	script := graph.Node{
		Host: "node", Address: `host.node.script["refresh"]`, Kind: "component_script", Managed: true, Desired: scriptDesired,
		DependsOn: []string{first.Address, second.Address}, TriggeredBy: []string{first.Address, second.Address}, Source: ir.SourceRef{File: "main.apf.hcl", Line: 3},
	}
	backend := newMemoryBackend()
	provider := newMemoryProvider()
	provider.set(script.Address, ObservedResource{Exists: true, Values: scriptDesired, Digest: corestate.Digest(scriptDesired)})
	actionEngine := Engine{Backend: backend, Provider: provider}
	apply := func() {
		t.Helper()
		_, err := actionEngine.Apply(context.Background(), staticBuild(testHost(), first, second, script), ApplyOptions{
			ReviewPreview: func(context.Context, Plan) error { return nil },
			ReviewLocked:  func(context.Context, Plan, Plan, bool) error { return nil },
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	apply()
	provider.mu.Lock()
	firstApplied := append([]Step(nil), provider.applied...)
	provider.mu.Unlock()
	scriptRuns := 0
	for _, step := range firstApplied {
		if step.Address == script.Address {
			scriptRuns++
			if !reflect.DeepEqual(step.TriggeredBy, []string{first.Address, second.Address}) {
				t.Fatalf("script triggers = %#v", step.TriggeredBy)
			}
		}
	}
	if scriptRuns != 1 {
		t.Fatalf("script runs = %d, applied = %#v", scriptRuns, firstApplied)
	}
	before, _ := provider.counts()
	apply()
	after, _ := provider.counts()
	if after != before {
		t.Fatalf("no-op apply executed %d additional resources", after-before)
	}
}

func TestComponentScriptFailureWritesNoSuccessfulState(t *testing.T) {
	node := graph.Node{Host: "node", Address: `host.node.script["fail"]`, Kind: "component_script", Managed: true, Desired: map[string]any{"name": "fail"}, Source: ir.SourceRef{File: "main.apf.hcl", Line: 1}}
	backend := newMemoryBackend()
	actionEngine := Engine{Backend: backend, Provider: failingProvider{applyError: errors.New("script failed")}}
	_, err := actionEngine.Apply(context.Background(), staticBuild(testHost(), node), ApplyOptions{
		ReviewPreview: func(context.Context, Plan) error { return nil },
		ReviewLocked:  func(context.Context, Plan, Plan, bool) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "script failed") {
		t.Fatalf("script failure = %v", err)
	}
	state, writes := backend.snapshot("node")
	if writes != 0 || len(state.Resources) != 0 {
		t.Fatalf("failed script state = %#v, writes = %d", state, writes)
	}
}

func TestPlanRejectsNilBuildInputs(t *testing.T) {
	engine := Engine{Backend: newMemoryBackend(), Provider: newMemoryProvider()}
	if _, err := engine.Plan(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "build callback") {
		t.Fatalf("nil callback error = %v", err)
	}
	build := func(context.Context) (*ir.Program, *graph.ResourceGraph, error) { return nil, nil, nil }
	if _, err := engine.Plan(context.Background(), build); err == nil || !strings.Contains(err.Error(), "nil program or graph") {
		t.Fatalf("nil build result error = %v", err)
	}
}

func TestPlanOrderingIsStable(t *testing.T) {
	nodes := []graph.Node{
		{Host: "node", Address: "host.node.z", Kind: "test", Managed: true, Desired: map[string]any{"v": 1}},
		{Host: "node", Address: "host.node.a", Kind: "test", Managed: true, Desired: map[string]any{"v": 2}},
	}
	engine := Engine{Backend: newMemoryBackend(), Provider: newMemoryProvider()}
	plan, err := engine.Plan(context.Background(), staticBuild(testHost(), nodes...))
	if err != nil {
		t.Fatal(err)
	}
	addresses := []string{plan.Hosts[0].Steps[0].Address, plan.Hosts[0].Steps[1].Address}
	sort.Strings(addresses)
	if !reflect.DeepEqual(addresses, []string{"host.node.a", "host.node.z"}) || plan.Hosts[0].Steps[0].Address != "host.node.a" {
		t.Fatalf("step order = %#v", plan.Hosts[0].Steps)
	}
}

func TestPlanFingerprintIgnoresOnlyFactDetectionTime(t *testing.T) {
	firstHost := testHost()
	secondHost := testHost()
	firstHost.Facts.DetectedAt = "2026-07-13T08:00:00Z"
	secondHost.Facts.DetectedAt = "2026-07-13T08:00:01Z"
	first := planFingerprint(HostPlan{Host: firstHost})
	second := planFingerprint(HostPlan{Host: secondHost})
	if first != second {
		t.Fatalf("detection time changed fingerprint: %q != %q", first, second)
	}
	secondHost.Facts.Version = "3.24.2"
	if first == planFingerprint(HostPlan{Host: secondHost}) {
		t.Fatal("semantic fact change did not change fingerprint")
	}
}

func TestPlanFingerprintIncludesProtectedIntentDigest(t *testing.T) {
	node := testNode(map[string]any{"verified": true})
	step := Step{Address: node.Address, Action: ActionNoOp, Node: node, Observed: ObservedResource{Exists: true, Digest: corestate.Digest(node.Desired)}}
	first := planFingerprint(HostPlan{Host: testHost(), Steps: []Step{step}})
	step.Node.ProtectedIntentDigest = "different-protected-intent"
	second := planFingerprint(HostPlan{Host: testHost(), Steps: []Step{step}})
	if first == second {
		t.Fatalf("protected intent did not change fingerprint: %q", first)
	}
}

func TestPlanPreservesDependencyOrder(t *testing.T) {
	nodes := []graph.Node{
		{Host: "node", Address: "host.node.a", Kind: "test", Managed: true, Desired: map[string]any{"v": 1}, DependsOn: []string{"host.node.z"}},
		{Host: "node", Address: "host.node.z", Kind: "test", Managed: true, Desired: map[string]any{"v": 2}},
	}
	engine := Engine{Backend: newMemoryBackend(), Provider: newMemoryProvider()}
	plan, err := engine.Plan(context.Background(), staticBuild(testHost(), nodes...))
	if err != nil {
		t.Fatal(err)
	}
	got := []string{plan.Hosts[0].Steps[0].Address, plan.Hosts[0].Steps[1].Address}
	if !reflect.DeepEqual(got, []string{"host.node.z", "host.node.a"}) {
		t.Fatalf("dependency order = %#v", got)
	}
}

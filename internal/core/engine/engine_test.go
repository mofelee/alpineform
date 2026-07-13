package engine

import (
	"context"
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
	node := testNode(map[string]any{"content": secret, "sensitive": true})
	node.Sensitive = true
	backend := newMemoryBackend()
	provider := newMemoryProvider()
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
	if resource.Observed != nil || !resource.Sensitive {
		t.Fatalf("protected state resource = %#v", resource)
	}
}

func TestProtectedPlanJSONDoesNotRetainObservedContent(t *testing.T) {
	secret := "not-a-real-observed-secret"
	node := testNode(map[string]any{"content": "desired"})
	node.Sensitive = true
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
	if strings.Contains(string(data), secret) || !strings.Contains(string(data), `"protected":true`) {
		t.Fatalf("protected online plan JSON = %s", data)
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

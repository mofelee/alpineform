package engine

import (
	"context"
	"encoding/json"
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

type migrationRecorder struct {
	mu     sync.Mutex
	events []string
}

func (recorder *migrationRecorder) add(event string) {
	recorder.mu.Lock()
	recorder.events = append(recorder.events, event)
	recorder.mu.Unlock()
}

func (recorder *migrationRecorder) snapshot() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.events...)
}

type migrationBackend struct {
	*memoryBackend
	recorder         *migrationRecorder
	mu               sync.Mutex
	writeAttempts    int
	failWriteAttempt int
}

func (backend *migrationBackend) Write(ctx context.Context, host ir.HostSpec, state corestate.State) (corestate.State, error) {
	backend.mu.Lock()
	backend.writeAttempts++
	attempt := backend.writeAttempts
	fail := attempt == backend.failWriteAttempt
	backend.mu.Unlock()
	backend.recorder.add(fmt.Sprintf("write:%d", attempt))
	if fail {
		return corestate.State{}, errors.New("state write failed")
	}
	return backend.memoryBackend.Write(ctx, host, state)
}

type migrationProvider struct {
	backend     *memoryBackend
	recorder    *migrationRecorder
	mu          sync.Mutex
	observed    map[string]ObservedResource
	migrateErr  error
	applyErrors map[string]error
	migrations  []Step
	applied     []Step
	locked      []bool
}

func newMigrationHarness() (*migrationBackend, *migrationProvider) {
	recorder := &migrationRecorder{}
	backend := &migrationBackend{memoryBackend: newMemoryBackend(), recorder: recorder}
	provider := &migrationProvider{
		backend: backend.memoryBackend, recorder: recorder,
		observed: map[string]ObservedResource{}, applyErrors: map[string]error{},
	}
	return backend, provider
}

func (provider *migrationProvider) Inspect(_ context.Context, node graph.Node) (ObservedResource, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.observed[node.Address], nil
}

func (provider *migrationProvider) MigrateProtectedPrior(_ context.Context, step Step) error {
	provider.recorder.add("migrate:" + step.Address)
	provider.mu.Lock()
	provider.migrations = append(provider.migrations, step)
	provider.locked = append(provider.locked, provider.backend.isLocked(step.Host))
	err := provider.migrateErr
	provider.mu.Unlock()
	return err
}

func (provider *migrationProvider) Apply(_ context.Context, step Step) (ObservedResource, error) {
	provider.recorder.add("apply:" + step.Address)
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.applied = append(provider.applied, step)
	if err := provider.applyErrors[step.Address]; err != nil {
		return ObservedResource{}, err
	}
	observed := ObservedResource{
		Exists: true, Digest: corestate.Digest(step.Node.Desired),
		Protected: step.Node.Sensitive || step.Node.Ephemeral,
	}
	provider.observed[step.Address] = observed
	return observed, nil
}

func (provider *migrationProvider) Delete(_ context.Context, step Step) error {
	provider.recorder.add("delete:" + step.Address)
	return nil
}

func (provider *migrationProvider) mutationSnapshot() ([]Step, []Step, []bool) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]Step(nil), provider.migrations...), append([]Step(nil), provider.applied...), append([]bool(nil), provider.locked...)
}

type sourceReclassification struct {
	node       graph.Node
	prior      corestate.Resource
	secretURL  string
	secretSHA  string
	legacyPath string
	stablePath string
}

func newSourceReclassification() sourceReclassification {
	secretURL := "https://example.invalid/tool?token=not-a-real-migration-secret"
	secretSHA := strings.Repeat("e", 64)
	legacyPath := "/var/cache/alpineform/components/tool/" + secretSHA + "/artifact"
	stablePath := "/var/cache/alpineform/components/tool/protected/amd64/artifact"
	desired := map[string]any{
		"path": stablePath, "ensure": "present", "delete_behavior": ActionDelete,
		"delete": map[string]any{"path": stablePath}, "verified": true,
		"url_sensitive": true, "sha256_sensitive": true,
	}
	node := graph.Node{
		Host: "node", Address: `host.node.component.tool.artifact.source["amd64"]`,
		Kind: "component_artifact_source", Managed: true, Desired: desired,
		Payload:   map[string]any{"url": secretURL, "sha256": secretSHA},
		Sensitive: true, DigestSafe: true, ProtectedIntentDigest: "not-a-public-protected-intent",
	}
	priorDesired := map[string]any{"path": legacyPath, "url": secretURL, "sha256": secretSHA}
	prior := corestate.Resource{
		Host: "node", Kind: node.Kind, Ownership: "managed",
		Desired: priorDesired, DesiredDigest: corestate.Digest(priorDesired),
		Observed:       map[string]any{"path": legacyPath, "url": secretURL, "sha256": secretSHA},
		DeleteBehavior: ActionDelete, Delete: map[string]any{"path": legacyPath},
	}
	return sourceReclassification{
		node: node, prior: prior, secretURL: secretURL, secretSHA: secretSHA,
		legacyPath: legacyPath, stablePath: stablePath,
	}
}

func seedMigrationState(backend *migrationBackend, fixture sourceReclassification) {
	backend.states[fixture.node.Host] = corestate.State{
		Product: corestate.Product, SchemaVersion: corestate.SchemaVersion, Host: fixture.node.Host,
		Resources: map[string]corestate.Resource{fixture.node.Address: fixture.prior},
	}
}

func approvedMigrationApply(recorder *migrationRecorder) ApplyOptions {
	return ApplyOptions{
		ReviewPreview: func(context.Context, Plan) error { return nil },
		ReviewLocked: func(context.Context, Plan, Plan, bool) error {
			recorder.add("review")
			return nil
		},
	}
}

func TestProtectedReclassificationScrubSurvivesProviderFailure(t *testing.T) {
	fixture := newSourceReclassification()
	backend, provider := newMigrationHarness()
	seedMigrationState(backend, fixture)
	provider.applyErrors[fixture.node.Address] = errors.New("not-a-real-provider-secret")

	_, err := (Engine{Backend: backend, Provider: provider}).Apply(
		context.Background(), staticBuild(testHost(), fixture.node), approvedMigrationApply(backend.recorder),
	)
	if err == nil || strings.Contains(err.Error(), "not-a-real-provider-secret") {
		t.Fatalf("protected apply error = %v", err)
	}
	wantEvents := []string{"review", "migrate:" + fixture.node.Address, "write:1", "apply:" + fixture.node.Address}
	if events := backend.recorder.snapshot(); !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("mutation order = %#v, want %#v", events, wantEvents)
	}
	migrations, applied, locked := provider.mutationSnapshot()
	if len(migrations) != 1 || migrations[0].Node.Payload["prior_delete_path"] != fixture.legacyPath || len(applied) != 1 || !reflect.DeepEqual(locked, []bool{true}) {
		t.Fatalf("migration/apply calls = migrations %#v, applied %#v, locked %#v", migrations, applied, locked)
	}

	state, writes := backend.snapshot("node")
	resource := state.Resources[fixture.node.Address]
	if writes != 1 || !resource.Protected || resource.Desired != nil || resource.Observed != nil || resource.DesiredDigest != "" || resource.Delete["path"] != fixture.stablePath {
		t.Fatalf("durable scrub state = %#v after %d writes", resource, writes)
	}
	encoded, encodeErr := corestate.Encode(state)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	for _, forbidden := range []string{fixture.secretURL, fixture.secretSHA, fixture.legacyPath, fixture.node.ProtectedIntentDigest} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("scrubbed state leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestProtectedCAReclassificationScrubSurvivesProviderFailure(t *testing.T) {
	secretSHA := strings.Repeat("f", 64)
	target := "/usr/local/share/ca-certificates/tool.crt"
	legacyMarker := "/var/lib/alpineform/ca-certificates/" + secretSHA + ".updated"
	stableMarker := "/var/lib/alpineform/ca-certificates/tool/protected/any.updated"
	desired := map[string]any{
		"path": target, "cache_path": "/var/cache/alpineform/components/tool/protected/any/artifact",
		"owner": "root", "group": "root", "mode": "0644",
		"content_verified": true, "content_sha256_sensitive": true,
		"trust_marker": stableMarker, "trust_updated": true,
		"delete": map[string]any{"path": target, "trust_marker": stableMarker},
	}
	node := graph.Node{
		Host: "node", Address: `host.node.component.tool.artifact.install["/usr/local/share/ca-certificates/tool.crt"]`,
		Kind: "component_ca_certificate", Managed: true, Desired: desired,
		Payload: map[string]any{"content_sha256": secretSHA}, Sensitive: true, DigestSafe: true,
	}
	priorDesired := map[string]any{"path": target, "content_sha256": secretSHA, "trust_marker": legacyMarker}
	prior := corestate.Resource{
		Host: "node", Kind: node.Kind, Ownership: "managed",
		Desired: priorDesired, DesiredDigest: corestate.Digest(priorDesired),
		Observed:       map[string]any{"path": target, "content_sha256": secretSHA, "trust_marker": legacyMarker},
		DeleteBehavior: ActionDestroy, Delete: map[string]any{"path": target, "trust_marker": legacyMarker},
	}
	backend, provider := newMigrationHarness()
	backend.states["node"] = corestate.State{
		Product: corestate.Product, SchemaVersion: corestate.SchemaVersion, Host: "node",
		Resources: map[string]corestate.Resource{node.Address: prior},
	}
	provider.applyErrors[node.Address] = errors.New("not-a-real-CA-provider-secret")

	_, err := (Engine{Backend: backend, Provider: provider}).Apply(
		context.Background(), staticBuild(testHost(), node), approvedMigrationApply(backend.recorder),
	)
	if err == nil || strings.Contains(err.Error(), "not-a-real-CA-provider-secret") {
		t.Fatalf("protected CA apply error = %v", err)
	}
	wantEvents := []string{"review", "migrate:" + node.Address, "write:1", "apply:" + node.Address}
	if events := backend.recorder.snapshot(); !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("CA mutation order = %#v, want %#v", events, wantEvents)
	}
	migrations, applied, locked := provider.mutationSnapshot()
	if len(migrations) != 1 || migrations[0].Node.Payload["prior_trust_marker"] != legacyMarker || len(applied) != 1 || !reflect.DeepEqual(locked, []bool{true}) {
		t.Fatalf("CA migration/apply calls = migrations %#v, applied %#v, locked %#v", migrations, applied, locked)
	}

	state, writes := backend.snapshot("node")
	resource := state.Resources[node.Address]
	wantDelete := map[string]any{"path": target, "trust_marker": stableMarker}
	if writes != 1 || !resource.Protected || resource.Desired != nil || resource.Observed != nil || resource.DesiredDigest != "" ||
		!reflect.DeepEqual(resource.Delete, wantDelete) {
		t.Fatalf("durable CA scrub state = %#v after %d writes", resource, writes)
	}
	encoded, encodeErr := corestate.Encode(state)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	for _, forbidden := range []string{secretSHA, legacyMarker} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("scrubbed CA state leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestProtectedReclassificationReviewAndMigrationFailuresDoNotWrite(t *testing.T) {
	fixture := newSourceReclassification()
	t.Run("review rejected", func(t *testing.T) {
		backend, provider := newMigrationHarness()
		seedMigrationState(backend, fixture)
		rejected := errors.New("review rejected")
		_, err := (Engine{Backend: backend, Provider: provider}).Apply(context.Background(), staticBuild(testHost(), fixture.node), ApplyOptions{
			ReviewPreview: func(context.Context, Plan) error { return nil },
			ReviewLocked: func(context.Context, Plan, Plan, bool) error {
				backend.recorder.add("review")
				return rejected
			},
		})
		if !errors.Is(err, rejected) || !reflect.DeepEqual(backend.recorder.snapshot(), []string{"review"}) {
			t.Fatalf("review rejection = %v, events %#v", err, backend.recorder.snapshot())
		}
		migrations, applied, _ := provider.mutationSnapshot()
		_, writes := backend.snapshot("node")
		if len(migrations) != 0 || len(applied) != 0 || writes != 0 {
			t.Fatalf("mutations after review rejection: migrations=%d applied=%d writes=%d", len(migrations), len(applied), writes)
		}
	})

	t.Run("migration failed", func(t *testing.T) {
		backend, provider := newMigrationHarness()
		seedMigrationState(backend, fixture)
		secret := "not-a-real-migration-error-secret"
		provider.migrateErr = errors.New(secret)
		_, err := (Engine{Backend: backend, Provider: provider}).Apply(
			context.Background(), staticBuild(testHost(), fixture.node), approvedMigrationApply(backend.recorder),
		)
		wantEvents := []string{"review", "migrate:" + fixture.node.Address}
		if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "migrate prior identity") || !reflect.DeepEqual(backend.recorder.snapshot(), wantEvents) {
			t.Fatalf("migration failure = %v, events %#v", err, backend.recorder.snapshot())
		}
		_, applied, locked := provider.mutationSnapshot()
		_, writes := backend.snapshot("node")
		if len(applied) != 0 || writes != 0 || !reflect.DeepEqual(locked, []bool{true}) {
			t.Fatalf("mutations after migration failure: applied=%d writes=%d locked=%#v", len(applied), writes, locked)
		}
	})
}

func TestProtectedReclassificationRejectsMissingLegacyIdentity(t *testing.T) {
	fixture := newSourceReclassification()
	fixture.prior.Delete = nil
	backend, provider := newMigrationHarness()
	seedMigrationState(backend, fixture)

	_, err := (Engine{Backend: backend, Provider: provider}).Plan(context.Background(), staticBuild(testHost(), fixture.node))
	if err == nil || !strings.Contains(err.Error(), "no recorded cleanup identity") || strings.Contains(err.Error(), fixture.secretURL) || strings.Contains(err.Error(), fixture.secretSHA) {
		t.Fatalf("missing migration identity error = %v", err)
	}
	migrations, applied, _ := provider.mutationSnapshot()
	_, writes := backend.snapshot("node")
	if len(migrations) != 0 || len(applied) != 0 || writes != 0 {
		t.Fatalf("missing identity mutations: migrations=%d applied=%d writes=%d", len(migrations), len(applied), writes)
	}
}

func TestProtectedReclassificationRetriesAfterPrewriteFailure(t *testing.T) {
	fixture := newSourceReclassification()
	backend, provider := newMigrationHarness()
	seedMigrationState(backend, fixture)
	backend.failWriteAttempt = 1
	engine := Engine{Backend: backend, Provider: provider}

	_, err := engine.Apply(context.Background(), staticBuild(testHost(), fixture.node), approvedMigrationApply(backend.recorder))
	if err == nil || !strings.Contains(err.Error(), "state write failed") {
		t.Fatalf("prewrite failure = %v", err)
	}
	migrations, applied, _ := provider.mutationSnapshot()
	state, writes := backend.snapshot("node")
	if len(migrations) != 1 || len(applied) != 0 || writes != 0 || state.Resources[fixture.node.Address].Desired["url"] != fixture.secretURL {
		t.Fatalf("failed prewrite state=%#v writes=%d migrations=%d applied=%d", state, writes, len(migrations), len(applied))
	}

	backend.failWriteAttempt = 0
	if _, err := engine.Apply(context.Background(), staticBuild(testHost(), fixture.node), approvedMigrationApply(backend.recorder)); err != nil {
		t.Fatal(err)
	}
	migrations, applied, locked := provider.mutationSnapshot()
	state, writes = backend.snapshot("node")
	if len(migrations) != 2 || len(applied) != 1 || writes != 2 || !state.Resources[fixture.node.Address].Protected || state.Resources[fixture.node.Address].DesiredDigest == "" {
		t.Fatalf("retry result state=%#v writes=%d migrations=%d applied=%d", state, writes, len(migrations), len(applied))
	}
	if !reflect.DeepEqual(locked, []bool{true, true}) {
		t.Fatalf("migration lease state = %#v", locked)
	}
}

func TestProtectedReclassificationRetriesAfterFinalWriteFailure(t *testing.T) {
	fixture := newSourceReclassification()
	backend, provider := newMigrationHarness()
	seedMigrationState(backend, fixture)
	backend.failWriteAttempt = 2
	engine := Engine{Backend: backend, Provider: provider}

	_, err := engine.Apply(context.Background(), staticBuild(testHost(), fixture.node), approvedMigrationApply(backend.recorder))
	if err == nil || !strings.Contains(err.Error(), "state write failed") {
		t.Fatalf("final write failure = %v", err)
	}
	state, writes := backend.snapshot("node")
	migrations, applied, _ := provider.mutationSnapshot()
	if writes != 1 || len(migrations) != 1 || len(applied) != 1 || state.Resources[fixture.node.Address].DesiredDigest != "" || state.Resources[fixture.node.Address].Delete["path"] != fixture.stablePath {
		t.Fatalf("final write failure state=%#v writes=%d migrations=%d applied=%d", state, writes, len(migrations), len(applied))
	}

	backend.failWriteAttempt = 0
	var retryAction string
	options := approvedMigrationApply(backend.recorder)
	options.ReviewLocked = func(_ context.Context, _, locked Plan, _ bool) error {
		backend.recorder.add("review")
		retryAction = locked.Hosts[0].Steps[0].Action
		return nil
	}
	if _, err := engine.Apply(context.Background(), staticBuild(testHost(), fixture.node), options); err != nil {
		t.Fatal(err)
	}
	state, writes = backend.snapshot("node")
	migrations, applied, _ = provider.mutationSnapshot()
	if retryAction != ActionAdopt || writes != 2 || len(migrations) != 1 || len(applied) != 1 || state.Resources[fixture.node.Address].DesiredDigest != corestate.Digest(fixture.node.Desired) {
		t.Fatalf("final write retry action=%q state=%#v writes=%d migrations=%d applied=%d", retryAction, state, writes, len(migrations), len(applied))
	}
}

func TestProtectedReclassificationScrubSurvivesLaterStepFailureAndRestart(t *testing.T) {
	fixture := newSourceReclassification()
	later := testNode(map[string]any{"value": "later"})
	later.Address = "host.node.test.later"
	backend, provider := newMigrationHarness()
	seedMigrationState(backend, fixture)
	provider.applyErrors[later.Address] = errors.New("later failed")
	engine := Engine{Backend: backend, Provider: provider}

	_, err := engine.Apply(context.Background(), staticBuild(testHost(), fixture.node, later), approvedMigrationApply(backend.recorder))
	if err == nil || !strings.Contains(err.Error(), "later failed") {
		t.Fatalf("later failure = %v", err)
	}
	state, writes := backend.snapshot("node")
	if writes != 1 || state.Resources[fixture.node.Address].Delete["path"] != fixture.stablePath || state.Resources[fixture.node.Address].DesiredDigest != "" {
		t.Fatalf("scrub after later failure = %#v after %d writes", state, writes)
	}
	delete(provider.applyErrors, later.Address)
	var retrySourceAction string
	options := approvedMigrationApply(backend.recorder)
	options.ReviewLocked = func(_ context.Context, _, locked Plan, _ bool) error {
		backend.recorder.add("review")
		for _, step := range locked.Hosts[0].Steps {
			if step.Address == fixture.node.Address {
				retrySourceAction = step.Action
			}
		}
		return nil
	}
	if _, err := engine.Apply(context.Background(), staticBuild(testHost(), fixture.node, later), options); err != nil {
		t.Fatal(err)
	}
	migrations, applied, _ := provider.mutationSnapshot()
	state, writes = backend.snapshot("node")
	if retrySourceAction != ActionAdopt || len(migrations) != 1 || len(applied) != 3 || writes != 2 || state.Resources[fixture.node.Address].DesiredDigest != corestate.Digest(fixture.node.Desired) {
		t.Fatalf("restart result action=%q migrations=%d applied=%d writes=%d state=%#v", retrySourceAction, len(migrations), len(applied), writes, state)
	}
}

func TestProtectedInstallReclassificationPrewritesWithoutMigration(t *testing.T) {
	for _, kind := range []string{"component_binary", "component_file", "component_archive"} {
		t.Run(kind, func(t *testing.T) {
			path := "/opt/tool/" + kind
			desired := map[string]any{
				"path": path, "delete": map[string]any{"path": path},
				"content_verified": true, "content_sha256_sensitive": true,
			}
			node := testNode(desired)
			node.Kind = kind
			node.Sensitive = true
			node.DigestSafe = true
			node.Payload = map[string]any{"content_sha256": strings.Repeat("a", 64)}
			priorDesired := map[string]any{"path": path, "content_sha256": strings.Repeat("a", 64)}
			backend, provider := newMigrationHarness()
			backend.states["node"] = corestate.State{
				Product: corestate.Product, SchemaVersion: corestate.SchemaVersion, Host: "node",
				Resources: map[string]corestate.Resource{node.Address: {
					Host: "node", Kind: kind, Ownership: "managed", Desired: priorDesired,
					DesiredDigest: corestate.Digest(priorDesired), Observed: priorDesired,
					Delete: map[string]any{"path": path},
				}},
			}
			provider.observed[node.Address] = ObservedResource{Exists: true, Digest: corestate.Digest(desired), Protected: true}
			if _, err := (Engine{Backend: backend, Provider: provider}).Apply(
				context.Background(), staticBuild(testHost(), node), approvedMigrationApply(backend.recorder),
			); err != nil {
				t.Fatal(err)
			}
			migrations, applied, _ := provider.mutationSnapshot()
			wantEvents := []string{"review", "write:1", "write:2"}
			if len(migrations) != 0 || len(applied) != 0 || !reflect.DeepEqual(backend.recorder.snapshot(), wantEvents) {
				t.Fatalf("install migration=%d applied=%d events=%#v", len(migrations), len(applied), backend.recorder.snapshot())
			}
		})
	}
}

func TestMoveAndProtectedScrubShareOnePrewrite(t *testing.T) {
	fixture := newSourceReclassification()
	backend, provider := newMigrationHarness()
	scrubbed := planProtectedPrior(fixture.prior, fixture.node, true)
	step := planNode(fixture.node, scrubbed, true, ObservedResource{})
	step.ReclassifiedProtected = true
	step.Node.Payload = copyMap(step.Node.Payload)
	step.Node.Payload["prior_delete_path"] = fixture.legacyPath
	plan := HostPlan{
		Host: testHost(), PriorState: corestate.State{
			Product: corestate.Product, SchemaVersion: corestate.SchemaVersion, Host: "node",
			Resources: map[string]corestate.Resource{fixture.node.Address: scrubbed},
		},
		Moves:         []corestate.RealizedMove{{Host: "node", From: "host.node.component.old", To: "host.node.component.tool"}},
		StatePrewrite: true, Steps: []Step{step},
	}
	engine := Engine{Backend: backend, Provider: provider}
	if err := backend.WithLease(context.Background(), plan.Host, time.Second, func(ctx context.Context) error {
		return engine.executeHost(ctx, plan)
	}); err != nil {
		t.Fatal(err)
	}
	wantEvents := []string{"migrate:" + fixture.node.Address, "write:1", "apply:" + fixture.node.Address, "write:2"}
	if events := backend.recorder.snapshot(); !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("move and scrub writes = %#v, want %#v", events, wantEvents)
	}
	_, writes := backend.snapshot("node")
	if writes != 2 {
		t.Fatalf("move and scrub wrote state %d times", writes)
	}
}

func TestPlanFingerprintIncludesHiddenCleanupIdentities(t *testing.T) {
	baseNode := testNode(map[string]any{"path": "/stable"})
	baseNode.Sensitive = true
	baseNode.Payload = map[string]any{"prior_delete_path": "/legacy-a", "prior_trust_marker": "/marker-a"}
	base := Step{
		Address: baseNode.Address, Action: ActionUpdate, Node: baseNode,
		Prior:                 &corestate.Resource{Protected: true, Delete: map[string]any{"path": "/stable-a"}},
		ReclassifiedProtected: true,
	}
	fingerprint := func(step Step) string {
		return planFingerprint(HostPlan{Host: testHost(), Steps: []Step{step}})
	}

	variants := map[string]Step{}
	changedPath := base
	changedPath.Node.Payload = copyMap(base.Node.Payload)
	changedPath.Node.Payload["prior_delete_path"] = "/legacy-b"
	variants["prior delete path"] = changedPath
	changedMarker := base
	changedMarker.Node.Payload = copyMap(base.Node.Payload)
	changedMarker.Node.Payload["prior_trust_marker"] = "/marker-b"
	variants["prior trust marker"] = changedMarker
	changedDelete := base
	prior := *base.Prior
	prior.Delete = map[string]any{"path": "/stable-b"}
	changedDelete.Prior = &prior
	variants["prior delete map"] = changedDelete
	for name, variant := range variants {
		t.Run(name, func(t *testing.T) {
			if fingerprint(base) == fingerprint(variant) {
				t.Fatalf("%s did not change plan fingerprint", name)
			}
		})
	}

	data, err := json.Marshal(Plan{Hosts: []HostPlan{{Host: testHost(), Steps: []Step{base}, Fingerprint: fingerprint(base)}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"/legacy-a", "/marker-a", fingerprint(base)} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("plan JSON leaked %q: %s", forbidden, data)
		}
	}
}

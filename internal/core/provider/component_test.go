package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

type failOnceCommandRunner struct {
	backend.Runner
	operation string
	failed    bool
}

func (runner *failOnceCommandRunner) Run(ctx context.Context, command backend.Command) ([]byte, error) {
	if command.Name == runner.operation && !runner.failed {
		runner.failed = true
		return nil, errors.New("injected cleanup failure")
	}
	return runner.Runner.Run(ctx, command)
}

func TestProtectedArtifactSourceFieldsUseOnlyRedactedStdin(t *testing.T) {
	tests := []struct {
		name string
		mark string
	}{
		{name: "URL sensitive", mark: "url_sensitive"},
		{name: "URL ephemeral", mark: "url_ephemeral"},
		{name: "SHA sensitive", mark: "sha256_sensitive"},
		{name: "SHA ephemeral", mark: "sha256_ephemeral"},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			url := "https://not-a-real-secret.invalid/tool?token=protected-" + strconv.Itoa(index)
			digest := strings.Repeat(strconv.FormatInt(int64(index+10), 16), 64)
			desired := map[string]any{
				"path": filepath.Join(t.TempDir(), "protected", "amd64", "artifact"), "verified": true,
				"ensure": "present", "delete_behavior": "delete", test.mark: true,
			}
			payload := map[string]any{}
			if strings.HasPrefix(test.mark, "url_") {
				payload["url"] = url
				desired["sha256"] = digest
			} else {
				desired["url"] = url
				payload["sha256"] = digest
			}
			node := graph.Node{Kind: "component_artifact_source", Desired: desired, Payload: payload, Sensitive: strings.HasSuffix(test.mark, "sensitive")}
			node.Ephemeral = strings.HasSuffix(test.mark, "ephemeral")
			runner := &commandRunner{outputs: map[string][]byte{"inspect.component_artifact_source": []byte("verified\n")}}
			observed, err := applyComponentSource(context.Background(), runner, engine.Step{Node: node})
			if err != nil {
				t.Fatal(err)
			}
			if !observed.Exists || !observed.Protected || observed.Values["verified"] != true || len(runner.commands) != 2 {
				t.Fatalf("protected source result = %#v, commands=%#v", observed, runner.commands)
			}
			apply := runner.commands[0]
			if !apply.RedactStdin || !apply.RedactOutput || string(apply.Stdin) != url+"\n"+digest+"\n" || !strings.Contains(apply.Script, `/usr/bin/wget -q -O "$tmp" --input-file=-`) {
				t.Fatalf("protected source apply command = %#v", apply)
			}
			for _, command := range runner.commands {
				if !command.RedactStdin || !command.RedactOutput {
					t.Fatalf("unredacted protected command = %#v", command)
				}
				for _, raw := range []string{url, digest} {
					if commandContainsOutsideStdin(command, raw) {
						t.Fatalf("protected value %q escaped stdin in %#v", raw, command)
					}
				}
			}
		})
	}
}

func TestComponentArtifactFieldPlacementIsStrictAndSafe(t *testing.T) {
	secretURL := "https://not-a-real-secret.invalid/strict"
	digest := strings.Repeat("a", 64)
	tests := []struct {
		name    string
		desired map[string]any
		payload map[string]any
	}{
		{
			name:    "protected value duplicated in desired",
			desired: map[string]any{"path": "/tmp/artifact", "url": secretURL, "url_sensitive": true, "sha256": digest},
			payload: map[string]any{"url": secretURL},
		},
		{
			name:    "protected value missing from payload",
			desired: map[string]any{"path": "/tmp/artifact", "url_sensitive": true, "sha256": digest},
			payload: map[string]any{},
		},
		{
			name:    "unmarked value placed in payload",
			desired: map[string]any{"path": "/tmp/artifact", "sha256": digest},
			payload: map[string]any{"url": secretURL},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, _, _, err := componentSourceValues(graph.Node{Desired: test.desired, Payload: test.payload})
			if err == nil || strings.Contains(err.Error(), secretURL) || strings.Contains(err.Error(), digest) {
				t.Fatalf("strict field error = %v", err)
			}
		})
	}
}

func TestProtectedArtifactSourceFailurePreservesPriorAndCleansOnlyNewIdentityParent(t *testing.T) {
	content := "protected-source-content"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(content))
	}))
	defer server.Close()
	root := t.TempDir()
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}
	nodeFor := func(path, digest string) graph.Node {
		return graph.Node{
			Kind: "component_artifact_source", Sensitive: true,
			Desired: map[string]any{
				"path": path, "verified": true, "url_sensitive": true, "sha256_sensitive": true,
			},
			Payload: map[string]any{"url": server.URL + "/artifact?token=protected", "sha256": digest},
		}
	}
	newPath := filepath.Join(root, "new", "identity", "artifact")
	if _, err := provider.Apply(context.Background(), engine.Step{Node: nodeFor(newPath, sha256String("wrong"))}); err == nil {
		t.Fatal("wrong checksum unexpectedly succeeded")
	}
	if _, err := os.Stat(filepath.Dir(newPath)); !os.IsNotExist(err) {
		t.Fatalf("new identity parent survived failure: %v", err)
	}

	priorPath := filepath.Join(root, "existing", "identity", "artifact")
	if err := os.MkdirAll(filepath.Dir(priorPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(priorPath, []byte("prior-valid-artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Apply(context.Background(), engine.Step{Node: nodeFor(priorPath, sha256String("wrong"))}); err == nil {
		t.Fatal("replacement with wrong checksum unexpectedly succeeded")
	}
	data, err := os.ReadFile(priorPath)
	if err != nil || string(data) != "prior-valid-artifact" {
		t.Fatalf("prior artifact after failure = %q, %v", data, err)
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(priorPath), ".alpineform-download.*")); len(matches) != 0 {
		t.Fatalf("temporary downloads survived failure: %#v", matches)
	}
	observed, err := provider.Apply(context.Background(), engine.Step{Node: nodeFor(priorPath, sha256String(content))})
	if err != nil || !observed.Exists || !observed.Protected || observed.Values["verified"] != true {
		t.Fatalf("protected retry = %#v, %v", observed, err)
	}
}

func TestComponentArtifactSourceCleansPreviousCacheOnlyAfterVerifiedReplacement(t *testing.T) {
	content := "replacement-artifact"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(content))
	}))
	defer server.Close()
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}

	t.Run("success", func(t *testing.T) {
		root := t.TempDir()
		priorPath := filepath.Join(root, "prior-sha", "artifact")
		currentPath := filepath.Join(root, "current-sha", "artifact")
		if err := os.MkdirAll(filepath.Dir(priorPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(priorPath, []byte("prior-artifact"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(priorPath), ".alpineform-owned"), []byte("alpineform-component-source-v1"), 0600); err != nil {
			t.Fatal(err)
		}
		node := graph.Node{Kind: "component_artifact_source", Desired: map[string]any{
			"path": currentPath, "url": server.URL + "/artifact", "sha256": sha256String(content),
		}}
		observed, err := provider.Apply(context.Background(), engine.Step{
			Node: node, Prior: &corestate.Resource{Delete: map[string]any{"path": priorPath}},
		})
		if err != nil || !observed.Exists {
			t.Fatalf("source replacement = %#v, %v", observed, err)
		}
		if _, err := os.Stat(priorPath); !os.IsNotExist(err) {
			t.Fatalf("prior cache survived successful replacement: %v", err)
		}
		if _, err := os.Stat(filepath.Dir(priorPath)); !os.IsNotExist(err) {
			t.Fatalf("prior identity parent survived successful replacement: %v", err)
		}
		if data, err := os.ReadFile(currentPath); err != nil || string(data) != content {
			t.Fatalf("current cache = %q, %v", data, err)
		}
	})

	t.Run("download failure", func(t *testing.T) {
		root := t.TempDir()
		priorPath := filepath.Join(root, "prior-sha", "artifact")
		currentPath := filepath.Join(root, "current-sha", "artifact")
		if err := os.MkdirAll(filepath.Dir(priorPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(priorPath, []byte("prior-artifact"), 0600); err != nil {
			t.Fatal(err)
		}
		node := graph.Node{Kind: "component_artifact_source", Desired: map[string]any{
			"path": currentPath, "url": server.URL + "/artifact", "sha256": sha256String("wrong"),
		}}
		_, err := provider.Apply(context.Background(), engine.Step{
			Node: node, Prior: &corestate.Resource{Delete: map[string]any{"path": priorPath}},
		})
		if err == nil {
			t.Fatal("source replacement with a wrong checksum unexpectedly succeeded")
		}
		if data, readErr := os.ReadFile(priorPath); readErr != nil || string(data) != "prior-artifact" {
			t.Fatalf("prior cache after failed replacement = %q, %v", data, readErr)
		}
		if _, statErr := os.Stat(currentPath); !os.IsNotExist(statErr) {
			t.Fatalf("failed replacement created current cache: %v", statErr)
		}
	})
}

func TestComponentArtifactSourcePriorCleanupFailureIsRetryable(t *testing.T) {
	content := "replacement-artifact"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(content))
	}))
	defer server.Close()
	root := t.TempDir()
	priorPath := filepath.Join(root, "prior-sha", "artifact")
	currentPath := filepath.Join(root, "current-sha", "artifact")
	if err := os.MkdirAll(filepath.Dir(priorPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(priorPath, []byte("prior-artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	node := graph.Node{Kind: "component_artifact_source", Desired: map[string]any{
		"path": currentPath, "url": server.URL + "/artifact", "sha256": sha256String(content),
	}}
	step := engine.Step{Node: node, Prior: &corestate.Resource{Delete: map[string]any{"path": priorPath}}}
	runner := &failOnceCommandRunner{Runner: localRunner{}, operation: "cleanup.component_artifact_source_previous"}
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return runner, nil }}
	if _, err := provider.Apply(context.Background(), step); err == nil {
		t.Fatal("injected source prior cleanup failure unexpectedly succeeded")
	}
	if data, err := os.ReadFile(priorPath); err != nil || string(data) != "prior-artifact" {
		t.Fatalf("prior source cache after cleanup failure = %q, %v", data, err)
	}
	if data, err := os.ReadFile(currentPath); err != nil || string(data) != content {
		t.Fatalf("current source cache after cleanup failure = %q, %v", data, err)
	}
	observed, err := provider.Apply(context.Background(), step)
	if err != nil || !observed.Exists {
		t.Fatalf("source prior cleanup retry = %#v, %v", observed, err)
	}
	if _, err := os.Stat(priorPath); !os.IsNotExist(err) {
		t.Fatalf("prior source cache survived cleanup retry: %v", err)
	}
}

func TestProtectedArtifactSourcePriorCleanupUsesOnlyRedactedStdin(t *testing.T) {
	url := "https://example.invalid/tool?token=protected"
	digest := strings.Repeat("a", 64)
	priorPath := "/var/cache/alpineform/components/tool/" + strings.Repeat("b", 64) + "/artifact"
	node := graph.Node{
		Kind: "component_artifact_source", Sensitive: true,
		Desired: map[string]any{
			"path":     "/var/cache/alpineform/components/tool/protected/any/artifact",
			"verified": true, "url_sensitive": true, "sha256_sensitive": true,
		},
		Payload: map[string]any{"url": url, "sha256": digest, "prior_delete_path": priorPath},
	}
	runner := &commandRunner{outputs: map[string][]byte{"inspect.component_artifact_source": []byte("verified\n")}}
	observed, err := applyComponentSource(context.Background(), runner, engine.Step{Node: node})
	if err != nil || !observed.Exists || len(runner.commands) != 3 {
		t.Fatalf("protected source cleanup = %#v, commands=%#v, error=%v", observed, runner.commands, err)
	}
	cleanup := runner.commands[2]
	if cleanup.Name != "cleanup.component_artifact_source_previous" || len(cleanup.Arguments) != 0 || string(cleanup.Stdin) != priorPath+"\n" || !cleanup.RedactStdin || !cleanup.RedactOutput {
		t.Fatalf("protected prior cleanup command = %#v", cleanup)
	}
	if commandContainsOutsideStdin(cleanup, priorPath) {
		t.Fatalf("protected prior path escaped stdin: %#v", cleanup)
	}
}

func TestProtectedBinaryAndFileFailurePreservePriorAndRetry(t *testing.T) {
	for _, kind := range []string{"component_binary", "component_file"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			cachePath := filepath.Join(root, "cache", "artifact")
			installPath := filepath.Join(root, "install", "artifact")
			if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(installPath), 0755); err != nil {
				t.Fatal(err)
			}
			content := "new-protected-content"
			if err := os.WriteFile(cachePath, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(installPath, []byte("prior-valid-content"), 0644); err != nil {
				t.Fatal(err)
			}
			node := graph.Node{
				Kind: kind, Sensitive: true,
				Desired: map[string]any{
					"path": installPath, "cache_path": cachePath, "owner": strconv.Itoa(os.Getuid()), "group": strconv.Itoa(os.Getgid()), "mode": "0644",
					"content_verified": true, "content_sha256_sensitive": true,
				},
				Payload: map[string]any{"content_sha256": sha256String("wrong")},
			}
			provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}
			if _, err := provider.Apply(context.Background(), engine.Step{Node: node}); err == nil {
				t.Fatal("wrong protected install checksum unexpectedly succeeded")
			}
			data, err := os.ReadFile(installPath)
			if err != nil || string(data) != "prior-valid-content" {
				t.Fatalf("prior install after failure = %q, %v", data, err)
			}
			node.Payload["content_sha256"] = sha256String(content)
			observed, err := provider.Apply(context.Background(), engine.Step{Node: node})
			if err != nil || !observed.Exists || !observed.Protected || observed.Values["content_verified"] != true {
				t.Fatalf("protected install retry = %#v, %v", observed, err)
			}
		})
	}
}

func TestProtectedArtifactSourceSignalBoundariesAndOwnedParentLifecycle(t *testing.T) {
	content := "protected-source-boundary"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(content))
	}))
	defer server.Close()
	nodeFor := func(path string) graph.Node {
		return graph.Node{
			Kind: "component_artifact_source", Sensitive: true,
			Desired: map[string]any{
				"path": path, "verified": true, "url_sensitive": true, "sha256_sensitive": true,
			},
			Payload: map[string]any{"url": server.URL + "/artifact?token=protected", "sha256": sha256String(content)},
		}
	}
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}

	t.Run("parent creation signal", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "new", "identity", "artifact")
		marker := installSignalAfterCommand(t, "mkdir", 2, filepath.Dir(path), "exact")
		if _, err := provider.Apply(context.Background(), engine.Step{Node: nodeFor(path)}); err == nil {
			t.Fatal("signaled source parent creation unexpectedly succeeded")
		}
		assertSignalBoundaryTriggered(t, marker)
		if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
			t.Fatalf("signaled source creation left identity parent: %v", err)
		}
	})

	t.Run("replacement commit signal", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "identity", "artifact")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("prior"), 0600); err != nil {
			t.Fatal(err)
		}
		marker := installSignalAfterCommand(t, "mv", 3, path, "exact")
		observed, err := provider.Apply(context.Background(), engine.Step{Node: nodeFor(path)})
		if err != nil || !observed.Exists || observed.Values["verified"] != true {
			t.Fatalf("source commit boundary = %#v, %v", observed, err)
		}
		assertSignalBoundaryTriggered(t, marker)
		if data, err := os.ReadFile(path); err != nil || string(data) != content {
			t.Fatalf("source commit content = %q, %v", data, err)
		}
		if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".alpineform-download.*")); len(matches) != 0 {
			t.Fatalf("source commit left temporary files: %#v", matches)
		}
	})

	t.Run("owned and preexisting parents", func(t *testing.T) {
		root := t.TempDir()
		ownedPath := filepath.Join(root, "owned", "identity", "artifact")
		ownedNode := nodeFor(ownedPath)
		if _, err := provider.Apply(context.Background(), engine.Step{Node: ownedNode}); err != nil {
			t.Fatal(err)
		}
		ownershipMarker := filepath.Join(filepath.Dir(ownedPath), ".alpineform-owned")
		if data, err := os.ReadFile(ownershipMarker); err != nil || string(data) != "alpineform-component-source-v1" {
			t.Fatalf("source ownership marker = %q, %v", data, err)
		}
		if err := provider.Delete(context.Background(), engine.Step{Node: ownedNode}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Dir(ownedPath)); !os.IsNotExist(err) {
			t.Fatalf("owned source parent survived teardown: %v", err)
		}

		preexistingPath := filepath.Join(root, "preexisting", "artifact")
		if err := os.MkdirAll(filepath.Dir(preexistingPath), 0755); err != nil {
			t.Fatal(err)
		}
		preexistingNode := nodeFor(preexistingPath)
		if _, err := provider.Apply(context.Background(), engine.Step{Node: preexistingNode}); err != nil {
			t.Fatal(err)
		}
		if err := provider.Delete(context.Background(), engine.Step{Node: preexistingNode}); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(filepath.Dir(preexistingPath)); err != nil || !info.IsDir() {
			t.Fatalf("preexisting source parent was removed: %#v, %v", info, err)
		}
	})
}

func TestProtectedInstallSignalBoundaries(t *testing.T) {
	for _, boundary := range []string{"temporary creation", "replacement commit"} {
		t.Run(boundary, func(t *testing.T) {
			root := t.TempDir()
			cachePath := filepath.Join(root, "cache", "artifact")
			installPath := filepath.Join(root, "install", "artifact")
			for _, parent := range []string{filepath.Dir(cachePath), filepath.Dir(installPath)} {
				if err := os.MkdirAll(parent, 0755); err != nil {
					t.Fatal(err)
				}
			}
			content := "new-protected-content"
			if err := os.WriteFile(cachePath, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(installPath, []byte("prior-valid-content"), 0644); err != nil {
				t.Fatal(err)
			}
			node := graph.Node{
				Kind: "component_binary", Sensitive: true,
				Desired: map[string]any{
					"path": installPath, "cache_path": cachePath, "owner": strconv.Itoa(os.Getuid()), "group": strconv.Itoa(os.Getgid()), "mode": "0644",
					"content_verified": true, "content_sha256_sensitive": true,
				},
				Payload: map[string]any{"content_sha256": sha256String(content)},
			}
			var marker string
			if boundary == "temporary creation" {
				marker = installSignalAfterCommand(t, "mktemp", 1, ".alpineform-component.", "contains")
			} else {
				marker = installSignalAfterCommand(t, "mv", 3, installPath, "exact")
			}
			observed, err := (Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}).Apply(context.Background(), engine.Step{Node: node})
			assertSignalBoundaryTriggered(t, marker)
			if boundary == "temporary creation" {
				if err == nil {
					t.Fatal("signaled install temporary creation unexpectedly succeeded")
				}
				if data, readErr := os.ReadFile(installPath); readErr != nil || string(data) != "prior-valid-content" {
					t.Fatalf("prior install after signal = %q, %v", data, readErr)
				}
			} else if err != nil || !observed.Exists || observed.Values["content_verified"] != true {
				t.Fatalf("install commit boundary = %#v, %v", observed, err)
			}
			if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(installPath), ".alpineform-component.*")); len(matches) != 0 {
				t.Fatalf("install signal left temporary files: %#v", matches)
			}
		})
	}
}

func TestProtectedInstallRejectsUnverifiedPostApplyObservation(t *testing.T) {
	digest := strings.Repeat("a", 64)
	node := graph.Node{Kind: "component_binary", Sensitive: true, Desired: map[string]any{
		"path": "/usr/local/bin/tool", "cache_path": "/var/cache/alpineform/components/tool/protected/any/artifact",
		"owner": "root", "group": "root", "mode": "0755", "content_verified": true, "content_sha256_sensitive": true,
	}, Payload: map[string]any{"content_sha256": digest}}
	runner := &commandRunner{outputs: map[string][]byte{
		"inspect.component_binary": []byte("file\nroot\n0\nroot\n0\n755\nunverified\n"),
	}}
	if _, err := applyComponentInstall(context.Background(), runner, node); err == nil || !strings.Contains(err.Error(), "verification failed after apply") {
		t.Fatalf("unverified protected install error = %v", err)
	}
}

func TestProtectedPriorMigrationPathsAreStrictlyBounded(t *testing.T) {
	sourceCurrent := "/var/cache/alpineform/components/tool/protected/any/artifact"
	sourcePrior := "/var/cache/alpineform/components/tool/" + strings.Repeat("a", 64) + "/artifact"
	if !validProtectedSourceMigrationPaths(sourceCurrent, sourcePrior) {
		t.Fatal("valid protected source migration was rejected")
	}
	for _, prior := range []string{
		"/var/cache/alpineform/components/other/" + strings.Repeat("a", 64) + "/artifact",
		"/var/cache/alpineform/components/tool/not-a-digest/artifact",
		"/tmp/" + strings.Repeat("a", 64) + "/artifact",
	} {
		if validProtectedSourceMigrationPaths(sourceCurrent, prior) {
			t.Fatalf("unsafe protected source migration accepted: %q", prior)
		}
	}

	caCurrent := "/var/lib/alpineform/ca-certificates/tool/protected/any.updated"
	caPrior := "/var/lib/alpineform/ca-certificates/" + strings.Repeat("b", 64) + ".updated"
	if !validProtectedCAMigrationPaths(caCurrent, caPrior) {
		t.Fatal("valid protected CA migration was rejected")
	}
	for _, prior := range []string{
		"/var/lib/alpineform/ca-certificates/tool/" + strings.Repeat("b", 64) + ".updated",
		"/var/lib/alpineform/ca-certificates/not-a-digest.updated",
		"/tmp/" + strings.Repeat("b", 64) + ".updated",
	} {
		if validProtectedCAMigrationPaths(caCurrent, prior) {
			t.Fatalf("unsafe protected CA migration accepted: %q", prior)
		}
	}
	if validProtectedCAMigrationPaths("/var/lib/alpineform/ca-certificates/tool/protected/.updated", caPrior) ||
		validProtectedCAMigrationPaths("/var/lib/alpineform/ca-certificates/tool/protected/any", caPrior) {
		t.Fatal("invalid protected CA current marker shape was accepted")
	}
}

func TestProtectedPriorMigrationRejectsArbitraryStatePathsGenerically(t *testing.T) {
	tests := []struct {
		name        string
		node        graph.Node
		payloadName string
		priorPath   string
	}{
		{
			name: "cross-component source",
			node: graph.Node{Kind: "component_artifact_source", Sensitive: true, Desired: map[string]any{
				"path": "/var/cache/alpineform/components/tool/protected/any/artifact",
			}},
			payloadName: "prior_delete_path",
			priorPath:   "/var/cache/alpineform/components/other/" + strings.Repeat("d", 64) + "/artifact",
		},
		{
			name: "arbitrary CA marker",
			node: graph.Node{Kind: "component_ca_certificate", Sensitive: true, Desired: map[string]any{
				"trust_marker": "/var/lib/alpineform/ca-certificates/tool/protected/any.updated",
			}},
			payloadName: "prior_trust_marker",
			priorPath:   "/tmp/" + strings.Repeat("e", 64) + ".updated",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.node.Payload = map[string]any{test.payloadName: test.priorPath}
			runner := &commandRunner{}
			err := migrateProtectedComponentPrior(context.Background(), runner, engine.Step{Node: test.node})
			if err == nil || err.Error() != "protected prior component identity is invalid" || strings.Contains(err.Error(), test.priorPath) || len(runner.commands) != 0 {
				t.Fatalf("unsafe protected migration result: error=%v commands=%#v", err, runner.commands)
			}
		})
	}
}

func TestProtectedPriorMigrationUsesOnlyRedactedStdin(t *testing.T) {
	tests := []struct {
		name          string
		node          graph.Node
		payloadName   string
		priorPath     string
		operationName string
	}{
		{
			name: "source",
			node: graph.Node{Kind: "component_artifact_source", Sensitive: true, Desired: map[string]any{
				"path": "/var/cache/alpineform/components/tool/protected/any/artifact",
			}},
			payloadName: "prior_delete_path", priorPath: "/var/cache/alpineform/components/tool/" + strings.Repeat("a", 64) + "/artifact",
			operationName: "migrate.component_artifact_source_previous",
		},
		{
			name: "CA marker",
			node: graph.Node{Kind: "component_ca_certificate", Sensitive: true, Desired: map[string]any{
				"trust_marker": "/var/lib/alpineform/ca-certificates/tool/protected/any.updated",
			}},
			payloadName: "prior_trust_marker", priorPath: "/var/lib/alpineform/ca-certificates/" + strings.Repeat("b", 64) + ".updated",
			operationName: "migrate.component_ca_certificate_trust_previous",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.node.Payload = map[string]any{test.payloadName: test.priorPath}
			runner := &commandRunner{}
			if err := migrateProtectedComponentPrior(context.Background(), runner, engine.Step{Node: test.node}); err != nil {
				t.Fatal(err)
			}
			if len(runner.commands) != 1 {
				t.Fatalf("migration commands = %#v", runner.commands)
			}
			command := runner.commands[0]
			if command.Name != test.operationName || len(command.Arguments) != 1 || string(command.Stdin) != test.priorPath+"\n" || !command.RedactStdin || !command.RedactOutput || commandContainsOutsideStdin(command, test.priorPath) {
				t.Fatalf("protected prior migration command = %#v", command)
			}
		})
	}
}

func TestProtectedPriorMigrationScriptsAreIdempotentAndCollisionSafe(t *testing.T) {
	t.Run("source", func(t *testing.T) {
		root := t.TempDir()
		prior := filepath.Join(root, "legacy", "artifact")
		current := filepath.Join(root, "protected", "any", "artifact")
		if err := os.MkdirAll(filepath.Dir(prior), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(prior, []byte("prior-cache"), 0600); err != nil {
			t.Fatal(err)
		}
		command := backend.Command{Script: componentProtectedSourcePriorMigrateScript, Arguments: []string{current}, Stdin: []byte(prior + "\n")}
		if _, err := (localRunner{}).Run(context.Background(), command); err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(current); err != nil || string(data) != "prior-cache" {
			t.Fatalf("migrated source cache = %q, %v", data, err)
		}
		if _, err := os.Stat(filepath.Dir(prior)); !os.IsNotExist(err) {
			t.Fatalf("legacy checksum identity parent survived migration: %v", err)
		}
		if data, err := os.ReadFile(filepath.Join(filepath.Dir(current), ".alpineform-owned")); err != nil || string(data) != "alpineform-component-source-v1" {
			t.Fatalf("migrated source ownership = %q, %v", data, err)
		}
		if _, err := (localRunner{}).Run(context.Background(), command); err != nil {
			t.Fatalf("idempotent source migration: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(prior), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(prior, []byte("collision"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := (localRunner{}).Run(context.Background(), command); err == nil {
			t.Fatal("source migration collision unexpectedly succeeded")
		}
		if data, err := os.ReadFile(current); err != nil || string(data) != "prior-cache" {
			t.Fatalf("source collision changed current cache = %q, %v", data, err)
		}
	})

	t.Run("CA marker", func(t *testing.T) {
		root := t.TempDir()
		prior := filepath.Join(root, "legacy", "digest.updated")
		current := filepath.Join(root, "protected", "any.updated")
		if err := os.MkdirAll(filepath.Dir(prior), 0755); err != nil {
			t.Fatal(err)
		}
		rawDigest := strings.Repeat("c", 64)
		if err := os.WriteFile(prior, []byte(rawDigest), 0600); err != nil {
			t.Fatal(err)
		}
		command := backend.Command{Script: componentProtectedCAPriorMigrateScript, Arguments: []string{current}, Stdin: []byte(prior + "\n")}
		if _, err := (localRunner{}).Run(context.Background(), command); err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(current); err != nil || string(data) != "alpineform-protected-ca-stale-v1" || strings.Contains(string(data), rawDigest) {
			t.Fatalf("migrated CA marker = %q, %v", data, err)
		}
		if _, err := (localRunner{}).Run(context.Background(), command); err != nil {
			t.Fatalf("idempotent CA migration: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(prior), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(prior, []byte(rawDigest), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := (localRunner{}).Run(context.Background(), command); err == nil {
			t.Fatal("CA migration collision unexpectedly succeeded")
		}
		if data, err := os.ReadFile(current); err != nil || string(data) != "alpineform-protected-ca-stale-v1" {
			t.Fatalf("CA collision changed current marker = %q, %v", data, err)
		}
	})
}

func commandContainsOutsideStdin(command backend.Command, value string) bool {
	if strings.Contains(command.Script, value) {
		return true
	}
	for _, argument := range command.Arguments {
		if strings.Contains(argument, value) {
			return true
		}
	}
	for key, parameter := range command.Parameters {
		if strings.Contains(key, value) || strings.Contains(parameter, value) {
			return true
		}
	}
	return false
}

func TestComponentArtifactDownloadInstallDriftAndChecksumSafety(t *testing.T) {
	content := "#!/bin/sh\necho alpine-musl\n"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(content))
	}))
	defer server.Close()

	root := t.TempDir()
	cachePath := filepath.Join(root, "cache", "artifact")
	installPath := filepath.Join(root, "bin", "tool")
	digest := sha256String(content)
	sourceNode := graph.Node{Host: "node", Address: "source", Kind: "component_artifact_source", Managed: true, DigestSafe: true, Desired: map[string]any{
		"path": cachePath, "url": server.URL + "/tool", "sha256": digest, "ensure": "present", "delete_behavior": "delete", "delete": map[string]any{"path": cachePath},
	}}
	installNode := graph.Node{Host: "node", Address: "install", Kind: "component_binary", Managed: true, DigestSafe: true, Desired: map[string]any{
		"path": installPath, "owner": strconv.Itoa(os.Getuid()), "group": strconv.Itoa(os.Getgid()), "mode": "0755",
		"content_sha256": digest, "cache_path": cachePath, "artifact_type": "binary", "version": "1", "ensure": "present",
		"delete_behavior": "destroy", "delete": map[string]any{"path": installPath},
	}}
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}

	sourceObserved, err := provider.Apply(context.Background(), engine.Step{Host: "node", Action: engine.ActionCreate, Node: sourceNode})
	if err != nil {
		t.Fatal(err)
	}
	if corestate.Digest(sourceObserved.Values) != corestate.Digest(sourceNode.Desired) {
		t.Fatalf("source observation = %#v", sourceObserved)
	}
	installed, err := provider.Apply(context.Background(), engine.Step{Host: "node", Action: engine.ActionCreate, Node: installNode})
	if err != nil {
		t.Fatal(err)
	}
	if corestate.Digest(installed.Values) != corestate.Digest(installNode.Desired) {
		t.Fatalf("install observation = %#v", installed)
	}
	if err := os.WriteFile(installPath, []byte("tampered"), 0755); err != nil {
		t.Fatal(err)
	}
	drifted, err := provider.Inspect(context.Background(), installNode)
	if err != nil {
		t.Fatal(err)
	}
	if corestate.Digest(drifted.Values) == corestate.Digest(installNode.Desired) {
		t.Fatalf("install drift was not observed: %#v", drifted)
	}

	if err := os.WriteFile(cachePath, []byte("known-good-cache"), 0600); err != nil {
		t.Fatal(err)
	}
	badSource := sourceNode
	badSource.Desired = cloneDesired(sourceNode.Desired)
	badSource.Desired["sha256"] = sha256String("different")
	if _, err := provider.Apply(context.Background(), engine.Step{Host: "node", Action: engine.ActionUpdate, Node: badSource}); err == nil {
		t.Fatal("checksum mismatch unexpectedly succeeded")
	}
	cache, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(cache) != "known-good-cache" {
		t.Fatalf("checksum failure replaced cache with %q", cache)
	}
}

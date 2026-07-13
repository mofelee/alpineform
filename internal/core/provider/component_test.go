package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

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

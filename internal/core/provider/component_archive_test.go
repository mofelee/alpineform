package provider

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

type archiveEntry struct {
	name     string
	typeflag byte
	linkname string
	content  string
}

func writeTestTarGZ(t *testing.T, path string, entries []archiveEntry) string {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zipper := gzip.NewWriter(file)
	writer := tar.NewWriter(zipper)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{Name: entry.name, Typeflag: typeflag, Linkname: entry.linkname, Mode: 0644, Size: int64(len(entry.content))}
		if typeflag != tar.TypeReg {
			header.Size = 0
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if typeflag == tar.TypeReg {
			if _, err := writer.Write([]byte(entry.content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipper.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256String(string(data))
}

func testArchiveNode(cachePath, installPath, digest string, strip int) graph.Node {
	return graph.Node{Host: "node", Address: "archive", Kind: "component_archive", Managed: true, DigestSafe: true, Desired: map[string]any{
		"path": installPath, "owner": strconv.Itoa(os.Getuid()), "group": strconv.Itoa(os.Getgid()), "mode": "0755",
		"content_sha256": digest, "cache_path": cachePath, "artifact_type": "archive", "version": "1",
		"extract_format": "tar.gz", "strip_components": strip, "ensure": "present", "delete_behavior": "destroy",
		"delete": map[string]any{"path": installPath},
	}}
}

func testProtectedArchiveNode(cachePath, installPath, digest string, strip int) graph.Node {
	node := testArchiveNode(cachePath, installPath, "", strip)
	node.Sensitive = true
	delete(node.Desired, "content_sha256")
	node.Desired["content_verified"] = true
	node.Desired["content_sha256_sensitive"] = true
	node.Desired["tree_integrity"] = "clean"
	node.Payload = map[string]any{"content_sha256": digest}
	return node
}

func TestComponentArchiveAtomicInstallAndDrift(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "bundle.tar.gz")
	digest := writeTestTarGZ(t, cache, []archiveEntry{
		{name: "bundle/bin/tool", content: "tool-v1"},
		{name: "bundle/etc/config", content: "enabled=true\n"},
	})
	target := filepath.Join(root, "bundle")
	node := testArchiveNode(cache, target, digest, 1)
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}
	observed, err := provider.Apply(context.Background(), engine.Step{Host: "node", Action: engine.ActionCreate, Node: node})
	if err != nil {
		t.Fatal(err)
	}
	if corestate.Digest(observed.Values) != corestate.Digest(node.Desired) {
		t.Fatalf("archive observation = %#v", observed)
	}
	if data, err := os.ReadFile(filepath.Join(target, "bin", "tool")); err != nil || string(data) != "tool-v1" {
		t.Fatalf("installed tool = %q, %v", data, err)
	}
	if err := os.WriteFile(filepath.Join(target, "bin", "tool"), []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	drifted, err := provider.Inspect(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	if corestate.Digest(drifted.Values) == corestate.Digest(node.Desired) || drifted.Values["tree_integrity"] != "drift" {
		t.Fatalf("archive drift = %#v", drifted)
	}
}

func TestComponentArchiveRejectsUnsafeInputsWithoutReplacingTarget(t *testing.T) {
	tests := []struct {
		name    string
		entries []archiveEntry
		strip   int
	}{
		{name: "traversal", entries: []archiveEntry{{name: "../escape", content: "bad"}}},
		{name: "absolute", entries: []archiveEntry{{name: "/absolute", content: "bad"}}},
		{name: "symlink", entries: []archiveEntry{{name: "bundle/link", typeflag: tar.TypeSymlink, linkname: "../../outside"}}},
		{name: "stripped collision", entries: []archiveEntry{{name: "one/tool", content: "one"}, {name: "two/tool", content: "two"}}, strip: 1},
		{name: "missing product", entries: []archiveEntry{{name: "bundle", typeflag: tar.TypeDir}}, strip: 1},
		{name: "reserved artifact marker", entries: []archiveEntry{{name: "bundle/.alpineform-artifact.sha256", content: "forged"}}, strip: 1},
		{name: "reserved manifest", entries: []archiveEntry{{name: "bundle/.alpineform-manifest.sha256", content: "forged"}}, strip: 1},
		{name: "nested reserved artifact marker", entries: []archiveEntry{{name: "bundle/sub/.alpineform-artifact.sha256", content: "forged"}}, strip: 1},
		{name: "nested reserved manifest", entries: []archiveEntry{{name: "bundle/sub/.alpineform-manifest.sha256", content: "forged"}}, strip: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			cache := filepath.Join(root, "unsafe.tar.gz")
			digest := writeTestTarGZ(t, cache, test.entries)
			target := filepath.Join(root, "target")
			if err := os.Mkdir(target, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			node := testArchiveNode(cache, target, digest, test.strip)
			provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}
			if _, err := provider.Apply(context.Background(), engine.Step{Host: "node", Action: engine.ActionUpdate, Node: node}); err == nil {
				t.Fatal("unsafe archive unexpectedly succeeded")
			}
			data, err := os.ReadFile(filepath.Join(target, "sentinel"))
			if err != nil || string(data) != "keep" {
				t.Fatalf("failed extraction replaced target: data=%q error=%v", data, err)
			}
			if _, err := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
				t.Fatalf("traversal created an escaped file: %v", err)
			}
		})
	}
}

func TestProtectedComponentArchiveMarkerDriftChecksumRollbackAndRetry(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "bundle.tar.gz")
	target := filepath.Join(root, "bundle")
	digest := writeTestTarGZ(t, cache, []archiveEntry{{name: "bundle/bin/tool", content: "tool-v1"}})
	node := testProtectedArchiveNode(cache, target, digest, 1)
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}

	observed, err := provider.Apply(context.Background(), engine.Step{Host: "node", Action: engine.ActionCreate, Node: node})
	if err != nil || !observed.Exists || !observed.Protected || observed.Values["content_verified"] != true || observed.Values["tree_integrity"] != "clean" {
		t.Fatalf("protected archive apply = %#v, %v", observed, err)
	}
	markerPath := filepath.Join(target, ".alpineform-artifact.sha256")
	marker, err := os.ReadFile(markerPath)
	if err != nil || string(marker) != componentProtectedArchiveMarkerValue || strings.Contains(string(marker), digest) {
		t.Fatalf("protected archive marker = %q, %v", marker, err)
	}
	noOp, err := provider.Inspect(context.Background(), node)
	if err != nil || !noOp.Exists || noOp.Values["content_verified"] != true || noOp.Values["tree_integrity"] != "clean" {
		t.Fatalf("protected archive no-op inspection = %#v, %v", noOp, err)
	}

	toolPath := filepath.Join(target, "bin", "tool")
	if err := os.WriteFile(toolPath, []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	drifted, err := provider.Inspect(context.Background(), node)
	if err != nil || drifted.Values["tree_integrity"] != "drift" || drifted.Values["content_verified"] != true {
		t.Fatalf("protected archive drift = %#v, %v", drifted, err)
	}
	if _, err := provider.Apply(context.Background(), engine.Step{Host: "node", Action: engine.ActionUpdate, Node: node}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(toolPath); err != nil || string(data) != "tool-v1" {
		t.Fatalf("repaired protected archive = %q, %v", data, err)
	}

	nextCache := filepath.Join(root, "bundle-v2.tar.gz")
	digestV2 := writeTestTarGZ(t, nextCache, []archiveEntry{{name: "bundle/bin/tool", content: "tool-v2"}})
	node.Payload["content_sha256"] = digestV2
	if err := os.Remove(cache); err != nil {
		t.Fatal(err)
	}
	missingCache, err := provider.Inspect(context.Background(), node)
	if err != nil || !missingCache.Exists || missingCache.Values["content_verified"] != false || missingCache.Values["tree_integrity"] != "clean" {
		t.Fatalf("protected archive after cache loss and checksum change = %#v, %v", missingCache, err)
	}
	if err := os.Rename(nextCache, cache); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Apply(context.Background(), engine.Step{Host: "node", Action: engine.ActionUpdate, Node: node}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(toolPath); err != nil || string(data) != "tool-v2" {
		t.Fatalf("rotated protected archive = %q, %v", data, err)
	}
	marker, err = os.ReadFile(markerPath)
	if err != nil || string(marker) != componentProtectedArchiveMarkerValue || strings.Contains(string(marker), digestV2) {
		t.Fatalf("rotated protected archive marker = %q, %v", marker, err)
	}

	unsafeDigest := writeTestTarGZ(t, cache, []archiveEntry{{name: "../escape", content: "bad"}})
	node.Payload["content_sha256"] = unsafeDigest
	if _, err := provider.Apply(context.Background(), engine.Step{Host: "node", Action: engine.ActionUpdate, Node: node}); err == nil {
		t.Fatal("unsafe protected archive unexpectedly succeeded")
	}
	if data, err := os.ReadFile(toolPath); err != nil || string(data) != "tool-v2" {
		t.Fatalf("failed protected archive replaced prior tree = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
		t.Fatalf("protected archive escaped target: %v", err)
	}

	digestV3 := writeTestTarGZ(t, cache, []archiveEntry{{name: "bundle/bin/tool", content: "tool-v3"}})
	node.Payload["content_sha256"] = digestV3
	retried, err := provider.Apply(context.Background(), engine.Step{Host: "node", Action: engine.ActionUpdate, Node: node})
	if err != nil || retried.Values["content_verified"] != true || retried.Values["tree_integrity"] != "clean" {
		t.Fatalf("protected archive retry = %#v, %v", retried, err)
	}
	if data, err := os.ReadFile(toolPath); err != nil || string(data) != "tool-v3" {
		t.Fatalf("retried protected archive = %q, %v", data, err)
	}
	if matches, _ := filepath.Glob(filepath.Join(root, ".alpineform-archive-*")); len(matches) != 0 {
		t.Fatalf("protected archive temporary paths survived: %#v", matches)
	}
}

func TestProtectedComponentArchiveChecksumUsesOnlyRedactedStdin(t *testing.T) {
	digest := strings.Repeat("a", 64)
	node := testProtectedArchiveNode("/var/cache/alpineform/components/tool/protected/any/artifact", "/opt/tool", digest, 1)
	runner := &commandRunner{outputs: map[string][]byte{
		"inspect.component_archive": []byte("directory\nroot\n0\nroot\n0\n755\nverified\nclean\n"),
	}}
	observed, err := applyComponentArchive(context.Background(), runner, node)
	if err != nil || !observed.Exists || len(runner.commands) != 2 {
		t.Fatalf("protected archive transport = %#v, commands=%#v, error=%v", observed, runner.commands, err)
	}
	for _, command := range runner.commands {
		if !command.RedactStdin || !command.RedactOutput || string(command.Stdin) != digest+"\n" || commandContainsOutsideStdin(command, digest) {
			t.Fatalf("protected archive command leaked checksum: %#v", command)
		}
	}
}

func TestProtectedComponentArchiveSignalBoundaries(t *testing.T) {
	for _, boundary := range []string{"work creation", "prior rename", "replacement commit"} {
		t.Run(boundary, func(t *testing.T) {
			root := t.TempDir()
			cache := filepath.Join(root, "bundle.tar.gz")
			target := filepath.Join(root, "bundle")
			digest := writeTestTarGZ(t, cache, []archiveEntry{{name: "bundle/bin/tool", content: "new-tool"}})
			if err := os.Mkdir(target, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("prior-tree"), 0600); err != nil {
				t.Fatal(err)
			}
			var marker string
			switch boundary {
			case "work creation":
				marker = installSignalAfterCommand(t, "mktemp", 2, ".alpineform-archive-work.", "contains")
			case "prior rename":
				marker = installSignalAfterCommand(t, "mv", 1, target, "exact")
			case "replacement commit":
				marker = installSignalAfterCommand(t, "mv", 2, target, "exact")
			}
			provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}
			observed, err := provider.Apply(context.Background(), engine.Step{Node: testProtectedArchiveNode(cache, target, digest, 1)})
			assertSignalBoundaryTriggered(t, marker)
			if boundary == "replacement commit" {
				if err != nil || !observed.Exists || observed.Values["content_verified"] != true || observed.Values["tree_integrity"] != "clean" {
					t.Fatalf("archive commit boundary = %#v, %v", observed, err)
				}
				if data, readErr := os.ReadFile(filepath.Join(target, "bin", "tool")); readErr != nil || string(data) != "new-tool" {
					t.Fatalf("committed archive tree = %q, %v", data, readErr)
				}
			} else {
				if err == nil {
					t.Fatalf("signaled archive %s unexpectedly succeeded", boundary)
				}
				if data, readErr := os.ReadFile(filepath.Join(target, "sentinel")); readErr != nil || string(data) != "prior-tree" {
					t.Fatalf("archive prior tree after %s = %q, %v", boundary, data, readErr)
				}
			}
			if matches, _ := filepath.Glob(filepath.Join(root, ".alpineform-archive-*")); len(matches) != 0 {
				t.Fatalf("archive %s left transaction paths: %#v", boundary, matches)
			}
		})
	}
}

func TestProtectedComponentArchiveCleanupFailuresRollbackOrCommitCleanly(t *testing.T) {
	for _, test := range []struct {
		name      string
		match     string
		afterReal bool
		wantError bool
	}{
		{name: "work cleanup fails before removal", match: ".alpineform-archive-work.", wantError: true},
		{name: "prior cleanup fails before removal", match: ".alpineform-archive-old.", wantError: true},
		{name: "prior cleanup reports failure after removal", match: ".alpineform-archive-old.", afterReal: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			cache := filepath.Join(root, "bundle.tar.gz")
			target := filepath.Join(root, "bundle")
			digest := writeTestTarGZ(t, cache, []archiveEntry{{name: "bundle/tool", content: "new"}})
			if err := os.Mkdir(target, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, "tool"), []byte("prior"), 0600); err != nil {
				t.Fatal(err)
			}
			failureMarker := installCommandFailure(t, "rm", test.match, "contains", test.afterReal)
			provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}
			observed, err := provider.Apply(context.Background(), engine.Step{Node: testProtectedArchiveNode(cache, target, digest, 1)})
			if _, markerErr := os.Stat(failureMarker); markerErr != nil {
				t.Fatalf("cleanup failure was not injected: %v", markerErr)
			}
			if test.wantError {
				if err == nil {
					t.Fatal("archive cleanup failure unexpectedly succeeded")
				}
				if data, readErr := os.ReadFile(filepath.Join(target, "tool")); readErr != nil || string(data) != "prior" {
					t.Fatalf("archive cleanup failure did not restore prior = %q, %v", data, readErr)
				}
			} else {
				if err != nil || !observed.Exists || observed.Values["content_verified"] != true {
					t.Fatalf("archive removed-backup commit = %#v, %v", observed, err)
				}
				if data, readErr := os.ReadFile(filepath.Join(target, "tool")); readErr != nil || string(data) != "new" {
					t.Fatalf("archive removed-backup content = %q, %v", data, readErr)
				}
			}
			if matches, _ := filepath.Glob(filepath.Join(root, ".alpineform-archive-*")); len(matches) != 0 {
				t.Fatalf("archive cleanup failure left transaction paths: %#v", matches)
			}
		})
	}
}

func TestProtectedComponentArchiveRejectsNonConvergedPostApplyObservation(t *testing.T) {
	for _, output := range []string{
		"directory\nroot\n0\nroot\n0\n755\nunverified\nclean\n",
		"directory\nroot\n0\nroot\n0\n755\nverified\ndrift\n",
	} {
		node := testProtectedArchiveNode("/var/cache/alpineform/components/tool/protected/any/artifact", "/opt/tool", strings.Repeat("a", 64), 1)
		runner := &commandRunner{outputs: map[string][]byte{"inspect.component_archive": []byte(output)}}
		if _, err := applyComponentArchive(context.Background(), runner, node); err == nil || !strings.Contains(err.Error(), "verification failed after apply") {
			t.Fatalf("non-converged protected archive error = %v, output=%q", err, output)
		}
	}
}

func TestProtectedComponentArchiveRejectsReservedMetadataBeforeReplacement(t *testing.T) {
	for _, reserved := range []string{
		".alpineform-artifact.sha256", ".alpineform-manifest.sha256",
		"sub/.alpineform-artifact.sha256", "sub/.alpineform-manifest.sha256",
	} {
		t.Run(reserved, func(t *testing.T) {
			root := t.TempDir()
			cache := filepath.Join(root, "bundle.tar.gz")
			target := filepath.Join(root, "target")
			provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}
			goodDigest := writeTestTarGZ(t, cache, []archiveEntry{{name: "bundle/tool", content: "prior"}})
			node := testProtectedArchiveNode(cache, target, goodDigest, 1)
			if _, err := provider.Apply(context.Background(), engine.Step{Node: node}); err != nil {
				t.Fatal(err)
			}
			digest := writeTestTarGZ(t, cache, []archiveEntry{{name: "bundle/" + reserved, content: "forged"}})
			node.Payload["content_sha256"] = digest
			observed, err := provider.Inspect(context.Background(), node)
			if err != nil || observed.Values["content_verified"] != false || observed.Values["tree_integrity"] != "clean" {
				t.Fatalf("reserved metadata inspection = %#v, %v", observed, err)
			}
			if _, err := provider.Apply(context.Background(), engine.Step{Node: node}); err == nil {
				t.Fatal("protected archive with reserved metadata unexpectedly succeeded")
			}
			if data, err := os.ReadFile(filepath.Join(target, "tool")); err != nil || string(data) != "prior" {
				t.Fatalf("reserved metadata replaced prior tree = %q, %v", data, err)
			}
			if matches, _ := filepath.Glob(filepath.Join(root, ".alpineform-archive-*")); len(matches) != 0 {
				t.Fatalf("reserved metadata left transaction paths: %#v", matches)
			}
		})
	}
}

func TestCACertificateRefreshIsPartOfSuccessfulApply(t *testing.T) {
	digest := sha256String("certificate")
	node := graph.Node{Host: "node", Address: "ca", Kind: "component_ca_certificate", Managed: true, Desired: map[string]any{
		"path": "/usr/local/share/ca-certificates/example.crt", "owner": "root", "group": "root", "mode": "0644",
		"content_sha256": digest, "cache_path": "/var/cache/alpineform/ca", "artifact_type": "ca_certificate", "version": "1",
		"ensure": "present", "delete_behavior": "destroy", "trust_marker": "/var/lib/alpineform/ca-certificates/" + digest + ".updated", "trust_updated": true,
	}}
	runner := &commandRunner{outputs: map[string][]byte{
		"inspect.component_ca_certificate":       []byte("file\nroot\n0\nroot\n0\n644\n" + digest + "\n"),
		"inspect.component_ca_certificate_trust": []byte("updated\n"),
	}}
	if _, err := applyComponentCACertificate(context.Background(), runner, engine.Step{Node: node}); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(runner.commands))
	for _, command := range runner.commands {
		names = append(names, command.Name)
	}
	want := []string{"apply.component_ca_certificate", "inspect.component_ca_certificate", "apply.component_ca_certificate_trust", "inspect.component_ca_certificate", "inspect.component_ca_certificate_trust"}
	if len(names) != len(want) {
		t.Fatalf("CA commands = %#v", names)
	}
	for index := range want {
		if names[index] != want[index] {
			t.Fatalf("CA commands = %#v, want %#v", names, want)
		}
	}
}

func TestProtectedCACertificateRefreshFailureRollsBackAndRetries(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache", "root.crt")
	target := filepath.Join(root, "certificates", "root.crt")
	marker := filepath.Join(root, "markers", "protected", "root.updated")
	for _, parent := range []string{filepath.Dir(cache), filepath.Dir(target), filepath.Dir(marker)} {
		if err := os.MkdirAll(parent, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(cache, []byte("new-certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("prior-certificate"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(componentProtectedCAMarkerValue), 0600); err != nil {
		t.Fatal(err)
	}
	fail, log := installFakeCARefresh(t, marker)
	if err := os.WriteFile(fail, []byte("fail"), 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256String("new-certificate")
	node := protectedCANode(cache, target, marker, digest)
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}

	if _, err := provider.Apply(context.Background(), engine.Step{Host: "node", Action: engine.ActionUpdate, Node: node}); err == nil {
		t.Fatal("failed CA refresh unexpectedly succeeded")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "prior-certificate" {
		t.Fatalf("certificate after failed refresh = %q, %v", data, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("CA marker survived failed refresh: %v", err)
	}
	if err := os.Remove(fail); err != nil {
		t.Fatal(err)
	}
	observed, err := provider.Apply(context.Background(), engine.Step{Host: "node", Action: engine.ActionUpdate, Node: node})
	if err != nil || !observed.Exists || !observed.Protected || observed.Values["content_verified"] != true || observed.Values["trust_updated"] != true {
		t.Fatalf("protected CA retry = %#v, %v", observed, err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "new-certificate" {
		t.Fatalf("certificate after retry = %q, %v", data, err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != componentProtectedCAMarkerValue || strings.Contains(string(data), digest) {
		t.Fatalf("protected CA marker = %q, %v", data, err)
	}
	assertCARefreshSawMarkerAbsent(t, log)
}

func TestProtectedCACertificateSignalCancellationRollsBackExactlyOnce(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache", "root.crt")
	target := filepath.Join(root, "certificates", "root.crt")
	marker := filepath.Join(root, "markers", "protected", "root.updated")
	for _, parent := range []string{filepath.Dir(cache), filepath.Dir(target), filepath.Dir(marker)} {
		if err := os.MkdirAll(parent, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(cache, []byte("new-certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("prior-certificate"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(componentProtectedCAMarkerValue), 0600); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0755); err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(root, "refresh.started")
	once := filepath.Join(root, "refresh.once")
	fakeRefresh := `#!/bin/sh
set -eu
if [ ! -e "$APF_TEST_CA_ONCE" ]; then
  : >"$APF_TEST_CA_ONCE"
  : >"$APF_TEST_CA_STARTED"
  trap 'exit 143' HUP INT TERM
  while :; do sleep 1; done
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "update-ca-certificates"), []byte(fakeRefresh), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APF_TEST_CA_STARTED", started)
	t.Setenv("APF_TEST_CA_ONCE", once)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	digest := sha256String("new-certificate")
	node := protectedCANode(cache, target, marker, digest)
	arguments := []string{"-c", componentProtectedCAApplyScript, "alpineform",
		cache, target, stringValue(node.Desired, "owner"), stringValue(node.Desired, "group"), stringValue(node.Desired, "mode"), marker, componentProtectedCAMarkerValue,
	}
	command := exec.Command("sh", arguments...)
	command.Stdin = bytes.NewReader([]byte(digest + "\n"))
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			_, _ = command.Process.Wait()
			t.Fatal("fake CA refresh did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "new-certificate" {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		_, _ = command.Process.Wait()
		t.Fatalf("CA transaction had not installed candidate before cancellation: %q, %v", data, err)
	}
	if err := syscall.Kill(-command.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("signaled protected CA transaction unexpectedly succeeded")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "prior-certificate" {
		t.Fatalf("certificate after signal cancellation = %q, %v", data, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("CA marker survived signal cancellation: %v", err)
	}
	for _, pattern := range []string{
		filepath.Join(filepath.Dir(target), ".alpineform-ca-candidate.*"),
		filepath.Join(filepath.Dir(target), ".alpineform-ca-prior.*"),
		filepath.Join(filepath.Dir(marker), ".alpineform-ca.*"),
	} {
		if matches, _ := filepath.Glob(pattern); len(matches) != 0 {
			t.Fatalf("CA signal cancellation left temporary files: %#v", matches)
		}
	}
}

func TestProtectedCACertificateExactSignalBoundaries(t *testing.T) {
	for _, boundary := range []string{"candidate creation", "prior rename", "candidate install", "marker commit"} {
		t.Run(boundary, func(t *testing.T) {
			root := t.TempDir()
			cache := filepath.Join(root, "cache", "root.crt")
			target := filepath.Join(root, "certificates", "root.crt")
			markerPath := filepath.Join(root, "markers", "protected", "root.updated")
			for _, parent := range []string{filepath.Dir(cache), filepath.Dir(target), filepath.Dir(markerPath)} {
				if err := os.MkdirAll(parent, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(cache, []byte("new-certificate"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("prior-certificate"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(markerPath, []byte(componentProtectedCAMarkerValue), 0600); err != nil {
				t.Fatal(err)
			}
			_, refreshLog := installFakeCARefresh(t, markerPath)
			var signalMarker string
			switch boundary {
			case "candidate creation":
				signalMarker = installSignalAfterCommand(t, "mktemp", 1, ".alpineform-ca-candidate.", "contains")
			case "prior rename":
				signalMarker = installSignalAfterCommand(t, "mv", 1, target, "exact")
			case "candidate install":
				signalMarker = installSignalAfterCommand(t, "mv", 2, target, "exact")
			case "marker commit":
				signalMarker = installSignalAfterCommand(t, "mv", 3, markerPath, "exact")
			}
			node := protectedCANode(cache, target, markerPath, sha256String("new-certificate"))
			provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}
			observed, err := provider.Apply(context.Background(), engine.Step{Node: node})
			assertSignalBoundaryTriggered(t, signalMarker)
			if boundary == "marker commit" {
				if err != nil || !observed.Exists || observed.Values["content_verified"] != true || observed.Values["trust_updated"] != true {
					t.Fatalf("CA marker commit boundary = %#v, %v", observed, err)
				}
				if data, readErr := os.ReadFile(target); readErr != nil || string(data) != "new-certificate" {
					t.Fatalf("CA committed certificate = %q, %v", data, readErr)
				}
				if data, readErr := os.ReadFile(markerPath); readErr != nil || string(data) != componentProtectedCAMarkerValue {
					t.Fatalf("CA committed marker = %q, %v", data, readErr)
				}
				assertCARefreshSawMarkerAbsent(t, refreshLog)
			} else {
				if err == nil {
					t.Fatalf("signaled CA %s unexpectedly succeeded", boundary)
				}
				if data, readErr := os.ReadFile(target); readErr != nil || string(data) != "prior-certificate" {
					t.Fatalf("CA prior after %s = %q, %v", boundary, data, readErr)
				}
				if data, readErr := os.ReadFile(markerPath); readErr != nil || string(data) != componentProtectedCAMarkerValue {
					t.Fatalf("CA marker after %s = %q, %v", boundary, data, readErr)
				}
			}
			for _, pattern := range []string{
				filepath.Join(filepath.Dir(target), ".alpineform-ca-candidate.*"),
				filepath.Join(filepath.Dir(target), ".alpineform-ca-prior.*"),
				filepath.Join(filepath.Dir(markerPath), ".alpineform-ca.*"),
			} {
				if matches, _ := filepath.Glob(pattern); len(matches) != 0 {
					t.Fatalf("CA %s left transaction files: %#v", boundary, matches)
				}
			}
		})
	}
}

func TestProtectedCACertificateCleanupFailuresPreserveTransactionInvariant(t *testing.T) {
	t.Run("marker temporary cleanup continues through rollback", func(t *testing.T) {
		root := t.TempDir()
		cache := filepath.Join(root, "cache", "root.crt")
		target := filepath.Join(root, "certificates", "root.crt")
		markerPath := filepath.Join(root, "markers", "protected", "root.updated")
		for _, parent := range []string{filepath.Dir(cache), filepath.Dir(target), filepath.Dir(markerPath)} {
			if err := os.MkdirAll(parent, 0755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(cache, []byte("new-certificate"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("prior-certificate"), 0644); err != nil {
			t.Fatal(err)
		}
		installFakeCARefresh(t, markerPath)
		temporaryPrefix := filepath.Join(filepath.Dir(markerPath), ".alpineform-ca.")
		signalMarker := installSignalAfterCommand(t, "mktemp", 1, temporaryPrefix, "contains")
		failureMarker := installCommandFailure(t, "rm", temporaryPrefix, "contains", true)
		node := protectedCANode(cache, target, markerPath, sha256String("new-certificate"))
		provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}
		if _, err := provider.Apply(context.Background(), engine.Step{Node: node}); err == nil {
			t.Fatal("CA marker temporary cleanup failure unexpectedly succeeded")
		}
		assertSignalBoundaryTriggered(t, signalMarker)
		if _, err := os.Stat(failureMarker); err != nil {
			t.Fatalf("CA cleanup failure was not injected: %v", err)
		}
		if data, err := os.ReadFile(target); err != nil || string(data) != "prior-certificate" {
			t.Fatalf("CA cleanup failure did not restore prior = %q, %v", data, err)
		}
		if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
			t.Fatalf("CA cleanup failure left a trust marker: %v", err)
		}
		for _, pattern := range []string{
			filepath.Join(filepath.Dir(target), ".alpineform-ca-candidate.*"),
			filepath.Join(filepath.Dir(target), ".alpineform-ca-prior.*"),
			filepath.Join(filepath.Dir(markerPath), ".alpineform-ca.*"),
		} {
			if matches, _ := filepath.Glob(pattern); len(matches) != 0 {
				t.Fatalf("CA cleanup failure left transaction files: %#v", matches)
			}
		}
	})

	for _, afterReal := range []bool{false, true} {
		name := "backup removal fails before removal"
		if afterReal {
			name = "backup removal reports failure after removal"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			cache := filepath.Join(root, "cache", "root.crt")
			target := filepath.Join(root, "certificates", "root.crt")
			markerPath := filepath.Join(root, "markers", "protected", "root.updated")
			for _, parent := range []string{filepath.Dir(cache), filepath.Dir(target), filepath.Dir(markerPath)} {
				if err := os.MkdirAll(parent, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(cache, []byte("new-certificate"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("prior-certificate"), 0644); err != nil {
				t.Fatal(err)
			}
			installFakeCARefresh(t, markerPath)
			failureMarker := installCommandFailure(t, "rm", ".alpineform-ca-prior.", "contains", afterReal)
			node := protectedCANode(cache, target, markerPath, sha256String("new-certificate"))
			provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}
			observed, err := provider.Apply(context.Background(), engine.Step{Node: node})
			if _, markerErr := os.Stat(failureMarker); markerErr != nil {
				t.Fatalf("CA backup cleanup failure was not injected: %v", markerErr)
			}
			if afterReal {
				if err != nil || !observed.Exists || observed.Values["content_verified"] != true || observed.Values["trust_updated"] != true {
					t.Fatalf("CA removed-backup commit = %#v, %v", observed, err)
				}
				if data, readErr := os.ReadFile(target); readErr != nil || string(data) != "new-certificate" {
					t.Fatalf("CA removed-backup content = %q, %v", data, readErr)
				}
			} else {
				if err == nil {
					t.Fatal("CA backup cleanup failure unexpectedly succeeded")
				}
				if data, readErr := os.ReadFile(target); readErr != nil || string(data) != "prior-certificate" {
					t.Fatalf("CA backup cleanup failure did not restore prior = %q, %v", data, readErr)
				}
			}
			for _, pattern := range []string{
				filepath.Join(filepath.Dir(target), ".alpineform-ca-candidate.*"),
				filepath.Join(filepath.Dir(target), ".alpineform-ca-prior.*"),
				filepath.Join(filepath.Dir(markerPath), ".alpineform-ca.*"),
			} {
				if matches, _ := filepath.Glob(pattern); len(matches) != 0 {
					t.Fatalf("CA backup cleanup left transaction files: %#v", matches)
				}
			}
		})
	}
}

func TestProtectedCAMarkerParentOwnershipLifecycle(t *testing.T) {
	for _, preexisting := range []bool{false, true} {
		name := "created"
		if preexisting {
			name = "preexisting"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			cache := filepath.Join(root, "cache", "root.crt")
			target := filepath.Join(root, "certificates", "root.crt")
			markerPath := filepath.Join(root, "markers", "protected", "root.updated")
			for _, parent := range []string{filepath.Dir(cache), filepath.Dir(target)} {
				if err := os.MkdirAll(parent, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if preexisting {
				if err := os.MkdirAll(filepath.Dir(markerPath), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(cache, []byte("certificate"), 0600); err != nil {
				t.Fatal(err)
			}
			installFakeCARefresh(t, markerPath)
			node := protectedCANode(cache, target, markerPath, sha256String("certificate"))
			provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}
			if _, err := provider.Apply(context.Background(), engine.Step{Node: node}); err != nil {
				t.Fatal(err)
			}
			ownershipPath := filepath.Join(filepath.Dir(markerPath), ".alpineform-owned")
			if preexisting {
				if _, err := os.Stat(ownershipPath); !os.IsNotExist(err) {
					t.Fatalf("preexisting CA parent gained ownership marker: %v", err)
				}
			} else if data, err := os.ReadFile(ownershipPath); err != nil || string(data) != "alpineform-component-ca-marker-v1" {
				t.Fatalf("CA marker parent ownership = %q, %v", data, err)
			}
			if err := provider.Delete(context.Background(), engine.Step{Node: node}); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(filepath.Dir(markerPath))
			if preexisting && err != nil {
				t.Fatalf("preexisting CA marker parent was removed: %v", err)
			}
			if !preexisting && !os.IsNotExist(err) {
				t.Fatalf("owned CA marker parent survived teardown: %v", err)
			}
		})
	}
}

func TestProtectedCACertificatePriorMarkerCleanupUsesRedactedStdin(t *testing.T) {
	digest := strings.Repeat("a", 64)
	priorMarker := "/var/lib/alpineform/ca-certificates/" + strings.Repeat("b", 64) + ".updated"
	node := protectedCANode(
		"/var/cache/alpineform/components/ca/protected/any/artifact",
		"/usr/local/share/ca-certificates/root.crt",
		"/var/lib/alpineform/ca-certificates/ca/protected/any.updated",
		digest,
	)
	node.Payload["prior_trust_marker"] = priorMarker
	runner := &commandRunner{outputs: map[string][]byte{
		"inspect.component_ca_certificate":       []byte("file\nroot\n0\nroot\n0\n644\nverified\n"),
		"inspect.component_ca_certificate_trust": []byte("updated\n"),
	}}
	observed, err := applyComponentCACertificate(context.Background(), runner, engine.Step{Node: node})
	if err != nil || !observed.Exists || len(runner.commands) != 4 {
		t.Fatalf("protected CA prior cleanup = %#v, commands=%#v, error=%v", observed, runner.commands, err)
	}
	cleanup := runner.commands[3]
	if cleanup.Name != "cleanup.component_ca_certificate_trust_previous" || len(cleanup.Arguments) != 0 || string(cleanup.Stdin) != priorMarker+"\n" || !cleanup.RedactStdin || !cleanup.RedactOutput || commandContainsOutsideStdin(cleanup, priorMarker) {
		t.Fatalf("protected CA prior marker cleanup = %#v", cleanup)
	}
	for _, command := range runner.commands[:3] {
		if commandContainsOutsideStdin(command, digest) || !command.RedactOutput {
			t.Fatalf("protected CA command leaked checksum: %#v", command)
		}
	}
}

func TestPublicCACertificateCleansPreviousMarkerAfterSuccessfulRefresh(t *testing.T) {
	digest := sha256String("certificate")
	marker := "/var/lib/alpineform/ca-certificates/" + digest + ".updated"
	priorMarker := "/var/lib/alpineform/ca-certificates/" + strings.Repeat("b", 64) + ".updated"
	node := graph.Node{Kind: "component_ca_certificate", Desired: map[string]any{
		"path": "/usr/local/share/ca-certificates/example.crt", "owner": "root", "group": "root", "mode": "0644",
		"content_sha256": digest, "cache_path": "/var/cache/alpineform/ca", "trust_marker": marker, "trust_updated": true,
	}}
	runner := &commandRunner{outputs: map[string][]byte{
		"inspect.component_ca_certificate":       []byte("file\nroot\n0\nroot\n0\n644\n" + digest + "\n"),
		"inspect.component_ca_certificate_trust": []byte("updated\n"),
	}}
	_, err := applyComponentCACertificate(context.Background(), runner, engine.Step{
		Node: node, Prior: &corestate.Resource{Delete: map[string]any{"trust_marker": priorMarker}},
	})
	if err != nil || len(runner.commands) != 6 {
		t.Fatalf("public CA prior cleanup commands = %#v, error=%v", runner.commands, err)
	}
	cleanup := runner.commands[5]
	if cleanup.Name != "cleanup.component_ca_certificate_trust_previous" || len(cleanup.Arguments) != 1 || cleanup.Arguments[0] != priorMarker || cleanup.RedactStdin {
		t.Fatalf("public CA prior marker cleanup = %#v", cleanup)
	}
}

func TestCACertificatePriorMarkerCleanupFailureIsRetryable(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache", "root.crt")
	target := filepath.Join(root, "certificates", "root.crt")
	marker := filepath.Join(root, "markers", "current.updated")
	priorMarker := filepath.Join(root, "prior-marker", "prior.updated")
	for _, parent := range []string{filepath.Dir(cache), filepath.Dir(target), filepath.Dir(priorMarker)} {
		if err := os.MkdirAll(parent, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(cache, []byte("new-certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("prior-certificate"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(priorMarker, []byte("prior-marker"), 0600); err != nil {
		t.Fatal(err)
	}
	_, log := installFakeCARefresh(t, marker)
	digest := sha256String("new-certificate")
	node := graph.Node{Kind: "component_ca_certificate", Desired: map[string]any{
		"path": target, "owner": strconv.Itoa(os.Getuid()), "group": strconv.Itoa(os.Getgid()), "mode": "0644",
		"content_sha256": digest, "cache_path": cache, "trust_marker": marker, "trust_updated": true,
	}}
	step := engine.Step{Node: node, Prior: &corestate.Resource{Delete: map[string]any{"trust_marker": priorMarker}}}
	runner := &failOnceCommandRunner{Runner: localRunner{}, operation: "cleanup.component_ca_certificate_trust_previous"}
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return runner, nil }}
	if _, err := provider.Apply(context.Background(), step); err == nil {
		t.Fatal("injected CA prior marker cleanup failure unexpectedly succeeded")
	}
	for name, path := range map[string]string{"current marker": marker, "prior marker": priorMarker} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s missing after cleanup failure: %v", name, err)
		}
	}
	observed, err := provider.Apply(context.Background(), step)
	if err != nil || !observed.Exists || observed.Values["trust_updated"] != true {
		t.Fatalf("CA prior marker cleanup retry = %#v, %v", observed, err)
	}
	if _, err := os.Stat(priorMarker); !os.IsNotExist(err) {
		t.Fatalf("prior CA marker survived cleanup retry: %v", err)
	}
	if info, err := os.Stat(filepath.Dir(priorMarker)); err != nil || !info.IsDir() {
		t.Fatalf("preexisting prior CA marker parent was removed: %#v, %v", info, err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != digest {
		t.Fatalf("current CA marker after retry = %q, %v", data, err)
	}
	assertCARefreshSawMarkerAbsent(t, log)
}

func TestProtectedCACertificatePriorOnlyDeleteClearsMarkerBeforeRefresh(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "certificates", "root.crt")
	marker := filepath.Join(root, "markers", "protected", "root.updated")
	for _, parent := range []string{filepath.Dir(target), filepath.Dir(marker)} {
		if err := os.MkdirAll(parent, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(target, []byte("certificate"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(componentProtectedCAMarkerValue), 0600); err != nil {
		t.Fatal(err)
	}
	_, log := installFakeCARefresh(t, marker)
	step := engine.Step{Prior: &corestate.Resource{Protected: true, Delete: map[string]any{"path": target, "trust_marker": marker}}}
	if err := deleteComponentCACertificate(context.Background(), localRunner{}, step); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"certificate": target, "marker": marker} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s survived protected prior-only delete: %v", name, err)
		}
	}
	assertCARefreshSawMarkerAbsent(t, log)
}

func protectedCANode(cache, target, marker, digest string) graph.Node {
	return graph.Node{
		Kind: "component_ca_certificate", Sensitive: true,
		Desired: map[string]any{
			"path": target, "owner": strconv.Itoa(os.Getuid()), "group": strconv.Itoa(os.Getgid()), "mode": "0644",
			"cache_path": cache, "content_verified": true, "content_sha256_sensitive": true,
			"trust_marker": marker, "trust_updated": true,
		},
		Payload: map[string]any{"content_sha256": digest},
	}
}

func installFakeCARefresh(t *testing.T, marker string) (string, string) {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
set -eu
if [ -e "$APF_TEST_CA_MARKER" ]; then
  printf 'present\n' >>"$APF_TEST_CA_LOG"
else
  printf 'absent\n' >>"$APF_TEST_CA_LOG"
fi
if [ -e "$APF_TEST_CA_FAIL" ]; then exit 1; fi
`
	if err := os.WriteFile(filepath.Join(bin, "update-ca-certificates"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	fail := filepath.Join(root, "fail")
	log := filepath.Join(root, "refresh.log")
	t.Setenv("APF_TEST_CA_MARKER", marker)
	t.Setenv("APF_TEST_CA_LOG", log)
	t.Setenv("APF_TEST_CA_FAIL", fail)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	return fail, log
}

func assertCARefreshSawMarkerAbsent(t *testing.T, log string) {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(data))
	if len(lines) == 0 {
		t.Fatal("fake CA refresh was not called")
	}
	for _, line := range lines {
		if line != "absent" {
			t.Fatalf("CA refresh observed marker state %q; log=%q", line, data)
		}
	}
}

func installSignalAfterCommand(t *testing.T, name string, argumentIndex int, match, matchMode string) string {
	t.Helper()
	realCommand, err := exec.LookPath(name)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	marker := filepath.Join(bin, "signal.sent")
	script := `#!/bin/sh
set -eu
"$APF_TEST_REAL_COMMAND" "$@"
case "$APF_TEST_SIGNAL_ARGUMENT" in
  1) actual=${1-} ;;
  2) actual=${2-} ;;
  3) actual=${3-} ;;
  *) exit 97 ;;
esac
matched=0
case "$APF_TEST_SIGNAL_MATCH_MODE" in
  exact) [ "$actual" = "$APF_TEST_SIGNAL_MATCH" ] && matched=1 ;;
  contains) case "$actual" in *"$APF_TEST_SIGNAL_MATCH"*) matched=1;; esac ;;
  *) exit 98 ;;
esac
if [ "$matched" = 1 ] && [ ! -e "$APF_TEST_SIGNAL_MARKER" ]; then
  : >"$APF_TEST_SIGNAL_MARKER"
  kill -TERM "$PPID"
fi
`
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APF_TEST_REAL_COMMAND", realCommand)
	t.Setenv("APF_TEST_SIGNAL_ARGUMENT", strconv.Itoa(argumentIndex))
	t.Setenv("APF_TEST_SIGNAL_MATCH", match)
	t.Setenv("APF_TEST_SIGNAL_MATCH_MODE", matchMode)
	t.Setenv("APF_TEST_SIGNAL_MARKER", marker)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	return marker
}

func assertSignalBoundaryTriggered(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("signal boundary was not triggered: %v", err)
	}
}

func installCommandFailure(t *testing.T, name, match, matchMode string, afterReal bool) string {
	t.Helper()
	realCommand, err := exec.LookPath(name)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	marker := filepath.Join(bin, "failure.injected")
	script := `#!/bin/sh
set -eu
matched=0
for actual do
  case "$APF_TEST_FAIL_MATCH_MODE" in
    exact) [ "$actual" = "$APF_TEST_FAIL_MATCH" ] && matched=1 ;;
    contains) case "$actual" in *"$APF_TEST_FAIL_MATCH"*) matched=1;; esac ;;
    *) exit 98 ;;
  esac
done
if [ "$matched" = 1 ] && [ ! -e "$APF_TEST_FAIL_MARKER" ]; then
  : >"$APF_TEST_FAIL_MARKER"
  if [ "$APF_TEST_FAIL_AFTER_REAL" = 1 ]; then "$APF_TEST_FAIL_REAL_COMMAND" "$@"; fi
  exit 99
fi
exec "$APF_TEST_FAIL_REAL_COMMAND" "$@"
`
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APF_TEST_FAIL_REAL_COMMAND", realCommand)
	t.Setenv("APF_TEST_FAIL_MATCH", match)
	t.Setenv("APF_TEST_FAIL_MATCH_MODE", matchMode)
	t.Setenv("APF_TEST_FAIL_MARKER", marker)
	if afterReal {
		t.Setenv("APF_TEST_FAIL_AFTER_REAL", "1")
	} else {
		t.Setenv("APF_TEST_FAIL_AFTER_REAL", "0")
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	return marker
}

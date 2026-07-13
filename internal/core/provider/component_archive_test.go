package provider

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

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
	if _, err := applyComponentCACertificate(context.Background(), runner, node); err != nil {
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

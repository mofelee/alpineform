package provider

import (
	"context"
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

func testDirectoryNode(path string, recursive bool) graph.Node {
	return graph.Node{
		Host:    "node",
		Address: `host.node.directories.directory["` + path + `"]`,
		Kind:    "directory",
		Managed: true,
		Desired: map[string]any{
			"path":             path,
			"owner":            strconv.Itoa(os.Getuid()),
			"group":            strconv.Itoa(os.Getgid()),
			"mode":             "0750",
			"ensure":           "present",
			"recursive_delete": recursive,
			"delete_behavior":  "",
			"delete":           map[string]any{"path": path, "recursive": recursive},
			"prevent_destroy":  false,
		},
		DigestSafe: true,
	}
}

func TestDirectoryProviderCreateObserveAndSafeDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "parent", "managed")
	node := testDirectoryNode(path, false)
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}
	missing, err := provider.Inspect(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	if missing.Exists {
		t.Fatalf("missing directory = %#v", missing)
	}
	observed, err := provider.Apply(context.Background(), engine.Step{Host: "node", Action: engine.ActionCreate, Node: node})
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || corestate.Digest(observed.Values) != corestate.Digest(node.Desired) {
		t.Fatalf("directory observation = %#v", observed)
	}
	if err := os.WriteFile(filepath.Join(path, "child"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	step := engine.Step{Host: "node", Action: engine.ActionDelete, Node: node}
	if err := provider.Delete(context.Background(), step); err == nil {
		t.Fatal("non-recursive delete unexpectedly removed a non-empty directory")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("directory missing after safe delete failure: %v", err)
	}
	node.Desired["recursive_delete"] = true
	if err := provider.Delete(context.Background(), engine.Step{Host: "node", Action: engine.ActionDelete, Node: node}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("recursive delete left directory: %v", err)
	}
}

func TestDirectoryProviderDestroysOrphanWithRecordedPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orphan")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "child"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}
	step := engine.Step{Host: "node", Action: engine.ActionDestroy, Prior: &corestate.Resource{Kind: "directory", Delete: map[string]any{"path": path, "recursive": true}}}
	if err := provider.Delete(context.Background(), step); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("orphan directory remains: %v", err)
	}
}

func TestDirectoryProviderRefusesSymbolicLinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "managed")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	node := testDirectoryNode(link, true)
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}
	observed, err := provider.Inspect(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || observed.Values["type"] != "other" {
		t.Fatalf("symbolic link observation = %#v", observed)
	}
	if _, err := provider.Apply(context.Background(), engine.Step{Node: node}); err == nil {
		t.Fatal("directory apply unexpectedly followed a symbolic link")
	}
	if err := provider.Delete(context.Background(), engine.Step{Node: node}); err == nil {
		t.Fatal("directory delete unexpectedly removed a symbolic link")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("symbolic link target was changed: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("symbolic link was changed: %v", err)
	}
}

func TestDirectoryProviderUsesOnlyFixedScriptsAndArguments(t *testing.T) {
	path := "/tmp/example; echo not-a-command"
	node := testDirectoryNode(path, true)
	runner := &commandRunner{outputs: map[string][]byte{
		"inspect.directory": []byte("directory\nroot\n0\nroot\n0\n750\n"),
	}}
	if _, err := applyDirectory(context.Background(), runner, node); err != nil {
		t.Fatal(err)
	}
	if err := deleteDirectory(context.Background(), runner, engine.Step{Node: node}); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	apply := runner.commands[0]
	if apply.Name != "apply.directory" || len(apply.Arguments) != 4 || apply.Arguments[0] != path {
		t.Fatalf("apply directory command = %#v", apply)
	}
	remove := runner.commands[2]
	if remove.Name != "delete.directory" || len(remove.Arguments) != 2 || remove.Arguments[0] != path || remove.Arguments[1] != "true" {
		t.Fatalf("delete directory command = %#v", remove)
	}
	if strings.Contains(apply.Script, path) || strings.Contains(remove.Script, path) || !strings.Contains(apply.Script, "path=$1") || !strings.Contains(remove.Script, "path=$1") {
		t.Fatalf("directory scripts contain user data:\napply:\n%s\ndelete:\n%s", apply.Script, remove.Script)
	}
}

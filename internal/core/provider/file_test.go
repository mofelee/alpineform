package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

type localRunner struct{}

func (localRunner) Run(ctx context.Context, command backend.Command) ([]byte, error) {
	arguments := append([]string{"-c", command.Script, "alpineform"}, command.Arguments...)
	process := exec.CommandContext(ctx, "sh", arguments...)
	process.Stdin = bytes.NewReader(command.Stdin)
	output, err := process.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, output)
	}
	return output, nil
}

type commandRunner struct {
	commands []backend.Command
	outputs  map[string][]byte
	errors   map[string]error
}

func (runner *commandRunner) Run(_ context.Context, command backend.Command) ([]byte, error) {
	copyCommand := command
	copyCommand.Arguments = append([]string(nil), command.Arguments...)
	copyCommand.Stdin = append([]byte(nil), command.Stdin...)
	runner.commands = append(runner.commands, copyCommand)
	if err := runner.errors[command.Name]; err != nil {
		return nil, err
	}
	return append([]byte(nil), runner.outputs[command.Name]...), nil
}

func testFileNode(path, content string) graph.Node {
	desired := map[string]any{
		"path":               path,
		"owner":              strconv.Itoa(os.Getuid()),
		"group":              strconv.Itoa(os.Getgid()),
		"mode":               "0640",
		"ensure":             "present",
		"content_sha256":     sha256String(content),
		"content_bytes":      int64(len(content)),
		"content_version":    "",
		"content_write_only": false,
		"delete_behavior":    "",
		"delete":             map[string]any{"path": path},
	}
	return graph.Node{
		Host:       "node",
		Address:    `host.node.files.file["` + path + `"]`,
		Kind:       "file",
		Managed:    true,
		Desired:    desired,
		Payload:    map[string]any{"content": content},
		DigestSafe: true,
		Source:     ir.SourceRef{File: "main.apf.hcl", Line: 3},
	}
}

func sha256String(value string) string {
	return fmt.Sprintf("%x", sha256Bytes([]byte(value)))
}

func sha256Bytes(value []byte) [32]byte {
	return sha256.Sum256(value)
}

func TestFileProviderCreateObserveDriftAndDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "app.conf")
	node := testFileNode(path, "expected\n")
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}
	missing, err := provider.Inspect(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	if missing.Exists {
		t.Fatalf("missing observation = %#v", missing)
	}
	observed, err := provider.Apply(context.Background(), engine.Step{Host: "node", Address: node.Address, Action: engine.ActionCreate, Node: node})
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || corestate.Digest(observed.Values) != corestate.Digest(node.Desired) {
		t.Fatalf("applied observation = %#v, desired=%#v", observed, node.Desired)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "expected\n" {
		t.Fatalf("file content = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("file mode = %o", info.Mode().Perm())
	}
	if err := os.WriteFile(path, []byte("drifted\n"), 0640); err != nil {
		t.Fatal(err)
	}
	drifted, err := provider.Inspect(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	if corestate.Digest(drifted.Values) == corestate.Digest(node.Desired) {
		t.Fatalf("drift was not detected: %#v", drifted.Values)
	}
	if err := provider.Delete(context.Background(), engine.Step{Host: "node", Address: node.Address, Action: engine.ActionDelete, Node: node}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
}

func TestFileProviderUsesOnlyArgumentsAndRedactedStdin(t *testing.T) {
	secret := "not-a-real-file-secret"
	path := "/etc/example; echo not-a-command"
	node := testFileNode(path, secret)
	node.Sensitive = true
	runner := &commandRunner{outputs: map[string][]byte{
		"inspect.file": []byte("file\nroot\n0\nroot\n0\n640\n22\n" + sha256String(secret) + "\n"),
	}}
	_, err := applyFile(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	write := runner.commands[0]
	if write.Name != "apply.file" || !write.RedactStdin || !write.RedactOutput || string(write.Stdin) != secret {
		t.Fatalf("write command = %#v", write)
	}
	if strings.Contains(write.Script, secret) || strings.Contains(write.Script, path) || !strings.Contains(write.Script, "path=$1") {
		t.Fatalf("file script contains user data:\n%s", write.Script)
	}
	if len(write.Arguments) != 4 || write.Arguments[0] != path {
		t.Fatalf("file argv = %#v", write.Arguments)
	}
}

func TestFileProviderDestroysOrphanFromStateIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orphan")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}
	step := engine.Step{Host: "node", Address: "orphan", Action: engine.ActionDestroy, Prior: &corestate.Resource{Kind: "file", Delete: map[string]any{"path": path}}}
	if err := provider.Delete(context.Background(), step); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("orphan still exists: %v", err)
	}
}

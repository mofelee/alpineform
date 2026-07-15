package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/graph"
)

const testBuildIdentity = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestComponentBuildInputStagesProtectedBytesOnlyThroughStdin(t *testing.T) {
	content := []byte("protected-build-input")
	path := filepath.Join(t.TempDir(), "input")
	node := graph.Node{
		Kind: "component_build_input", Sensitive: true, DigestSafe: true,
		Desired: map[string]any{"kind": "content", "path": path, "sha256": "", "content_version": "v1"},
		Payload: map[string]any{"content": content, "sha256": sha256String(string(content))},
	}
	observed, err := applyComponentBuildInput(context.Background(), localRunner{}, node)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || !observed.Protected {
		t.Fatalf("observed = %#v", observed)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("staged content = %q", got)
	}

	runner := &commandRunner{outputs: map[string][]byte{"inspect.component_build_input": []byte("missing\n")}, errors: map[string]error{}}
	_, err = applyComponentBuildInput(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	command := runner.commands[0]
	if !command.RedactStdin || !command.RedactOutput || string(command.Stdin) != string(content) {
		t.Fatalf("protected input command = %#v", command)
	}
	if strings.Contains(command.Script, string(content)) || strings.Contains(strings.Join(command.Arguments, "\x00"), string(content)) {
		t.Fatal("protected input leaked into remote shell source or argv")
	}
}

func TestComponentBuildWorkspaceUsesArgvAndProtectedManifest(t *testing.T) {
	secret := "build-secret-sentinel"
	runner := &commandRunner{
		outputs: map[string][]byte{"inspect.component_build_workspace": []byte("active\n")},
		errors:  map[string]error{},
	}
	node := graph.Node{
		Kind: "component_build_workspace", Sensitive: true, DigestSafe: true,
		Desired: map[string]any{
			"workspace": "/var/tmp/alpineform/builds/" + testBuildIdentity, "build_identity": testBuildIdentity,
			"output_marker": "/var/cache/alpineform/builds/outputs/" + testBuildIdentity + "/artifact.sha256",
			"output":        "tool", "working_directory": ".", "input_paths": map[string]string{},
			"virtual_package":   ".alpineform-build-0123456789abcdef01234567",
			"dependency_marker": "/var/lib/alpineform/builds/owner.dependencies",
		},
		Payload: map[string]any{
			"input_sha256": map[string]string{}, "environment": map[string]string{"TOKEN": secret},
			"commands": []map[string]any{{"argv": []string{"cc", "-o", "tool", "main.c"}, "stdin": []byte(secret)}},
		},
	}
	observed, err := applyComponentBuildWorkspace(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || !observed.Protected {
		t.Fatalf("observed = %#v", observed)
	}
	var execute backend.Command
	for _, command := range runner.commands {
		if command.Name == "apply.component_build_workspace.command" {
			execute = command
		}
	}
	if len(execute.Arguments) < 3 || execute.Arguments[2] != "cc" || !execute.RedactStdin || !execute.RedactOutput {
		t.Fatalf("build execution command = %#v", execute)
	}
	if strings.Contains(execute.Script, secret) || strings.Contains(strings.Join(execute.Arguments, "\x00"), secret) || !strings.Contains(string(execute.Stdin), "APFBUILD1") {
		t.Fatalf("secret placement is unsafe: %#v", execute)
	}
	if strings.Contains(execute.Script, "cc -o") {
		t.Fatal("user argv was interpolated into remote shell source")
	}
}

func TestComponentBuildOutputFailureRunsOwnedCleanupBeforeInstall(t *testing.T) {
	runner := &commandRunner{outputs: map[string][]byte{}, errors: map[string]error{"apply.component_build_output": errors.New("disk full")}}
	node := graph.Node{
		Kind: "component_build_output",
		Desired: map[string]any{
			"workspace": "/var/tmp/alpineform/builds/" + testBuildIdentity, "build_identity": testBuildIdentity,
			"output": "tool", "output_sha256": "", "max_output_bytes": int64(1024),
			"cache_path":        "/var/cache/alpineform/builds/outputs/" + testBuildIdentity + "/artifact",
			"marker_path":       "/var/cache/alpineform/builds/outputs/" + testBuildIdentity + "/artifact.sha256",
			"virtual_package":   ".alpineform-build-0123456789abcdef01234567",
			"dependency_marker": "/var/lib/alpineform/builds/owner.dependencies",
		},
	}
	_, err := applyComponentBuildOutput(context.Background(), runner, node)
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("apply error = %v", err)
	}
	var cleanup bool
	for _, command := range runner.commands {
		cleanup = cleanup || command.Name == "cleanup.component_build_failure"
		if strings.Contains(command.Name, "install") {
			t.Fatalf("output failure reached installation: %#v", command)
		}
	}
	if !cleanup {
		t.Fatalf("commands = %#v", runner.commands)
	}
}

package provider

import (
	"archive/tar"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

const testBuildIdentity = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const testBuildOwner = "0123456789abcdef0123456789abcdef"

func TestComponentBuildInputStagesProtectedBytesOnlyThroughStdin(t *testing.T) {
	content := []byte("protected-build-input")
	path := filepath.Join(t.TempDir(), "input")
	node := graph.Node{
		Kind: "component_build_input", Sensitive: true, DigestSafe: true,
		Desired: map[string]any{"kind": "content", "path": path, "sha256": "", "content_version": "v1"},
		Payload: map[string]any{"content": content, "sha256": sha256String(string(content))},
	}
	observed, err := applyComponentBuildInput(context.Background(), localRunner{}, engine.Step{Node: node})
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
	_, err = applyComponentBuildInput(context.Background(), runner, engine.Step{Node: node})
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

func TestComponentBuildInputUpdateCleansOnlyPreviousRecordedCache(t *testing.T) {
	content := []byte("new-input")
	digest := sha256String(string(content))
	oldPath := "/var/cache/alpineform/builds/inputs/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	newPath := "/var/cache/alpineform/builds/inputs/" + digest
	node := graph.Node{
		Kind: "component_build_input", Desired: map[string]any{"kind": "content", "path": newPath, "sha256": digest},
		Payload: map[string]any{"content": content, "sha256": digest},
	}
	runner := &commandRunner{
		outputs: map[string][]byte{"inspect.component_build_input": []byte("file\n" + digest + "\n")}, errors: map[string]error{},
	}
	step := engine.Step{Node: node, Prior: &corestate.Resource{Delete: map[string]any{"path": oldPath}}}
	if _, err := applyComponentBuildInput(context.Background(), runner, step); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 3 || runner.commands[2].Name != "cleanup.component_build_input_previous" || runner.commands[2].Arguments[0] != oldPath {
		t.Fatalf("commands = %#v", runner.commands)
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
			"owner_id":          testBuildOwner,
			"dependency_marker": "/var/lib/alpineform/builds/owner.dependencies",
		},
		Payload: map[string]any{
			"input_sha256": map[string]string{}, "input_extract": map[string]map[string]any{}, "environment": map[string]string{"TOKEN": secret},
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

func TestComponentBuildWorkspaceStagesSafeArchiveAndRejectsAdversarialEntries(t *testing.T) {
	root := t.TempDir()
	workspace := "/var/tmp/alpineform/builds/" + sha256String(root)
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	cache := filepath.Join(root, "source.tar.gz")
	digest := writeTestTarGZ(t, cache, []archiveEntry{{name: "project/main.c", content: "int main(void) { return 0; }\n"}})
	arguments := []string{workspace, ".", cache, "src", digest, "tar.gz", "1"}
	if _, err := (localRunner{}).Run(context.Background(), backend.Command{Script: componentBuildWorkspacePrepareScript, Arguments: arguments}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "src", "main.c"))
	if err != nil || !strings.Contains(string(data), "main") {
		t.Fatalf("staged archive content = %q, %v", data, err)
	}

	tests := []struct {
		name    string
		entries []archiveEntry
		strip   string
	}{
		{name: "traversal", entries: []archiveEntry{{name: "../escape", content: "bad"}}},
		{name: "absolute", entries: []archiveEntry{{name: "/escape", content: "bad"}}},
		{name: "symlink", entries: []archiveEntry{{name: "project/link", typeflag: tar.TypeSymlink, linkname: "../../escape"}}},
		{name: "special", entries: []archiveEntry{{name: "project/device", typeflag: tar.TypeChar}}},
		{name: "strip collision", entries: []archiveEntry{{name: "one/tool", content: "one"}, {name: "two/tool", content: "two"}}, strip: "1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_ = os.RemoveAll(workspace)
			unsafeCache := filepath.Join(root, strings.ReplaceAll(test.name, " ", "-")+".tar.gz")
			unsafeDigest := writeTestTarGZ(t, unsafeCache, test.entries)
			strip := test.strip
			if strip == "" {
				strip = "0"
			}
			_, err := (localRunner{}).Run(context.Background(), backend.Command{Script: componentBuildWorkspacePrepareScript, Arguments: []string{workspace, ".", unsafeCache, "src", unsafeDigest, "tar.gz", strip}})
			if err == nil {
				t.Fatal("unsafe source-build archive unexpectedly staged")
			}
			if _, statErr := os.Stat(filepath.Join(workspace, "escape")); !os.IsNotExist(statErr) {
				t.Fatalf("archive escaped workspace: %v", statErr)
			}
		})
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
			"owner_id":          testBuildOwner,
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

func TestComponentBuildCancelledStagingRunsBoundedCleanup(t *testing.T) {
	runner := &commandRunner{outputs: map[string][]byte{}, errors: map[string]error{"apply.component_build_workspace.prepare": context.Canceled}}
	node := graph.Node{
		Kind: "component_build_workspace",
		Desired: map[string]any{
			"workspace": "/var/tmp/alpineform/builds/" + testBuildIdentity, "build_identity": testBuildIdentity,
			"output_marker": "/var/cache/alpineform/builds/outputs/" + testBuildIdentity + "/artifact.sha256",
			"output":        "tool", "working_directory": ".", "input_paths": map[string]string{},
			"virtual_package":       ".alpineform-build-0123456789abcdef01234567",
			"owner_id":              testBuildOwner,
			"dependency_marker":     "/var/lib/alpineform/builds/owner.dependencies",
			"protected_input_paths": []string{"/run/alpineform/build-inputs/0123456789abcdef"},
		},
		Payload: map[string]any{
			"input_sha256": map[string]string{}, "input_extract": map[string]map[string]any{},
			"environment": map[string]string{}, "commands": []map[string]any{{"argv": []string{"cc"}, "stdin": []byte{}}},
		},
	}
	_, err := applyComponentBuildWorkspace(context.Background(), runner, node)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("apply error = %v", err)
	}
	if len(runner.commands) != 2 || runner.commands[1].Name != "cleanup.component_build_failure" {
		t.Fatalf("commands = %#v", runner.commands)
	}
	cleanup := runner.commands[1]
	if cleanup.Arguments[len(cleanup.Arguments)-1] != "/run/alpineform/build-inputs/0123456789abcdef" || !cleanup.RedactOutput {
		t.Fatalf("cleanup command = %#v", cleanup)
	}
}

func TestComponentBuildDependencyOwnershipInstallInspectAndRecovery(t *testing.T) {
	node := graph.Node{Kind: "component_build_dependencies", Desired: map[string]any{
		"virtual_package": ".alpineform-build-0123456789abcdef01234567",
		"owner_id":        testBuildOwner, "build_identity": testBuildIdentity,
		"marker_path":   "/var/lib/alpineform/builds/owner.dependencies",
		"output_marker": "/var/cache/alpineform/builds/outputs/" + testBuildIdentity + "/artifact.sha256",
		"packages":      []string{"build-base", "musl-dev"},
	}}
	runner := &commandRunner{outputs: map[string][]byte{"inspect.component_build_dependencies": []byte("active\n")}, errors: map[string]error{}}
	observed, err := applyComponentBuildDependencies(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || len(runner.commands) != 2 {
		t.Fatalf("observed=%#v commands=%#v", observed, runner.commands)
	}
	apply := runner.commands[0]
	wantPrefix := []string{
		".alpineform-build-0123456789abcdef01234567", "/var/lib/alpineform/builds/owner.dependencies",
		testBuildOwner, testBuildIdentity, "build-base", "musl-dev",
	}
	if strings.Join(apply.Arguments, "\x00") != strings.Join(wantPrefix, "\x00") || !apply.RedactOutput {
		t.Fatalf("dependency apply = %#v", apply)
	}
	if !strings.Contains(apply.Script, "/etc/apk/world") || !strings.Contains(apply.Script, "apk --quiet add --virtual \"$virtual\"") {
		t.Fatalf("dependency apply script does not inspect world and own a virtual package")
	}

	oldIdentity := strings.Repeat("a", 64)
	staleRunner := &commandRunner{outputs: map[string][]byte{"inspect.component_build_dependencies": []byte("stale\n" + oldIdentity + "\n")}, errors: map[string]error{}}
	stale, err := inspectComponentBuildDependencies(context.Background(), staleRunner, node)
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Exists || stale.Values["build_identity"] != oldIdentity || stale.Digest == corestate.Digest(node.Desired) {
		t.Fatalf("stale dependency observation = %#v", stale)
	}
}

func TestComponentBuildDependencyFailureAndCleanupAreOwnerScoped(t *testing.T) {
	node := graph.Node{Kind: "component_build_dependencies", Desired: map[string]any{
		"virtual_package": ".alpineform-build-0123456789abcdef01234567",
		"owner_id":        testBuildOwner, "build_identity": testBuildIdentity,
		"marker_path":   "/var/lib/alpineform/builds/owner.dependencies",
		"output_marker": "/var/cache/alpineform/builds/outputs/" + testBuildIdentity + "/artifact.sha256",
		"packages":      []string{"build-base"},
	}}
	for _, failure := range []error{errors.New("apk add failed"), context.Canceled} {
		runner := &commandRunner{outputs: map[string][]byte{}, errors: map[string]error{"apply.component_build_dependencies": failure}}
		_, err := applyComponentBuildDependencies(context.Background(), runner, node)
		if !errors.Is(err, failure) {
			t.Fatalf("dependency apply error = %v, want %v", err, failure)
		}
		if len(runner.commands) != 1 || !strings.Contains(runner.commands[0].Script, "success=0") || !strings.Contains(runner.commands[0].Script, "apk --quiet del \"$virtual\"") {
			t.Fatalf("failed dependency command = %#v", runner.commands)
		}
	}

	cleanupNode := graph.Node{Kind: "component_build_cleanup", Desired: map[string]any{
		"workspace": "/var/tmp/alpineform/builds/" + testBuildIdentity, "build_identity": testBuildIdentity,
		"output_marker":   "/var/cache/alpineform/builds/outputs/" + testBuildIdentity + "/artifact.sha256",
		"virtual_package": ".alpineform-build-0123456789abcdef01234567", "owner_id": testBuildOwner,
		"dependency_marker": "/var/lib/alpineform/builds/owner.dependencies", "protected_input_paths": []string{},
	}}
	runner := &commandRunner{outputs: map[string][]byte{"inspect.component_build_cleanup": []byte("clean\n")}, errors: map[string]error{}}
	if _, err := applyComponentBuildCleanup(context.Background(), runner, cleanupNode); err != nil {
		t.Fatal(err)
	}
	cleanup := runner.commands[0]
	if len(cleanup.Arguments) != 4 || cleanup.Arguments[3] != testBuildOwner || strings.Contains(strings.Join(cleanup.Arguments, "\x00"), "build-base") {
		t.Fatalf("dependency cleanup = %#v", cleanup)
	}
	if strings.Contains(cleanup.Script, `apk --quiet del "$package"`) || !strings.Contains(cleanup.Script, `apk --quiet del "$virtual"`) {
		t.Fatalf("cleanup can delete outside the owned virtual package")
	}
}

package provider

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
	corestate "github.com/mofelee/alpineform/internal/core/state"
	"github.com/mofelee/alpineform/internal/product"
)

const testBuildIdentity = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const testBuildOwner = "0123456789abcdef0123456789abcdef"

func localComponentBuildScript(script string) string {
	return strings.Replace(script, "workspace_uid=0", "workspace_uid="+strconv.Itoa(os.Getuid()), 1)
}

func writeTestBuildDependencyMarker(t *testing.T, path, identity, root, workspace string) {
	t.Helper()
	content := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n", ".alpineform-build-0123456789abcdef01234567", testBuildOwner, identity, root, workspace)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeTestOwnedBuildWorkspace(t *testing.T, root, identity string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, identity)
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	marker := fmt.Sprintf("APFWORKSPACE1\n%s\n%s\n%s\n%s\n", testBuildOwner, identity, root, workspace)
	if err := os.WriteFile(filepath.Join(workspace, ".alpineform-build-owner"), []byte(marker), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workspace, "build"), 0700); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func runLocalComponentBuildScript(t *testing.T, script string, arguments ...string) error {
	t.Helper()
	_, err := (localRunner{}).Run(context.Background(), backend.Command{Script: localComponentBuildScript(script), Arguments: arguments})
	return err
}

type localComponentBuildRunner struct{}

func (localComponentBuildRunner) Run(ctx context.Context, command backend.Command) ([]byte, error) {
	command.Script = localComponentBuildScript(command.Script)
	return (localRunner{}).Run(ctx, command)
}

func infoMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode().Perm()
}

func TestComponentBuildProviderRetainsPhysicalOwnershipAfterRename(t *testing.T) {
	type identities struct {
		Dependency []string
		Workspace  []string
		Output     []string
		Cleanup    []string
		Install    []string
		Command    []string
	}
	component := func(logical, physical string) ir.ComponentInstanceSpec {
		document := &ir.ComponentBuildIdentityDocument{
			Template: "tool", Instance: logical,
			Inputs:           []ir.ComponentBuildInputIdentity{{Name: "source", Kind: "content", Identity: testBuildIdentity, Destination: "main.c"}},
			Commands:         []ir.ComponentBuildCommandIdentity{{Argv: []string{"cc", "-o", "tool", "main.c"}}},
			WorkingDirectory: ".", Output: "tool", MaxOutputBytes: 1024, Dependencies: []string{"build-base"}, Network: "none",
			Install: ir.ComponentBuildInstallIdentity{Path: "/usr/local/bin/tool", Owner: "root", Group: "root", Mode: "0755"},
		}
		instance := ir.ComponentInstanceSpec{
			Name: logical, PhysicalName: logical, Template: "tool", ArtifactType: "source",
			Build: &ir.ComponentBuildSpec{
				Identity: document.DigestForInstance(logical), IdentityDocument: document,
				Inputs:           []ir.ComponentBuildInputSpec{{Name: "source", Kind: "content", SHA256: testBuildIdentity, PayloadSHA256: testBuildIdentity, Destination: "main.c"}},
				Commands:         []ir.ComponentBuildCommandSpec{{Argv: []string{"cc", "-o", "tool", "main.c"}}},
				WorkingDirectory: ".", Output: "tool", MaxOutputBytes: 1024, Dependencies: []string{"build-base"}, Network: "none", OnRemove: "destroy",
			},
			Install: &ir.ComponentArtifactInstallSpec{Path: "/usr/local/bin/tool", Owner: "root", Group: "root", Mode: "0755"},
		}
		return instance.WithPhysicalName(physical)
	}
	providerIdentities := func(instance ir.ComponentInstanceSpec) identities {
		t.Helper()
		resourceGraph, err := graph.Compile(&ir.Program{Hosts: []ir.HostSpec{{Name: "node", Components: []ir.ComponentInstanceSpec{instance}}}})
		if err != nil {
			t.Fatal(err)
		}
		byKind := map[string]graph.Node{}
		for _, node := range resourceGraph.Nodes {
			byKind[node.Kind] = node
		}

		virtual, dependencyMarker, owner, buildIdentity, dependencyOutputMarker, err := componentBuildDependencyIdentity(byKind["component_build_dependencies"])
		if err != nil {
			t.Fatal(err)
		}
		_, workspace, workspaceIdentity, workspaceOutputMarker, err := componentBuildWorkspaceIdentity(byKind["component_build_workspace"])
		if err != nil {
			t.Fatal(err)
		}
		outputCache, outputMarker, outputIdentity, err := componentBuildOutputIdentity(byKind["component_build_output"])
		if err != nil {
			t.Fatal(err)
		}
		_, cleanupWorkspace, cleanupVirtual, cleanupDependencyMarker, cleanupOutputMarker, cleanupIdentity, cleanupOwner, err := componentBuildCleanupIdentity(byKind["component_build_cleanup"])
		if err != nil {
			t.Fatal(err)
		}
		installPath, installMarker, installOutputMarker, installIdentity, err := componentBuildInstallIdentity(byKind["component_build_install"])
		if err != nil {
			t.Fatal(err)
		}

		runner := &commandRunner{outputs: map[string][]byte{"inspect.component_build_cleanup": []byte("clean\n")}, errors: map[string]error{}}
		if _, err := applyComponentBuildCleanup(context.Background(), runner, byKind["component_build_cleanup"]); err != nil {
			t.Fatal(err)
		}
		if len(runner.commands) != 2 || runner.commands[0].Name != "apply.component_build_cleanup" {
			t.Fatalf("cleanup commands = %#v", runner.commands)
		}
		return identities{
			Dependency: []string{virtual, dependencyMarker, owner, buildIdentity, dependencyOutputMarker},
			Workspace:  []string{workspace, workspaceIdentity, workspaceOutputMarker},
			Output:     []string{outputCache, outputMarker, outputIdentity},
			Cleanup:    []string{cleanupWorkspace, cleanupVirtual, cleanupDependencyMarker, cleanupOutputMarker, cleanupIdentity, cleanupOwner},
			Install:    []string{installPath, installMarker, installOutputMarker, installIdentity},
			Command:    append([]string(nil), runner.commands[0].Arguments...),
		}
	}

	legacy := providerIdentities(component("legacy", "legacy"))
	moved := providerIdentities(component("current", "legacy"))
	fresh := providerIdentities(component("current", "current"))
	if !reflect.DeepEqual(moved, legacy) {
		t.Fatalf("provider physical identity changed across rename:\nlegacy=%#v\nmoved=%#v", legacy, moved)
	}
	if reflect.DeepEqual(fresh, legacy) || reflect.DeepEqual(fresh.Command, legacy.Command) {
		t.Fatalf("fresh logical identity unexpectedly reused retained ownership: legacy=%#v fresh=%#v", legacy, fresh)
	}
}

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
	if len(execute.Arguments) < 6 || execute.Arguments[5] != "cc" || !execute.RedactStdin || !execute.RedactOutput {
		t.Fatalf("build execution command = %#v", execute)
	}
	if strings.Contains(execute.Script, secret) || strings.Contains(strings.Join(execute.Arguments, "\x00"), secret) || !strings.Contains(string(execute.Stdin), "APFBUILD1") {
		t.Fatalf("secret placement is unsafe: %#v", execute)
	}
	if strings.Contains(execute.Script, "cc -o") {
		t.Fatal("user argv was interpolated into remote shell source")
	}
	for _, required := range []string{"--unshare-net", "--cap-drop ALL", "--ro-bind /usr /usr", "--bind \"$build\" /workspace", ">/dev/null 2>&1"} {
		if !strings.Contains(execute.Script, required) {
			t.Fatalf("build sandbox script is missing %q", required)
		}
	}
	for _, forbidden := range []string{"--ro-bind / /", "--bind /etc", "--bind /var/lib", "--share-net"} {
		if strings.Contains(execute.Script, forbidden) {
			t.Fatalf("build sandbox exposes forbidden host surface %q", forbidden)
		}
	}
}

func TestComponentBuildWorkspaceStagesSafeArchiveAndRejectsAdversarialEntries(t *testing.T) {
	root := t.TempDir()
	workspaceRoot := filepath.Join(root, "workspace-root")
	identity := sha256String(root)
	workspace := filepath.Join(workspaceRoot, identity)
	dependencyMarker := filepath.Join(root, "dependencies")
	writeTestBuildDependencyMarker(t, dependencyMarker, identity, workspaceRoot, workspace)
	cache := filepath.Join(root, "source.tar.gz")
	digest := writeTestTarGZ(t, cache, []archiveEntry{{name: "project/main.c", content: "int main(void) { return 0; }\n"}})
	arguments := []string{workspaceRoot, workspace, identity, testBuildOwner, ".alpineform-build-0123456789abcdef01234567", dependencyMarker, product.DefaultComponentBuildWorkspaceRoot, ".", cache, "src", digest, "tar.gz", "1"}
	if _, err := (localRunner{}).Run(context.Background(), backend.Command{Script: localComponentBuildScript(componentBuildWorkspacePrepareScript), Arguments: arguments}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "build", "src", "main.c"))
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
			arguments := []string{workspaceRoot, workspace, identity, testBuildOwner, ".alpineform-build-0123456789abcdef01234567", dependencyMarker, product.DefaultComponentBuildWorkspaceRoot, ".", unsafeCache, "src", unsafeDigest, "tar.gz", strip}
			_, err := (localRunner{}).Run(context.Background(), backend.Command{Script: localComponentBuildScript(componentBuildWorkspacePrepareScript), Arguments: arguments})
			if err == nil {
				t.Fatal("unsafe source-build archive unexpectedly staged")
			}
			if _, statErr := os.Lstat(workspace); !os.IsNotExist(statErr) {
				t.Fatalf("failed source-build preparation left workspace: %v", statErr)
			}
			if _, statErr := os.Stat(filepath.Join(workspace, "escape")); !os.IsNotExist(statErr) {
				t.Fatalf("archive escaped workspace: %v", statErr)
			}
		})
	}
}

func TestComponentBuildWorkspaceSelectionUsesRuntimePayloadAndLegacyDefault(t *testing.T) {
	legacyWorkspace := product.DefaultComponentBuildWorkspaceRoot + "/" + testBuildIdentity
	node := graph.Node{
		Desired: map[string]any{"build_identity": testBuildIdentity, "workspace": legacyWorkspace},
		Payload: map[string]any{componentBuildWorkspaceRootPayload: "/srv/alpineform-builds"},
	}
	root, workspace, identity, err := componentBuildWorkspaceSelection(node)
	if err != nil {
		t.Fatal(err)
	}
	if root != "/srv/alpineform-builds" || workspace != root+"/"+testBuildIdentity || identity != testBuildIdentity {
		t.Fatalf("runtime workspace selection = root %q, workspace %q, identity %q", root, workspace, identity)
	}
	if node.Desired["workspace"] != legacyWorkspace {
		t.Fatalf("runtime selection mutated durable workspace metadata: %#v", node.Desired)
	}

	delete(node.Payload, componentBuildWorkspaceRootPayload)
	root, workspace, _, err = componentBuildWorkspaceSelection(node)
	if err != nil || root != product.DefaultComponentBuildWorkspaceRoot || workspace != legacyWorkspace {
		t.Fatalf("legacy workspace selection = root %q, workspace %q, error %v", root, workspace, err)
	}

	invalid := []any{"", "/", "relative", "/srv/../tmp", "/srv/builds/", 42}
	for _, value := range invalid {
		t.Run(fmt.Sprintf("%v", value), func(t *testing.T) {
			node.Payload[componentBuildWorkspaceRootPayload] = value
			if _, _, _, err := componentBuildWorkspaceSelection(node); err == nil {
				t.Fatalf("unsafe workspace root %#v was accepted", value)
			}
		})
	}
	node.Payload = nil
	node.Desired["workspace"] = "/srv/legacy-custom/" + testBuildIdentity
	if _, _, _, err := componentBuildWorkspaceSelection(node); err == nil {
		t.Fatal("nonhistorical legacy workspace was accepted without runtime payload")
	}
}

func TestComponentBuildWorkspacePrepareCreatesPrivateOwnedBoundary(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "selected-root")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	identity := sha256String(t.Name())
	workspace := filepath.Join(root, identity)
	dependencyMarker := filepath.Join(base, "dependencies")
	writeTestBuildDependencyMarker(t, dependencyMarker, identity, root, workspace)
	arguments := []string{root, workspace, identity, testBuildOwner, ".alpineform-build-0123456789abcdef01234567", dependencyMarker, product.DefaultComponentBuildWorkspaceRoot, "."}
	if err := runLocalComponentBuildScript(t, componentBuildWorkspacePrepareScript, arguments...); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		root: 0755, workspace: 0700, filepath.Join(workspace, "build"): 0700,
		filepath.Join(workspace, ".alpineform-build-owner"): 0600,
	} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s mode = %#o, %v; want %#o", path, infoMode(info), err, want)
		}
	}
	marker, err := os.ReadFile(filepath.Join(workspace, ".alpineform-build-owner"))
	if err != nil {
		t.Fatal(err)
	}
	wantMarker := fmt.Sprintf("APFWORKSPACE1\n%s\n%s\n%s\n%s\n", testBuildOwner, identity, root, workspace)
	if string(marker) != wantMarker {
		t.Fatalf("workspace ownership marker = %q, want %q", marker, wantMarker)
	}
}

func TestComponentBuildWorkspacePrepareRejectsUnsafeBoundariesAndAllowsStickyAncestor(t *testing.T) {
	virtual := ".alpineform-build-0123456789abcdef01234567"
	run := func(t *testing.T, root string) (string, error) {
		t.Helper()
		identity := sha256String(t.Name())
		workspace := filepath.Join(root, identity)
		marker := filepath.Join(t.TempDir(), "dependencies")
		writeTestBuildDependencyMarker(t, marker, identity, root, workspace)
		arguments := []string{root, workspace, identity, testBuildOwner, virtual, marker, product.DefaultComponentBuildWorkspaceRoot, "."}
		return workspace, runLocalComponentBuildScript(t, componentBuildWorkspacePrepareScript, arguments...)
	}

	t.Run("nonsticky writable ancestor", func(t *testing.T) {
		base := t.TempDir()
		ancestor := filepath.Join(base, "shared")
		if err := os.Mkdir(ancestor, 0777); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(ancestor, 0777); err != nil {
			t.Fatal(err)
		}
		workspace, err := run(t, filepath.Join(ancestor, "root"))
		if err == nil {
			t.Fatal("workspace below a nonsticky writable ancestor was accepted")
		}
		if _, statErr := os.Lstat(workspace); !os.IsNotExist(statErr) {
			t.Fatalf("rejected workspace was created: %v", statErr)
		}
	})

	t.Run("sticky ancestor", func(t *testing.T) {
		base := t.TempDir()
		ancestor := filepath.Join(base, "sticky")
		if err := os.Mkdir(ancestor, 0777); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(ancestor, os.ModeSticky|0777); err != nil {
			t.Fatal(err)
		}
		workspace, err := run(t, filepath.Join(ancestor, "root"))
		if err != nil {
			t.Fatal(err)
		}
		if info, statErr := os.Stat(workspace); statErr != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("workspace below sticky ancestor = %#v, %v", info, statErr)
		}
	})

	t.Run("symlink ancestor", func(t *testing.T) {
		base := t.TempDir()
		outside := filepath.Join(base, "outside")
		if err := os.Mkdir(outside, 0700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(base, "link")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		if _, err := run(t, filepath.Join(link, "root")); err == nil {
			t.Fatal("workspace below a symbolic-link ancestor was accepted")
		}
		entries, err := os.ReadDir(outside)
		if err != nil || len(entries) != 0 {
			t.Fatalf("symlink target was changed: %#v, %v", entries, err)
		}
	})

	t.Run("nonowner root", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("changing ownership requires root")
		}
		base := t.TempDir()
		root := filepath.Join(base, "foreign")
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(root, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		if _, err := run(t, root); err == nil {
			t.Fatal("workspace root owned by another account was accepted")
		}
	})
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
	_, err := applyComponentBuildOutput(context.Background(), runner, engine.Step{Node: node})
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
			"protected_input_paths": []string{"/run/alpineform/build-inputs/" + testBuildIdentity},
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
	if len(runner.commands) != 3 || runner.commands[1].Name != "cleanup.component_build_failure" || runner.commands[2].Name != "diagnose.component_build_workspace_capacity" {
		t.Fatalf("commands = %#v", runner.commands)
	}
	cleanup := runner.commands[1]
	if cleanup.Arguments[len(cleanup.Arguments)-1] != "/run/alpineform/build-inputs/"+testBuildIdentity || !cleanup.RedactOutput {
		t.Fatalf("cleanup command = %#v", cleanup)
	}
}

func TestComponentBuildFailurePreservesPrimaryCleanupAndSafeCapacityDiagnostic(t *testing.T) {
	primary := errors.New("not-a-real-protected-primary-secret")
	cleanupFailure := errors.New("owned workspace removal failed")
	root := "/srv/alpineform-builds"
	node := graph.Node{
		Kind: "component_build_workspace", Sensitive: true, DigestSafe: true,
		Desired: map[string]any{
			"workspace": product.DefaultComponentBuildWorkspaceRoot + "/" + testBuildIdentity, "build_identity": testBuildIdentity,
			"output_marker": "/var/cache/alpineform/builds/outputs/" + testBuildIdentity + "/artifact.sha256",
			"output":        "tool", "working_directory": ".", "input_paths": map[string]string{},
			"virtual_package": ".alpineform-build-0123456789abcdef01234567", "owner_id": testBuildOwner,
			"dependency_marker": "/var/lib/alpineform/builds/owner.dependencies",
		},
		Payload: map[string]any{
			componentBuildWorkspaceRootPayload: root,
			"input_sha256":                     map[string]string{}, "input_extract": map[string]map[string]any{},
			"environment": map[string]string{}, "commands": []map[string]any{{"argv": []string{"cc"}, "stdin": []byte{}}},
		},
	}
	runner := &commandRunner{
		outputs: map[string][]byte{"diagnose.component_build_workspace_capacity": []byte("4096\n")},
		errors: map[string]error{
			"apply.component_build_workspace.prepare": primary,
			"cleanup.component_build_failure":         cleanupFailure,
		},
	}
	_, err := applyComponentBuildWorkspace(context.Background(), runner, node)
	if !errors.Is(err, primary) || !errors.Is(err, cleanupFailure) {
		t.Fatalf("combined workspace error = %v", err)
	}
	wantSafe := "source-build workspace failed: staging_root=" + root + " work_path=" + root + "/" + testBuildIdentity + " available_kib=4096"
	safe, ok := engine.SafeOperationMessage(err)
	if !ok || safe != wantSafe || strings.Contains(safe, "protected-primary-secret") {
		t.Fatalf("safe workspace failure = %q, %v", safe, ok)
	}
	if len(runner.commands) != 3 || runner.commands[2].Name != "diagnose.component_build_workspace_capacity" || !runner.commands[2].RedactOutput || len(runner.commands[2].Arguments) != 1 || runner.commands[2].Arguments[0] != root {
		t.Fatalf("workspace failure commands = %#v", runner.commands)
	}
}

func TestComponentBuildCleanupOnlyFailureIsApplyFailureWithBoundedDiagnostic(t *testing.T) {
	cleanupFailure := errors.New("cleanup-only failure")
	root := "/mnt/build-work"
	node := graph.Node{Kind: "component_build_cleanup", Desired: map[string]any{
		"workspace": product.DefaultComponentBuildWorkspaceRoot + "/" + testBuildIdentity, "build_identity": testBuildIdentity,
		"output_marker":   "/var/cache/alpineform/builds/outputs/" + testBuildIdentity + "/artifact.sha256",
		"virtual_package": ".alpineform-build-0123456789abcdef01234567", "owner_id": testBuildOwner,
		"dependency_marker": "/var/lib/alpineform/builds/owner.dependencies", "protected_input_paths": []string{},
	}, Payload: map[string]any{componentBuildWorkspaceRootPayload: root}}
	runner := &commandRunner{
		outputs: map[string][]byte{"diagnose.component_build_workspace_capacity": []byte("not-a-capacity-secret\n")},
		errors:  map[string]error{"apply.component_build_cleanup": cleanupFailure},
	}
	_, err := applyComponentBuildCleanup(context.Background(), runner, node)
	if !errors.Is(err, cleanupFailure) {
		t.Fatalf("cleanup-only apply error = %v", err)
	}
	safe, ok := engine.SafeOperationMessage(err)
	want := "source-build workspace failed: staging_root=" + root + " work_path=" + root + "/" + testBuildIdentity + " available_kib=unknown"
	if !ok || safe != want || strings.Contains(safe, "capacity-secret") {
		t.Fatalf("cleanup-only safe error = %q, %v", safe, ok)
	}
	if len(runner.commands) != 2 || runner.commands[0].Name != "apply.component_build_cleanup" || runner.commands[1].Name != "diagnose.component_build_workspace_capacity" {
		t.Fatalf("cleanup-only commands = %#v", runner.commands)
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
		testBuildOwner, testBuildIdentity, product.DefaultComponentBuildWorkspaceRoot,
		product.DefaultComponentBuildWorkspaceRoot + "/" + testBuildIdentity, product.DefaultComponentBuildWorkspaceRoot,
		"build-base", "musl-dev",
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
	sameIdentityRunner := &commandRunner{outputs: map[string][]byte{"inspect.component_build_dependencies": []byte("stale\n" + testBuildIdentity + "\n")}, errors: map[string]error{}}
	sameIdentity, err := inspectComponentBuildDependencies(context.Background(), sameIdentityRunner, node)
	if err != nil {
		t.Fatal(err)
	}
	if !sameIdentity.Exists || sameIdentity.Values["workspace_recovery_pending"] != true || corestate.Digest(sameIdentity.Values) == corestate.Digest(node.Desired) {
		t.Fatalf("same-identity root recovery observation = %#v", sameIdentity)
	}
}

func TestComponentBuildDependenciesTreatVerifiedCacheWithOldRootResidueAsSatisfied(t *testing.T) {
	base := t.TempDir()
	oldRoot := filepath.Join(base, "old-root")
	oldWorkspace := writeTestOwnedBuildWorkspace(t, oldRoot, testBuildIdentity)
	currentRoot := filepath.Join(base, "current-root")
	dependencyMarker := filepath.Join(base, "dependencies")
	writeTestBuildDependencyMarker(t, dependencyMarker, testBuildIdentity, oldRoot, oldWorkspace)
	outputCache := filepath.Join(base, "artifact")
	content := []byte("verified cached artifact")
	if err := os.WriteFile(outputCache, content, 0600); err != nil {
		t.Fatal(err)
	}
	outputMarker := outputCache + ".sha256"
	if err := os.WriteFile(outputMarker, []byte(fmt.Sprintf("%s\n%s\n%d\n", testBuildIdentity, sha256String(string(content)), len(content))), 0600); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(base, "bin")
	if err := os.Mkdir(fakeBin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "apk"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+":"+os.Getenv("PATH"))

	node := graph.Node{Kind: "component_build_dependencies", Desired: map[string]any{
		"virtual_package": ".alpineform-build-0123456789abcdef01234567",
		"owner_id":        testBuildOwner, "build_identity": testBuildIdentity,
		"marker_path": dependencyMarker, "output_marker": outputMarker, "packages": []string{"build-base"},
	}, Payload: map[string]any{
		componentBuildWorkspaceRootPayload: currentRoot,
		componentBuildOutputCachePayload:   outputCache,
	}}
	observed, err := inspectComponentBuildDependencies(context.Background(), localComponentBuildRunner{}, node)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || observed.Digest != corestate.Digest(node.Desired) || !reflect.DeepEqual(observed.Values, node.Desired) {
		t.Fatalf("verified-cache dependency observation = %#v", observed)
	}
	if _, exists := observed.Values["workspace_recovery_pending"]; exists {
		t.Fatalf("verified cache requested dependency rebuild: %#v", observed.Values)
	}
	if err := os.WriteFile(outputCache, []byte("corrupt cached artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	observed, err = inspectComponentBuildDependencies(context.Background(), localComponentBuildRunner{}, node)
	if err != nil {
		t.Fatal(err)
	}
	if observed.Exists {
		t.Fatalf("corrupt cache suppressed dependency recovery: %#v", observed)
	}
}

func TestComponentBuildWorkspaceRequiresVerifiedCacheWhenDependenciesRemainActive(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "current-root")
	workspace := filepath.Join(root, testBuildIdentity)
	dependencyMarker := filepath.Join(base, "dependencies")
	writeTestBuildDependencyMarker(t, dependencyMarker, testBuildIdentity, root, workspace)
	outputCache := filepath.Join(base, "artifact")
	validContent := []byte("verified cached artifact")
	if err := os.WriteFile(outputCache, []byte("corrupt cached artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	outputMarker := outputCache + ".sha256"
	if err := os.WriteFile(outputMarker, []byte(fmt.Sprintf("%s\n%s\n%d\n", testBuildIdentity, sha256String(string(validContent)), len(validContent))), 0600); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(base, "bin")
	if err := os.Mkdir(fakeBin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "apk"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+":"+os.Getenv("PATH"))

	payload := map[string]any{
		componentBuildWorkspaceRootPayload: root,
		componentBuildOutputCachePayload:   outputCache,
	}
	dependencies := graph.Node{Kind: "component_build_dependencies", Desired: map[string]any{
		"virtual_package": ".alpineform-build-0123456789abcdef01234567",
		"owner_id":        testBuildOwner, "build_identity": testBuildIdentity,
		"marker_path": dependencyMarker, "output_marker": outputMarker, "packages": []string{},
	}, Payload: payload}
	dependencyObserved, err := inspectComponentBuildDependencies(context.Background(), localComponentBuildRunner{}, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if !dependencyObserved.Exists || dependencyObserved.Digest != corestate.Digest(dependencies.Desired) {
		t.Fatalf("active dependency observation = %#v", dependencyObserved)
	}

	workspaceNode := graph.Node{Kind: "component_build_workspace", Desired: map[string]any{
		"build_identity": testBuildIdentity, "output_marker": outputMarker, "output": "tool",
		"virtual_package": ".alpineform-build-0123456789abcdef01234567",
		"owner_id":        testBuildOwner, "dependency_marker": dependencyMarker,
	}, Payload: payload}
	workspaceObserved, err := inspectComponentBuildWorkspace(context.Background(), localComponentBuildRunner{}, workspaceNode)
	if err != nil {
		t.Fatal(err)
	}
	if workspaceObserved.Exists {
		t.Fatalf("corrupt cache suppressed workspace rebuild: %#v", workspaceObserved)
	}
	if err := os.WriteFile(outputCache, validContent, 0600); err != nil {
		t.Fatal(err)
	}
	workspaceObserved, err = inspectComponentBuildWorkspace(context.Background(), localComponentBuildRunner{}, workspaceNode)
	if err != nil {
		t.Fatal(err)
	}
	if !workspaceObserved.Exists || workspaceObserved.Digest != corestate.Digest(workspaceNode.Desired) {
		t.Fatalf("verified cache workspace observation = %#v", workspaceObserved)
	}
}

func TestComponentBuildDependenciesRejectMismatchedOutputCachePayload(t *testing.T) {
	node := graph.Node{Kind: "component_build_dependencies", Desired: map[string]any{
		"virtual_package": ".alpineform-build-0123456789abcdef01234567",
		"owner_id":        testBuildOwner, "build_identity": testBuildIdentity,
		"marker_path":   "/var/lib/alpineform/builds/owner.dependencies",
		"output_marker": "/var/cache/alpineform/builds/outputs/" + testBuildIdentity + "/artifact.sha256",
		"packages":      []string{"build-base"},
	}, Payload: map[string]any{
		componentBuildWorkspaceRootPayload: product.DefaultComponentBuildWorkspaceRoot,
		componentBuildOutputCachePayload:   "/var/cache/alpineform/builds/outputs/" + testBuildIdentity + "/other",
	}}
	if _, err := inspectComponentBuildDependencies(context.Background(), &commandRunner{}, node); err == nil || !strings.Contains(err.Error(), "output cache payload is invalid") {
		t.Fatalf("mismatched output cache payload error = %v", err)
	}
}

func TestComponentBuildShellScriptsHaveValidSyntax(t *testing.T) {
	for name, script := range map[string]string{
		"input write":          componentBuildInputWriteScript,
		"dependencies inspect": componentBuildDependenciesInspectScript,
		"dependencies apply":   componentBuildDependenciesApplyScript,
		"workspace inspect":    componentBuildWorkspaceInspectScript,
		"workspace prepare":    componentBuildWorkspacePrepareScript,
		"build command":        componentBuildCommandScript,
		"workspace ready":      componentBuildWorkspaceReadyScript,
		"output inspect":       componentBuildOutputInspectScript,
		"output apply":         componentBuildOutputApplyScript,
		"cleanup inspect":      componentBuildCleanupInspectScript,
		"cleanup apply":        componentBuildCleanupScript,
		"workspace capacity":   componentBuildWorkspaceCapacityScript,
		"install inspect":      componentBuildInstallInspectScript,
		"install apply":        componentBuildInstallApplyScript,
		"install delete":       componentBuildInstallDeleteScript,
	} {
		t.Run(name, func(t *testing.T) {
			command := exec.Command("sh", "-n")
			command.Stdin = strings.NewReader(script)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("shell syntax error: %v: %s", err, output)
			}
		})
	}
}

func TestComponentBuildLegacyInterruptedWorkspaceIsRecoverable(t *testing.T) {
	virtual := ".alpineform-build-0123456789abcdef01234567"
	identity := sha256String(t.Name())
	root := product.DefaultComponentBuildWorkspaceRoot
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Skipf("cannot create historical workspace root: %v", err)
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if int(rootInfo.Sys().(*syscall.Stat_t).Uid) != os.Getuid() {
		t.Skipf("historical workspace root is owned by uid %d", rootInfo.Sys().(*syscall.Stat_t).Uid)
	}
	workspace := filepath.Join(root, identity)
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	base := t.TempDir()
	dependencyMarker := filepath.Join(base, "dependencies")
	content := fmt.Sprintf("%s\n%s\n%s\n", virtual, testBuildOwner, identity)
	if err := os.WriteFile(dependencyMarker, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	outputMarker := filepath.Join(base, "missing-output-marker")
	inspectArguments := []string{root, workspace, identity, testBuildOwner, outputMarker, strings.TrimSuffix(outputMarker, ".sha256"), "tool", virtual, dependencyMarker, root}
	output, err := (localRunner{}).Run(context.Background(), backend.Command{Script: localComponentBuildScript(componentBuildWorkspaceInspectScript), Arguments: inspectArguments})
	if err != nil || strings.TrimSpace(string(output)) != "missing" {
		t.Fatalf("legacy workspace inspection = %q, %v", output, err)
	}
	prepareArguments := []string{root, workspace, identity, testBuildOwner, virtual, dependencyMarker, root, "."}
	if err := runLocalComponentBuildScript(t, componentBuildWorkspacePrepareScript, prepareArguments...); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		workspace: 0700, filepath.Join(workspace, "build"): 0700,
		filepath.Join(workspace, ".alpineform-build-owner"): 0600,
	} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("recovered %s mode = %#o, %v; want %#o", path, infoMode(info), err, want)
		}
	}
}

func TestComponentBuildFiveLineInterruptedWorkspaceIsRecoverable(t *testing.T) {
	virtual := ".alpineform-build-0123456789abcdef01234567"
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, testBuildIdentity)
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	dependencyMarker := filepath.Join(base, "dependencies")
	writeTestBuildDependencyMarker(t, dependencyMarker, testBuildIdentity, root, workspace)
	outputMarker := filepath.Join(base, "missing-output-marker")
	inspectArguments := []string{root, workspace, testBuildIdentity, testBuildOwner, outputMarker, strings.TrimSuffix(outputMarker, ".sha256"), "tool", virtual, dependencyMarker, product.DefaultComponentBuildWorkspaceRoot}
	output, err := (localRunner{}).Run(context.Background(), backend.Command{Script: localComponentBuildScript(componentBuildWorkspaceInspectScript), Arguments: inspectArguments})
	if err != nil || strings.TrimSpace(string(output)) != "missing" {
		t.Fatalf("interrupted workspace inspection = %q, %v", output, err)
	}
	prepareArguments := []string{root, workspace, testBuildIdentity, testBuildOwner, virtual, dependencyMarker, product.DefaultComponentBuildWorkspaceRoot, "."}
	if err := runLocalComponentBuildScript(t, componentBuildWorkspacePrepareScript, prepareArguments...); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		workspace: 0700, filepath.Join(workspace, "build"): 0700,
		filepath.Join(workspace, ".alpineform-build-owner"): 0600,
	} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("recovered %s mode = %#o, %v; want %#o", path, infoMode(info), err, want)
		}
	}
	if err := os.RemoveAll(filepath.Join(workspace, "build")); err != nil {
		t.Fatal(err)
	}
	output, err = (localRunner{}).Run(context.Background(), backend.Command{Script: localComponentBuildScript(componentBuildWorkspaceInspectScript), Arguments: inspectArguments})
	if err != nil || strings.TrimSpace(string(output)) != "missing" {
		t.Fatalf("owner-marked interrupted workspace inspection = %q, %v", output, err)
	}
	if err := runLocalComponentBuildScript(t, componentBuildWorkspacePrepareScript, prepareArguments...); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(workspace, "build")); err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatalf("owner-marked interrupted workspace recovery = %#o, %v", infoMode(info), err)
	}

	if err := os.RemoveAll(workspace); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	cleanupArguments := []string{root, workspace, virtual, dependencyMarker, testBuildOwner, testBuildIdentity, product.DefaultComponentBuildWorkspaceRoot}
	if err := runLocalComponentBuildScript(t, componentBuildCleanupScript, cleanupArguments...); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{workspace, dependencyMarker} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("interrupted cleanup left %s: %v", path, err)
		}
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
		if len(runner.commands) != 2 || runner.commands[1].Name != "diagnose.component_build_workspace_capacity" || !strings.Contains(runner.commands[0].Script, "success=0") || !strings.Contains(runner.commands[0].Script, "apk --quiet del \"$virtual\"") {
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
	if len(cleanup.Arguments) != 7 || cleanup.Arguments[4] != testBuildOwner || strings.Contains(strings.Join(cleanup.Arguments, "\x00"), "build-base") {
		t.Fatalf("dependency cleanup = %#v", cleanup)
	}
	if strings.Contains(cleanup.Script, `apk --quiet del "$package"`) || !strings.Contains(cleanup.Script, `apk --quiet del "$virtual"`) {
		t.Fatalf("cleanup can delete outside the owned virtual package")
	}
}

func TestComponentBuildCleanupRecoversRecordedOldRootAndLegacyDefault(t *testing.T) {
	virtual := ".alpineform-build-0123456789abcdef01234567"

	t.Run("five line old root", func(t *testing.T) {
		base := t.TempDir()
		oldRoot := filepath.Join(base, "old-root")
		oldWorkspace := writeTestOwnedBuildWorkspace(t, oldRoot, testBuildIdentity)
		currentRoot := filepath.Join(base, "current-root")
		currentWorkspace := filepath.Join(currentRoot, testBuildIdentity)
		marker := filepath.Join(base, "dependencies")
		writeTestBuildDependencyMarker(t, marker, testBuildIdentity, oldRoot, oldWorkspace)
		arguments := []string{currentRoot, currentWorkspace, virtual, marker, testBuildOwner, testBuildIdentity, product.DefaultComponentBuildWorkspaceRoot}
		if err := runLocalComponentBuildScript(t, componentBuildCleanupScript, arguments...); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{oldWorkspace, marker} {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("recorded cleanup left %s: %v", path, err)
			}
		}
	})

	t.Run("legacy default root", func(t *testing.T) {
		identity := sha256String(t.Name())
		legacyRoot := product.DefaultComponentBuildWorkspaceRoot
		if err := os.MkdirAll(legacyRoot, 0755); err != nil {
			t.Skipf("cannot create historical workspace root: %v", err)
		}
		legacyWorkspace := filepath.Join(legacyRoot, identity)
		if err := os.Mkdir(legacyWorkspace, 0700); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(legacyWorkspace) })
		base := t.TempDir()
		marker := filepath.Join(base, "dependencies")
		content := fmt.Sprintf("%s\n%s\n%s\n", virtual, testBuildOwner, identity)
		if err := os.WriteFile(marker, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		currentRoot := filepath.Join(base, "current")
		arguments := []string{currentRoot, filepath.Join(currentRoot, identity), virtual, marker, testBuildOwner, identity, legacyRoot}
		if err := runLocalComponentBuildScript(t, componentBuildCleanupScript, arguments...); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{legacyWorkspace, marker} {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("legacy cleanup left %s: %v", path, err)
			}
		}
	})
}

func TestComponentBuildCleanupNeverDeletesUnownedOrMismatchedPaths(t *testing.T) {
	virtual := ".alpineform-build-0123456789abcdef01234567"
	run := func(t *testing.T, root, workspace, marker string) error {
		t.Helper()
		arguments := []string{root, workspace, virtual, marker, testBuildOwner, testBuildIdentity, product.DefaultComponentBuildWorkspaceRoot}
		return runLocalComponentBuildScript(t, componentBuildCleanupScript, arguments...)
	}

	t.Run("malformed recorded path", func(t *testing.T) {
		base := t.TempDir()
		root := filepath.Join(base, "root")
		workspace := writeTestOwnedBuildWorkspace(t, root, testBuildIdentity)
		external := filepath.Join(base, "external")
		if err := os.Mkdir(external, 0700); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(external, "keep")
		if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(base, "dependencies")
		content := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n", virtual, testBuildOwner, testBuildIdentity, root, external)
		if err := os.WriteFile(marker, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if err := run(t, root, workspace, marker); err == nil {
			t.Fatal("mismatched recorded workspace was accepted")
		}
		for _, path := range []string{workspace, marker, sentinel} {
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("refused cleanup changed %s: %v", path, err)
			}
		}
	})

	tests := []struct {
		name    string
		prepare func(t *testing.T, base, root, workspace string)
	}{
		{name: "missing owner marker", prepare: func(t *testing.T, _, _, workspace string) {
			if err := os.Mkdir(workspace, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(workspace, "keep"), []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "workspace symlink", prepare: func(t *testing.T, base, _, workspace string) {
			outside := filepath.Join(base, "outside")
			if err := os.Mkdir(outside, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, workspace); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "writable root mode", prepare: func(t *testing.T, _, root, workspace string) {
			writeTestOwnedBuildWorkspace(t, root, testBuildIdentity)
			if err := os.Chmod(root, 0770); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "owner marker mode", prepare: func(t *testing.T, _, root, workspace string) {
			writeTestOwnedBuildWorkspace(t, root, testBuildIdentity)
			if err := os.Chmod(filepath.Join(workspace, ".alpineform-build-owner"), 0644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "root")
			if err := os.Mkdir(root, 0755); err != nil {
				t.Fatal(err)
			}
			workspace := filepath.Join(root, testBuildIdentity)
			test.prepare(t, base, root, workspace)
			if err := run(t, root, workspace, filepath.Join(base, "absent-marker")); err == nil {
				t.Fatal("unowned or unsafe workspace cleanup unexpectedly succeeded")
			}
			if _, err := os.Lstat(workspace); err != nil {
				t.Fatalf("refused cleanup removed workspace boundary: %v", err)
			}
		})
	}
}

func TestComponentBuildCleanupOrderingPreservesRetryOwnership(t *testing.T) {
	protectedCleanup := strings.LastIndex(componentBuildCleanupScript, `rm -f "$protected_path"`)
	markerCleanup := strings.LastIndex(componentBuildCleanupScript, `rm -f "$marker"`)
	if protectedCleanup < 0 || markerCleanup < 0 || markerCleanup < protectedCleanup {
		t.Fatal("dependency marker is not retained until protected-input cleanup completes")
	}
	for _, required := range []string{"cleanup_status=0", `if ! apk --quiet del "$virtual"`, `if [ "$cleanup_status" = 0 ]; then rm -f "$marker"`} {
		if !strings.Contains(componentBuildDependenciesApplyScript, required) {
			t.Fatalf("dependency failure cleanup does not preserve retry ownership: missing %q", required)
		}
	}
	if strings.Contains(componentBuildDependenciesApplyScript, `apk --quiet del "$virtual" >/dev/null 2>&1 || true`) {
		t.Fatal("dependency failure cleanup still masks APK cleanup failure")
	}
	if !strings.Contains(componentBuildCleanupInspectScript, `[ -e "$protected_path" ] || [ -L "$protected_path" ]`) {
		t.Fatal("cleanup inspection can ignore a broken protected-input symlink")
	}
}

func TestComponentBuildOutputRejectsMissingLinkedSpecialAndOversizedCandidates(t *testing.T) {
	root := t.TempDir()
	installed := filepath.Join(root, "installed")
	if err := os.WriteFile(installed, []byte("previous-install"), 0755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		prepare    func(string) error
		output     string
		maxBytes   int64
		expected   string
		executable bool
	}{
		{name: "missing", output: "tool", maxBytes: 1024},
		{name: "symlink", output: "tool", maxBytes: 1024, prepare: func(workspace string) error { return os.Symlink(installed, filepath.Join(workspace, "tool")) }},
		{name: "parent symlink", output: "out/tool", maxBytes: 1024, prepare: func(workspace string) error { return os.Symlink(root, filepath.Join(workspace, "out")) }},
		{name: "directory", output: "tool", maxBytes: 1024, prepare: func(workspace string) error { return os.Mkdir(filepath.Join(workspace, "tool"), 0700) }},
		{name: "fifo", output: "tool", maxBytes: 1024, prepare: func(workspace string) error { return syscall.Mkfifo(filepath.Join(workspace, "tool"), 0600) }},
		{name: "oversized", output: "tool", maxBytes: 3, prepare: func(workspace string) error {
			return os.WriteFile(filepath.Join(workspace, "tool"), []byte("large"), 0600)
		}},
		{name: "checksum", output: "tool", maxBytes: 1024, expected: strings.Repeat("f", 64), prepare: func(workspace string) error {
			return os.WriteFile(filepath.Join(workspace, "tool"), []byte("content"), 0600)
		}},
		{name: "not executable", output: "tool", maxBytes: 1024, executable: true, prepare: func(workspace string) error {
			return os.WriteFile(filepath.Join(workspace, "tool"), []byte("content"), 0600)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity := sha256String(t.Name())
			workspace := "/var/tmp/alpineform/builds/" + identity
			_ = os.RemoveAll(workspace)
			if err := os.MkdirAll(workspace, 0700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(workspace) })
			if test.prepare != nil {
				if err := test.prepare(workspace); err != nil {
					t.Fatal(err)
				}
			}
			cache := filepath.Join(root, test.name, "artifact")
			node := graph.Node{Kind: "component_build_output", Desired: map[string]any{
				"workspace": workspace, "build_identity": identity, "output": test.output,
				"output_sha256": test.expected, "max_output_bytes": test.maxBytes,
				"executable": test.executable,
				"cache_path": cache, "marker_path": cache + ".sha256",
				"virtual_package": ".alpineform-build-0123456789abcdef01234567", "owner_id": testBuildOwner,
				"dependency_marker": filepath.Join(root, "missing.dependencies"), "protected_input_paths": []string{},
			}}
			if _, err := applyComponentBuildOutput(context.Background(), localRunner{}, engine.Step{Node: node}); err == nil {
				t.Fatal("invalid source-build output unexpectedly passed verification")
			}
			data, err := os.ReadFile(installed)
			if err != nil || string(data) != "previous-install" {
				t.Fatalf("failed output verification changed prior install: %q, %v", data, err)
			}
		})
	}
}

func TestComponentBuildInstallAtomicApplyDriftRepairAndOwnedDestroy(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache", "artifact")
	if err := os.MkdirAll(filepath.Dir(cache), 0700); err != nil {
		t.Fatal(err)
	}
	content := []byte("built-output")
	if err := os.WriteFile(cache, content, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256String(string(content))
	outputMarker := cache + ".sha256"
	if err := os.WriteFile(outputMarker, []byte(testBuildIdentity+"\n"+digest+"\n"+fmt.Sprint(len(content))+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "bin", "tool")
	installMarker := filepath.Join(root, "state", "installed")
	node := graph.Node{Kind: "component_build_install", Desired: map[string]any{
		"build_identity": testBuildIdentity, "cache_path": cache, "output_marker": outputMarker,
		"path": target, "owner": strconv.Itoa(os.Getuid()), "group": strconv.Itoa(os.Getgid()), "mode": "0755",
		"install_marker": installMarker, "ensure": "present", "delete_behavior": "destroy",
		"delete": map[string]any{
			"path": target, "install_marker": installMarker, "cache_path": cache,
			"output_marker": outputMarker, "build_identity": testBuildIdentity,
		},
	}}
	observed, err := applyComponentBuildInstall(context.Background(), localRunner{}, node)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || corestate.Digest(observed.Values) != corestate.Digest(node.Desired) {
		t.Fatalf("observed = %#v", observed)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != string(content) {
		t.Fatalf("installed = %q, %v", data, err)
	}
	if err := os.WriteFile(target, []byte("drifted"), 0755); err != nil {
		t.Fatal(err)
	}
	drifted, err := inspectComponentBuildInstall(context.Background(), localRunner{}, node)
	if err != nil {
		t.Fatal(err)
	}
	if !drifted.Exists || corestate.Digest(drifted.Values) == corestate.Digest(node.Desired) {
		t.Fatalf("drift observation = %#v", drifted)
	}
	if _, err := applyComponentBuildInstall(context.Background(), localRunner{}, node); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != string(content) {
		t.Fatalf("repaired = %q, %v", data, err)
	}

	if err := os.WriteFile(target, []byte("external"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := deleteComponentBuildResource(context.Background(), localRunner{}, engine.Step{Action: engine.ActionDestroy, Node: node}); err == nil {
		t.Fatal("destroy unexpectedly removed a drifted installation")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "external" {
		t.Fatalf("refused destroy changed external content: %q, %v", data, err)
	}
	if _, err := applyComponentBuildInstall(context.Background(), localRunner{}, node); err != nil {
		t.Fatal(err)
	}
	if err := deleteComponentBuildResource(context.Background(), localRunner{}, engine.Step{Action: engine.ActionDestroy, Node: node}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{target, installMarker, cache, outputMarker} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("owned destroy left %s: %v", path, err)
		}
	}
}

func TestComponentBuildInstallReplacesTargetSymlinkWithoutFollowing(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	content := []byte("verified")
	if err := os.WriteFile(cache, content, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256String(string(content))
	outputMarker := cache + ".sha256"
	if err := os.WriteFile(outputMarker, []byte(testBuildIdentity+"\n"+digest+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "tool")
	if err := os.Symlink(outside, target); err != nil {
		t.Fatal(err)
	}
	node := graph.Node{Kind: "component_build_install", Desired: map[string]any{
		"build_identity": testBuildIdentity, "cache_path": cache, "output_marker": outputMarker,
		"path": target, "owner": strconv.Itoa(os.Getuid()), "group": strconv.Itoa(os.Getgid()), "mode": "0755",
		"install_marker": filepath.Join(root, "state", "installed"),
	}}
	if _, err := applyComponentBuildInstall(context.Background(), localRunner{}, node); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("target = %#v, %v", info, err)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("symlink destination was followed: %#v, %v", entries, err)
	}
}

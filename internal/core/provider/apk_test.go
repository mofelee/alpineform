package provider

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

func testAPKRepositoryNode(name, line, ensure string) graph.Node {
	return graph.Node{
		Host: "node", Address: `host.node.apk.repository["` + name + `"]`, Kind: "apk_repository", Managed: true,
		Desired: map[string]any{
			"name": name, "line": line, "ownership": "managed", "ensure": ensure,
			"delete_behavior": "", "delete": map[string]any{"name": name}, "prevent_destroy": false,
		},
		DigestSafe: true,
	}
}

func TestManagedAPKRepositoryScriptsPreserveExternalLinesAndComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repositories")
	external := "# external comment\nhttps://mirror.example/alpine/v3.24/main\n\n# another tool\n"
	if err := os.WriteFile(path, []byte(external), 0644); err != nil {
		t.Fatal(err)
	}
	name := "community"
	line := "@stable https://mirror.example/alpine/v3.24/community"
	begin, end := apkRepositoryMarkers(name)
	runner := localRunner{}
	if _, err := runner.Run(context.Background(), backend.Command{Script: apkManagedRepositoryWriteScript, Arguments: []string{path, begin, end, line}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), external) || !strings.Contains(string(data), begin+"\n"+line+"\n"+end+"\n") {
		t.Fatalf("managed repositories file = %q", data)
	}
	output, err := runner.Run(context.Background(), backend.Command{Script: apkManagedRepositoryInspectScript, Arguments: []string{path, begin, end}})
	if err != nil || string(output) != "repository\n"+line+"\n" {
		t.Fatalf("managed inspect output = %q, error = %v", output, err)
	}
	if _, err := runner.Run(context.Background(), backend.Command{Script: apkManagedRepositoryDeleteScript, Arguments: []string{path, begin, end}}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != external {
		t.Fatalf("external repository content changed after explicit removal: %q", data)
	}
}

func TestAPKProvidersUseFixedScriptsAndPositionalArguments(t *testing.T) {
	repositoryLine := "https://example.test/alpine/v3.24/main; echo not-a-command"
	repository := testAPKRepositoryNode("main", repositoryLine, "present")
	runner := &commandRunner{outputs: map[string][]byte{
		"inspect.apk_repository": []byte("repository\n" + repositoryLine + "\n"),
	}}
	if _, err := applyAPKRepository(context.Background(), runner, repository); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 || strings.Contains(runner.commands[0].Script, repositoryLine) || runner.commands[0].Arguments[3] != repositoryLine {
		t.Fatalf("repository commands = %#v", runner.commands)
	}

	key := graph.Node{Kind: "apk_key", Desired: map[string]any{
		"filename": "vendor.rsa.pub", "sha256": sha256String("public-key"), "ensure": "present",
	}, Payload: map[string]any{"content": []byte("public-key")}}
	runner = &commandRunner{outputs: map[string][]byte{"inspect.apk_key": []byte("key\n" + sha256String("public-key") + "\n")}}
	if _, err := applyAPKKey(context.Background(), runner, key); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 || !runner.commands[0].RedactStdin || string(runner.commands[0].Stdin) != "public-key" || strings.Contains(runner.commands[0].Script, "public-key") {
		t.Fatalf("key commands = %#v", runner.commands)
	}
	for _, command := range append([]backend.Command(nil), runner.commands...) {
		if strings.Contains(command.Script, "apk upgrade") || strings.Contains(command.Script, "apk fix") {
			t.Fatalf("forbidden APK mutation in script: %s", command.Script)
		}
	}
}

func TestAPKUpdateRunsOnceAndRequiresReadyInputs(t *testing.T) {
	repository := testAPKRepositoryNode("main", "https://example.test/alpine/v3.24/main", "present")
	update := graph.Node{Kind: "apk_update", Desired: map[string]any{
		"fingerprint": strings.Repeat("a", 64), "ensure": "present", "delete_behavior": "",
	}, Payload: map[string]any{"readiness": []graph.Node{repository}}}
	runner := &commandRunner{outputs: map[string][]byte{
		"inspect.apk_repository": []byte("repository\nhttps://example.test/alpine/v3.24/main\n"),
		"inspect.apk_update":     []byte("marker\n" + strings.Repeat("a", 64) + "\n"),
	}}
	observed, err := inspectAPKUpdate(context.Background(), runner, update)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || corestate.Digest(observed.Values) != corestate.Digest(update.Desired) {
		t.Fatalf("ready APK update observation = %#v", observed)
	}
	if _, err := applyAPKUpdate(context.Background(), runner, update); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, command := range runner.commands {
		if command.Name == "apply.apk_update" {
			count++
			if !strings.Contains(command.Script, "apk --quiet update") || strings.Contains(command.Script, "upgrade") || strings.Contains(command.Script, "fix") {
				t.Fatalf("APK update script = %s", command.Script)
			}
		}
	}
	if count != 1 {
		t.Fatalf("APK update command count = %d, commands = %#v", count, runner.commands)
	}
}

func TestAPKDeclarationRemovalStateDefaultsToForget(t *testing.T) {
	repository := testAPKRepositoryNode("main", "https://example.test/alpine/v3.24/main", "present")
	stateResource := corestate.Resource{Kind: repository.Kind, DeleteBehavior: stringValue(repository.Desired, "delete_behavior"), Delete: map[string]any{"name": "main"}}
	if stateResource.DeleteBehavior != "" {
		t.Fatalf("managed repository orphan behavior = %q", stateResource.DeleteBehavior)
	}
	key := corestate.Resource{Kind: "apk_key", DeleteBehavior: "", Delete: map[string]any{"filename": "vendor.rsa.pub"}}
	if !reflect.DeepEqual([]string{stateResource.DeleteBehavior, key.DeleteBehavior}, []string{"", ""}) {
		t.Fatalf("APK orphan behavior changed: repository=%#v key=%#v", stateResource, key)
	}
	step := engine.Step{Action: engine.ActionDestroy, Prior: &stateResource}
	if step.Prior.DeleteBehavior != "" {
		t.Fatal("test state unexpectedly requests orphan destruction")
	}
}

func TestAPKScriptsUseValidShellSyntax(t *testing.T) {
	scripts := map[string]string{
		"repository inspect":    apkManagedRepositoryInspectScript,
		"repository write":      apkManagedRepositoryWriteScript,
		"repository delete":     apkManagedRepositoryDeleteScript,
		"authoritative inspect": apkAuthoritativeRepositoryInspectScript,
		"authoritative write":   apkAuthoritativeRepositoryWriteScript,
		"key inspect":           apkKeyInspectScript,
		"key write":             apkKeyWriteScript,
		"key delete":            apkKeyDeleteScript,
		"update inspect":        apkUpdateInspectScript,
		"update apply":          apkUpdateApplyScript,
	}
	for name, script := range scripts {
		t.Run(name, func(t *testing.T) {
			command := exec.Command("sh", "-n")
			command.Stdin = strings.NewReader(script)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("shell syntax error: %v: %s", err, output)
			}
		})
	}
}

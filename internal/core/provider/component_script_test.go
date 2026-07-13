package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

func TestComponentScriptRunsOnceRecordsOutputsAndDetectsDrift(t *testing.T) {
	root := t.TempDir()
	counter := filepath.Join(root, "counter")
	output := filepath.Join(root, "output")
	marker := filepath.Join(root, "state", "outputs")
	content := "count=0\n[ ! -f '" + counter + "' ] || count=$(cat '" + counter + "')\ncount=$((count + 1))\nprintf '%s' \"$count\" >'" + counter + "'\nprintf '%s' \"$APF_TRIGGER_ADDRESSES\" >'" + output + "'\n"
	node := graph.Node{Host: "node", Address: "script", Kind: "component_script", Managed: true, DigestSafe: true,
		Desired: map[string]any{"name": "refresh", "declaration_id": "script.refresh", "script_digest": sha256String(content), "outputs": []string{output}, "marker_path": marker, "ensure": "present", "delete_behavior": ""},
		Payload: map[string]any{"interpreter": []string{"/bin/sh", "-eu"}, "commands": [][]string(nil), "content": content, "outputs": []string{output}, "trigger_paths": map[string]string{"first": "/etc/first", "second": "/etc/second"}},
	}
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}
	step := engine.Step{Host: "node", Address: node.Address, Action: engine.ActionUpdate, Node: node, TriggeredBy: []string{"first", "second"}}
	observed, err := provider.Apply(context.Background(), step)
	if err != nil {
		t.Fatal(err)
	}
	if corestate.Digest(observed.Values) != corestate.Digest(node.Desired) {
		t.Fatalf("script observation = %#v", observed)
	}
	if count, err := os.ReadFile(counter); err != nil || string(count) != "1" {
		t.Fatalf("script count = %q, %v", count, err)
	}
	if data, err := os.ReadFile(output); err != nil || string(data) != "first\nsecond" {
		t.Fatalf("script output = %q, %v", data, err)
	}
	changed := node
	changed.Desired = cloneDesired(node.Desired)
	changed.Desired["script_digest"] = sha256String(content + "\n")
	declarationDrift, err := provider.Inspect(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if declarationDrift.Values["outputs_integrity"] != "drift" {
		t.Fatalf("script declaration drift = %#v", declarationDrift)
	}
	if err := os.WriteFile(output, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	drifted, err := provider.Inspect(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	if drifted.Values["outputs_integrity"] != "drift" {
		t.Fatalf("output drift = %#v", drifted)
	}
}

func TestSensitiveComponentScriptKeepsPayloadOutOfRemoteScriptAndErrors(t *testing.T) {
	secret := "not-a-real-command-secret"
	node := graph.Node{Host: "node", Address: "script", Kind: "component_script", Managed: true, Sensitive: true,
		Desired: map[string]any{"name": "refresh", "marker_path": "/var/lib/alpineform/script", "outputs": []string{}, "ensure": "present"},
		Payload: map[string]any{"interpreter": []string(nil), "commands": [][]string{{"refresh", secret}}, "content": "", "outputs": []string{}, "trigger_paths": map[string]string{"first": "/etc/first"}},
	}
	runner := &commandRunner{outputs: map[string][]byte{}}
	_, err := applyComponentScript(context.Background(), runner, engine.Step{Node: node, TriggeredBy: []string{"first"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	command := runner.commands[0]
	if !command.RedactOutput || strings.Contains(command.Script, secret) || command.Arguments[len(command.Arguments)-1] != secret {
		t.Fatalf("sensitive command = %#v", command)
	}
}

func TestComponentScriptFailureDoesNotRecordOutputSuccess(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	node := graph.Node{Host: "node", Address: "script", Kind: "component_script", Managed: true,
		Desired: map[string]any{"name": "fail", "marker_path": marker, "outputs": []string{"/missing"}, "ensure": "present"},
		Payload: map[string]any{"interpreter": []string{"/bin/sh", "-eu"}, "commands": [][]string(nil), "content": "exit 9", "outputs": []string{"/missing"}, "trigger_paths": map[string]string{}},
	}
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return localRunner{}, nil }}
	if _, err := provider.Apply(context.Background(), engine.Step{Host: "node", Node: node}); err == nil {
		t.Fatal("failing script unexpectedly succeeded")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("failing script recorded success marker: %v", err)
	}
}

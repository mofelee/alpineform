package provider

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

func testPackageNode(name, tag, ensure string) graph.Node {
	intent := name
	if tag != "" {
		intent += "@" + tag
	}
	return graph.Node{Kind: "package", Desired: map[string]any{
		"name": name, "repository": tag, "world_intent": intent, "installed": true, "world": true,
		"ensure": ensure, "delete_behavior": "", "delete": map[string]any{"name": name}, "prevent_destroy": false,
	}}
}

func TestPackageProviderObservesInstalledAndExactWorldIntent(t *testing.T) {
	node := testPackageNode("vendor-agent", "vendor", "present")
	runner := &commandRunner{outputs: map[string][]byte{"inspect.package": []byte("package\ntrue\ntrue\nvendor-agent-1.2.3-r0\n")}}
	observed, err := inspectPackage(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || observed.Values["installed_package"] != "vendor-agent-1.2.3-r0" || observed.Digest != corestate.Digest(node.Desired) || runner.commands[0].Arguments[1] != "vendor-agent@vendor" {
		t.Fatalf("package observation = %#v, commands=%#v", observed, runner.commands)
	}
	runner.outputs["inspect.package"] = []byte("package\ntrue\nfalse\nvendor-agent-1.2.3-r0\n")
	drifted, err := inspectPackage(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if drifted.Digest == corestate.Digest(node.Desired) || drifted.Values["world"] != false {
		t.Fatalf("missing world intent was not drift: %#v", drifted)
	}
}

func TestPackageProviderUsesSafeAddAndExplicitDeleteOnly(t *testing.T) {
	node := testPackageNode("curl", "testing", "present")
	runner := &commandRunner{outputs: map[string][]byte{"inspect.package": []byte("package\ntrue\ntrue\ncurl-8.0-r0\n")}}
	if _, err := applyPackage(context.Background(), runner, node); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 || runner.commands[0].Name != "apply.package" || runner.commands[0].Arguments[0] != "curl@testing" || strings.Contains(runner.commands[0].Script, "curl") || !strings.Contains(runner.commands[0].Script, "apk --quiet add") {
		t.Fatalf("package add commands = %#v", runner.commands)
	}
	if err := deletePackage(context.Background(), runner, engine.Step{Action: engine.ActionDestroy, Prior: &corestate.Resource{Kind: "package", Delete: map[string]any{"name": "curl"}}}); err == nil {
		t.Fatal("orphan package destroy was accepted")
	}
	absent := testPackageNode("curl", "", "absent")
	if err := deletePackage(context.Background(), runner, engine.Step{Action: engine.ActionDelete, Node: absent}); err != nil {
		t.Fatal(err)
	}
	remove := runner.commands[len(runner.commands)-1]
	if remove.Name != "delete.package" || remove.Arguments[0] != "curl" || strings.Contains(remove.Script, "curl") || !strings.Contains(remove.Script, "apk --quiet del") {
		t.Fatalf("package delete command = %#v", remove)
	}
	for _, command := range runner.commands {
		if strings.Contains(command.Script, "upgrade") || strings.Contains(command.Script, "apk fix") {
			t.Fatalf("forbidden APK mutation: %s", command.Script)
		}
	}
}

func TestPackageProviderRejectsInjectionAndScriptsHaveValidSyntax(t *testing.T) {
	if _, err := applyPackage(context.Background(), &commandRunner{}, testPackageNode("curl;reboot", "", "present")); err == nil {
		t.Fatal("unsafe package name was accepted")
	}
	for name, script := range map[string]string{"inspect": packageInspectScript, "add": packageAddScript, "delete": packageDeleteScript} {
		t.Run(name, func(t *testing.T) {
			command := exec.Command("sh", "-n")
			command.Stdin = strings.NewReader(script)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("shell syntax error: %v: %s", err, output)
			}
		})
	}
}

package provider

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

func testServiceNode(name string, enabled bool, state string) graph.Node {
	return graph.Node{Kind: "service", Desired: map[string]any{
		"name": name, "enabled": enabled, "runlevel": "default", "state": state,
		"package": "", "user": "", "group": "", "delete_behavior": "", "prevent_destroy": false,
	}}
}

func TestServiceProviderClassifiesRuntimeAndRunlevelDrift(t *testing.T) {
	node := testServiceNode("worker", true, "running")
	runner := &commandRunner{outputs: map[string][]byte{"inspect.service": []byte("service\ntrue\nstarted\n0\n")}}
	observed, err := inspectService(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || observed.Values["runtime_status"] != "started" || observed.Digest != corestate.Digest(node.Desired) {
		t.Fatalf("started service observation = %#v", observed)
	}
	runner.outputs["inspect.service"] = []byte("service\nfalse\ncrashed\n32\n")
	drifted, err := inspectService(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if drifted.Values["state"] != "crashed" || drifted.Values["enabled"] != false || drifted.Digest == corestate.Digest(node.Desired) {
		t.Fatalf("crashed service observation = %#v", drifted)
	}
}

func TestServiceProviderUsesSafeOpenRCArgumentsAndForgetsOnly(t *testing.T) {
	node := testServiceNode("worker", false, "stopped")
	runner := &commandRunner{outputs: map[string][]byte{"inspect.service": []byte("service\nfalse\nstopped\n3\n")}}
	if _, err := applyService(context.Background(), runner, engine.Step{Node: node}); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 || runner.commands[0].Name != "apply.service" || strings.Contains(runner.commands[0].Script, "worker") || strings.Join(runner.commands[0].Arguments, ",") != "worker,default,false,stopped,," || !strings.Contains(runner.commands[0].Script, "rc-update") || !strings.Contains(runner.commands[0].Script, "rc-service") {
		t.Fatalf("service commands = %#v", runner.commands)
	}
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return runner, nil }}
	if err := provider.Delete(context.Background(), engine.Step{Action: engine.ActionDestroy, Prior: &corestate.Resource{Kind: "service"}}); err == nil || !strings.Contains(err.Error(), "only be forgotten") {
		t.Fatalf("service orphan destroy error = %v", err)
	}
}

func TestServiceProviderRunsOneRequestedOperationForChangedFiles(t *testing.T) {
	node := testServiceNode("worker", true, "running")
	node.Desired["operation"] = "restarted"
	runner := &commandRunner{outputs: map[string][]byte{"inspect.service": []byte("service\ntrue\nstarted\n0\n")}}
	step := engine.Step{Action: engine.ActionUpdate, Node: node, TriggeredBy: []string{"init", "conf"}, Prior: &corestate.Resource{Observed: map[string]any{"runlevel": "boot"}}}
	if _, err := applyService(context.Background(), runner, step); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 || strings.Join(runner.commands[0].Arguments, ",") != "worker,default,true,running,restarted,boot" || !strings.Contains(runner.commands[0].Script, "does not support or failed operation") || !strings.Contains(runner.commands[0].Script, "reload_descriptions") || !strings.Contains(runner.commands[0].Script, "previous_runlevel") {
		t.Fatalf("triggered service commands = %#v", runner.commands)
	}
}

func TestServiceProviderRestartsCrashedServiceForRecovery(t *testing.T) {
	node := testServiceNode("worker", true, "running")
	runner := &commandRunner{outputs: map[string][]byte{"inspect.service": []byte("service\ntrue\nstarted\n0\n")}}
	step := engine.Step{Action: engine.ActionUpdate, Node: node, Observed: engine.ObservedResource{Values: map[string]any{"runtime_status": "crashed"}}}
	if _, err := applyService(context.Background(), runner, step); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(runner.commands[0].Arguments, ","); got != "worker,default,true,running,restarted," {
		t.Fatalf("crashed service recovery arguments = %q", got)
	}
}

func TestServiceProviderRejectsUnsafeIdentityAndScriptsHaveValidSyntax(t *testing.T) {
	if _, err := applyService(context.Background(), &commandRunner{}, engine.Step{Node: testServiceNode("worker;reboot", true, "running")}); err == nil {
		t.Fatal("unsafe service identity was accepted")
	}
	for name, script := range map[string]string{"inspect": serviceInspectScript, "apply": serviceApplyScript} {
		t.Run(name, func(t *testing.T) {
			command := exec.Command("sh", "-n")
			command.Stdin = strings.NewReader(script)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("shell syntax error: %v: %s", err, output)
			}
		})
	}
}

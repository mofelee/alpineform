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

func testKernelModuleNode(name string) graph.Node {
	return graph.Node{Kind: "kernel_module", Desired: map[string]any{"name": name, "persist": true, "delete_behavior": "", "prevent_destroy": false}}
}

func testSysctlNode(key, value string, applyRuntime bool) graph.Node {
	return graph.Node{Kind: "sysctl", Desired: map[string]any{
		"key": key, "value": value, "apply_runtime": applyRuntime, "delete_behavior": "delete", "delete": map[string]any{"key": key}, "prevent_destroy": false,
	}}
}

func TestKernelModuleProviderClassifiesAndPersists(t *testing.T) {
	node := testKernelModuleNode("br_netfilter")
	runner := &commandRunner{outputs: map[string][]byte{"inspect.kernel_module": []byte("module\nloaded\ntrue\n")}}
	observed, err := inspectKernelModule(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || observed.Values["class"] != "loaded" || observed.Digest != corestate.Digest(node.Desired) {
		t.Fatalf("loaded module = %#v", observed)
	}
	runner.outputs["inspect.kernel_module"] = []byte("module\nbuiltin\ntrue\n")
	builtin, err := inspectKernelModule(context.Background(), runner, node)
	if err != nil || builtin.Digest != corestate.Digest(node.Desired) {
		t.Fatalf("builtin module = %#v, %v", builtin, err)
	}
	runner.outputs["inspect.kernel_module"] = []byte("module\navailable\nfalse\n")
	available, err := inspectKernelModule(context.Background(), runner, node)
	if err != nil || !available.Exists || available.Digest != "" {
		t.Fatalf("available module = %#v, %v", available, err)
	}
	if _, err := applyKernelModule(context.Background(), runner, node); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(runner.commands[len(runner.commands)-2].Arguments, ","); got != "br_netfilter,/etc/modules-load.d/alpineform-br_netfilter.conf" {
		t.Fatalf("module apply arguments = %q", got)
	}
}

func TestSysctlProviderPersistsAndAggregatesRuntimeApply(t *testing.T) {
	node := testSysctlNode("net.ipv4.ip_forward", "1", true)
	runner := &commandRunner{outputs: map[string][]byte{"inspect.sysctl": []byte("sysctl\ntrue\ntrue\n1\ntrue\n")}}
	observed, err := inspectSysctl(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || observed.Digest != corestate.Digest(node.Desired) {
		t.Fatalf("sysctl observation = %#v", observed)
	}
	if _, err := applySysctl(context.Background(), runner, node); err != nil {
		t.Fatal(err)
	}
	runtime := graph.Node{Kind: "sysctl_runtime", Desired: map[string]any{
		"entries": []string{"net.ipv4.ip_forward", "1", "vm.swappiness", "10"}, "delete_behavior": "",
	}}
	if _, err := applySysctlRuntime(context.Background(), runner, runtime); err != nil {
		t.Fatal(err)
	}
	command := runner.commands[len(runner.commands)-1]
	if command.Name != "apply.sysctl_runtime" || strings.Join(command.Arguments, ",") != "net.ipv4.ip_forward,1,vm.swappiness,10" {
		t.Fatalf("runtime sysctl command = %#v", command)
	}
}

func TestKernelProviderRejectsUnsafeValuesAndDeletesOnlyOwnedSysctlFile(t *testing.T) {
	if _, err := applyKernelModule(context.Background(), &commandRunner{}, testKernelModuleNode("bad/name")); err == nil {
		t.Fatal("unsafe module name was accepted")
	}
	if _, err := applySysctl(context.Background(), &commandRunner{}, testSysctlNode("bad..key", "1", true)); err == nil {
		t.Fatal("unsafe sysctl key was accepted")
	}
	runner := &commandRunner{}
	step := engine.Step{Action: engine.ActionDestroy, Prior: &corestate.Resource{Kind: "sysctl", Delete: map[string]any{"key": "vm.swappiness"}}}
	if err := deleteSysctl(context.Background(), runner, step); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 || runner.commands[0].Name != "delete.sysctl" || !strings.HasPrefix(runner.commands[0].Arguments[0], "/etc/sysctl.d/99-alpineform-vm_swappiness-") {
		t.Fatalf("sysctl delete command = %#v", runner.commands)
	}
	for name, script := range map[string]string{
		"module inspect": kernelModuleInspectScript,
		"module apply":   kernelModuleApplyScript,
		"sysctl inspect": sysctlInspectScript,
		"sysctl persist": sysctlPersistScript,
		"sysctl runtime": sysctlRuntimeApplyScript,
		"sysctl delete":  sysctlDeleteScript,
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

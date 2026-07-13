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

func TestSystemHostnameProviderConvergesPersistentAndRuntimeValues(t *testing.T) {
	node := graph.Node{Kind: "system_hostname", Desired: map[string]any{"hostname": "edge.example", "delete_behavior": ""}}
	runner := &commandRunner{outputs: map[string][]byte{"inspect.system_hostname": []byte("hostname\ntrue\nedge.example\nedge.example\n")}}
	observed, err := inspectSystemHostname(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || observed.Digest != corestate.Digest(node.Desired) {
		t.Fatalf("hostname observation = %#v", observed)
	}
	if _, err := applySystemHostname(context.Background(), runner, node); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 3 || runner.commands[1].Name != "apply.system_hostname" || strings.Join(runner.commands[1].Arguments, ",") != "edge.example" || strings.Contains(runner.commands[1].Script, "edge.example") {
		t.Fatalf("hostname commands = %#v", runner.commands)
	}
	runner.outputs["inspect.system_hostname"] = []byte("hostname\ntrue\nold\nold\n")
	drifted, err := inspectSystemHostname(context.Background(), runner, node)
	if err != nil || drifted.Digest != "" {
		t.Fatalf("hostname drift = %#v, %v", drifted, err)
	}
}

func TestSystemTimezoneProviderRequiresZoneAndBothManagedPaths(t *testing.T) {
	node := graph.Node{Kind: "system_timezone", Desired: map[string]any{"timezone": "Asia/Shanghai", "delete_behavior": ""}}
	runner := &commandRunner{outputs: map[string][]byte{"inspect.system_timezone": []byte("timezone\ntrue\ntrue\ntrue\ntrue\n")}}
	observed, err := inspectSystemTimezone(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || observed.Digest != corestate.Digest(node.Desired) {
		t.Fatalf("timezone observation = %#v", observed)
	}
	if _, err := applySystemTimezone(context.Background(), runner, node); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 3 || runner.commands[1].Name != "apply.system_timezone" || strings.Join(runner.commands[1].Arguments, ",") != "Asia/Shanghai" || strings.Contains(runner.commands[1].Script, "Asia/Shanghai") {
		t.Fatalf("timezone commands = %#v", runner.commands)
	}
	runner.outputs["inspect.system_timezone"] = []byte("timezone\nfalse\nfalse\nfalse\nfalse\n")
	drifted, err := inspectSystemTimezone(context.Background(), runner, node)
	if err != nil || drifted.Digest != "" || drifted.Exists {
		t.Fatalf("timezone drift = %#v, %v", drifted, err)
	}
}

func TestSystemProviderRejectsUnsafeValuesAndOnlyForgetsDeclarations(t *testing.T) {
	if _, err := applySystemHostname(context.Background(), &commandRunner{}, graph.Node{Desired: map[string]any{"hostname": "bad_name"}}); err == nil {
		t.Fatal("unsafe hostname was accepted")
	}
	if _, err := applySystemTimezone(context.Background(), &commandRunner{}, graph.Node{Desired: map[string]any{"timezone": "../etc/passwd"}}); err == nil {
		t.Fatal("unsafe timezone was accepted")
	}
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return &commandRunner{}, nil }}
	for _, kind := range []string{"system_hostname", "system_timezone"} {
		if err := provider.Delete(context.Background(), engine.Step{Prior: &corestate.Resource{Kind: kind}}); err == nil || !strings.Contains(err.Error(), "only be forgotten") {
			t.Fatalf("%s deletion error = %v", kind, err)
		}
	}
	for name, script := range map[string]string{
		"hostname inspect": systemHostnameInspectScript,
		"hostname apply":   systemHostnameApplyScript,
		"timezone inspect": systemTimezoneInspectScript,
		"timezone apply":   systemTimezoneApplyScript,
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

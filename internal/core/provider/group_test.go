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

func testGroupNode(name, gid string, system bool) graph.Node {
	return graph.Node{
		Host:    "node",
		Address: `host.node.groups.group["` + name + `"]`,
		Kind:    "group",
		Managed: true,
		Desired: map[string]any{
			"name":            name,
			"gid":             gid,
			"system":          system,
			"ensure":          "present",
			"delete_behavior": "destroy",
			"delete":          map[string]any{"name": name},
			"prevent_destroy": false,
		},
		DigestSafe: true,
	}
}

func TestGroupProviderObservesExplicitGIDDrift(t *testing.T) {
	node := testGroupNode("app", "1500", true)
	runner := &commandRunner{outputs: map[string][]byte{"inspect.group": []byte("group\n1600\n")}}
	observed, err := inspectGroup(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || observed.Values["gid"] != "1600" || corestate.Digest(observed.Values) == corestate.Digest(node.Desired) {
		t.Fatalf("group drift observation = %#v", observed)
	}
	node.Desired["gid"] = ""
	observed, err = inspectGroup(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if corestate.Digest(observed.Values) != corestate.Digest(node.Desired) {
		t.Fatalf("unmanaged gid observation = %#v", observed)
	}
}

func TestGroupProviderUsesBusyBoxCommandsAndArguments(t *testing.T) {
	name := "app_group"
	node := testGroupNode(name, "1500", true)
	runner := &commandRunner{outputs: map[string][]byte{"inspect.group": []byte("group\n1500\n")}}
	if _, err := applyGroup(context.Background(), runner, node); err != nil {
		t.Fatal(err)
	}
	if err := deleteGroup(context.Background(), runner, engine.Step{Node: node}); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	apply := runner.commands[0]
	if apply.Name != "apply.group" || len(apply.Arguments) != 3 || apply.Arguments[0] != name || apply.Arguments[1] != "1500" || apply.Arguments[2] != "true" {
		t.Fatalf("apply group command = %#v", apply)
	}
	remove := runner.commands[2]
	if remove.Name != "delete.group" || len(remove.Arguments) != 1 || remove.Arguments[0] != name {
		t.Fatalf("delete group command = %#v", remove)
	}
	if strings.Contains(apply.Script, name) || strings.Contains(remove.Script, name) || !strings.Contains(apply.Script, "addgroup -S") || strings.Contains(apply.Script, "groupadd") || !strings.Contains(remove.Script, "delgroup") || strings.Contains(remove.Script, "groupdel") {
		t.Fatalf("group scripts do not follow the fixed BusyBox contract:\napply:\n%s\ndelete:\n%s", apply.Script, remove.Script)
	}
}

func TestGroupProviderDestroysOrphanFromStateIdentity(t *testing.T) {
	runner := &commandRunner{outputs: map[string][]byte{}}
	step := engine.Step{Action: engine.ActionDestroy, Prior: &corestate.Resource{Kind: "group", Delete: map[string]any{"name": "orphan"}}}
	if err := deleteGroup(context.Background(), runner, step); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 || runner.commands[0].Arguments[0] != "orphan" {
		t.Fatalf("orphan delete command = %#v", runner.commands)
	}
}

func TestGroupProviderRejectsInvalidIdentity(t *testing.T) {
	runner := &commandRunner{outputs: map[string][]byte{}}
	if _, err := applyGroup(context.Background(), runner, testGroupNode("bad.name", "1500", false)); err == nil {
		t.Fatal("invalid group name was accepted")
	}
	if _, err := applyGroup(context.Background(), runner, testGroupNode("app", "-1", false)); err == nil {
		t.Fatal("invalid gid was accepted")
	}
}

func TestGroupProviderScriptsUseValidShellSyntax(t *testing.T) {
	for name, script := range map[string]string{
		"inspect": groupInspectScript,
		"apply":   groupApplyScript,
		"delete":  groupDeleteScript,
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

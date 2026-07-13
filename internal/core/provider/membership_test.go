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

func testMembershipNode(user, group string) graph.Node {
	return graph.Node{
		Host: "node", Kind: "membership", Managed: true,
		Desired: map[string]any{
			"user": user, "group": group, "ensure": "present", "delete_behavior": "destroy",
			"delete": map[string]any{"user": user, "group": group},
		},
		DigestSafe: true,
	}
}

func TestMembershipProviderConvergesWithBusyBoxArguments(t *testing.T) {
	node := testMembershipNode("app", "wheel")
	runner := &commandRunner{outputs: map[string][]byte{"inspect.membership": []byte("membership\n")}}
	if _, err := applyMembership(context.Background(), runner, node); err != nil {
		t.Fatal(err)
	}
	if err := deleteMembership(context.Background(), runner, engine.Step{Node: node}); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	apply := runner.commands[0]
	remove := runner.commands[2]
	if apply.Name != "apply.membership" || strings.Join(apply.Arguments, ":") != "app:wheel" || remove.Name != "delete.membership" || strings.Join(remove.Arguments, ":") != "app:wheel" {
		t.Fatalf("membership commands = %#v", runner.commands)
	}
	if strings.Contains(apply.Script, "wheel") || !strings.Contains(apply.Script, "addgroup \"$user\" \"$group\"") || strings.Contains(apply.Script, "usermod") || !strings.Contains(remove.Script, "delgroup \"$user\" \"$group\"") {
		t.Fatalf("membership scripts do not follow BusyBox argv semantics:\n%s\n%s", apply.Script, remove.Script)
	}
}

func TestMembershipProviderDestroysOrphanFromStateIdentity(t *testing.T) {
	runner := &commandRunner{outputs: map[string][]byte{}}
	step := engine.Step{Prior: &corestate.Resource{Kind: "membership", Delete: map[string]any{"user": "app", "group": "wheel"}}}
	if err := deleteMembership(context.Background(), runner, step); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 || strings.Join(runner.commands[0].Arguments, ":") != "app:wheel" {
		t.Fatalf("orphan membership delete = %#v", runner.commands)
	}
}

func TestMembershipProviderScriptsUseValidShellSyntax(t *testing.T) {
	for name, script := range map[string]string{"inspect": membershipInspectScript, "apply": membershipApplyScript, "delete": membershipDeleteScript} {
		t.Run(name, func(t *testing.T) {
			command := exec.Command("sh", "-n")
			command.Stdin = strings.NewReader(script)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("shell syntax error: %v: %s", err, output)
			}
		})
	}
}

func TestMembershipProviderAWKMembershipScan(t *testing.T) {
	program := `{
  count=split($4, members, ",")
  for (i=1; i<=count; i++) {
    if (members[i] == user) found=1
  }
}
END { exit found ? 0 : 1 }`
	command := exec.Command("awk", "-F:", "-v", "user=app", program)
	command.Stdin = strings.NewReader("wheel:x:10:root,app\n")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("membership awk failed: %v: %s", err, output)
	}
}

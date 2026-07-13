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

func testUserNode(name string) graph.Node {
	return graph.Node{
		Host:    "node",
		Address: `host.node.users.user["` + name + `"]`,
		Kind:    "user",
		Managed: true,
		Desired: map[string]any{
			"name":            name,
			"uid":             "1500",
			"group":           "app",
			"home":            "/srv/app",
			"shell":           "/sbin/nologin",
			"system":          true,
			"ensure":          "present",
			"delete_behavior": "destroy",
			"delete":          map[string]any{"name": name},
			"prevent_destroy": false,
		},
		DigestSafe: true,
	}
}

func TestUserProviderObservesExplicitIdentityDrift(t *testing.T) {
	node := testUserNode("app")
	runner := &commandRunner{outputs: map[string][]byte{
		"inspect.user": []byte("user\n1600\n1601\nother\n/home/other\n/bin/ash\n"),
	}}
	observed, err := inspectUser(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || observed.Values["uid"] != "1600" || observed.Values["group"] != "other" || observed.Values["home"] != "/home/other" || observed.Values["shell"] != "/bin/ash" || corestate.Digest(observed.Values) == corestate.Digest(node.Desired) {
		t.Fatalf("user drift observation = %#v", observed)
	}
	node.Desired["uid"] = ""
	node.Desired["group"] = ""
	node.Desired["home"] = ""
	node.Desired["shell"] = ""
	observed, err = inspectUser(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if corestate.Digest(observed.Values) != corestate.Digest(node.Desired) {
		t.Fatalf("unmanaged user defaults = %#v", observed)
	}
}

func TestUserProviderUsesBusyBoxCommandsAndArguments(t *testing.T) {
	name := "app_user"
	node := testUserNode(name)
	runner := &commandRunner{outputs: map[string][]byte{
		"inspect.user": []byte("user\n1500\n1500\napp\n/srv/app\n/sbin/nologin\n"),
	}}
	if _, err := applyUser(context.Background(), runner, node); err != nil {
		t.Fatal(err)
	}
	if err := deleteUser(context.Background(), runner, engine.Step{Node: node}); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	apply := runner.commands[0]
	wantArguments := []string{name, "1500", "app", "/srv/app", "/sbin/nologin", "true"}
	if apply.Name != "apply.user" || strings.Join(apply.Arguments, "\x00") != strings.Join(wantArguments, "\x00") {
		t.Fatalf("apply user command = %#v", apply)
	}
	remove := runner.commands[2]
	if remove.Name != "delete.user" || len(remove.Arguments) != 1 || remove.Arguments[0] != name {
		t.Fatalf("delete user command = %#v", remove)
	}
	for _, value := range wantArguments[:5] {
		if value != "" && strings.Contains(apply.Script, value) {
			t.Fatalf("apply user script contains argument %q:\n%s", value, apply.Script)
		}
	}
	if !strings.Contains(apply.Script, "adduser") || strings.Contains(apply.Script, "useradd") || !strings.Contains(remove.Script, "deluser") || strings.Contains(remove.Script, "userdel") {
		t.Fatalf("user scripts do not follow the BusyBox contract:\napply:\n%s\ndelete:\n%s", apply.Script, remove.Script)
	}
}

func TestUserProviderDestroysOrphanFromStateIdentity(t *testing.T) {
	runner := &commandRunner{outputs: map[string][]byte{}}
	step := engine.Step{Action: engine.ActionDestroy, Prior: &corestate.Resource{Kind: "user", Delete: map[string]any{"name": "orphan"}}}
	if err := deleteUser(context.Background(), runner, step); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 || runner.commands[0].Arguments[0] != "orphan" {
		t.Fatalf("orphan delete command = %#v", runner.commands)
	}
}

func TestUserProviderRejectsInvalidIdentity(t *testing.T) {
	runner := &commandRunner{outputs: map[string][]byte{}}
	if _, err := applyUser(context.Background(), runner, testUserNode("root")); err == nil {
		t.Fatal("root user was accepted")
	}
	node := testUserNode("app")
	node.Desired["home"] = "relative"
	if _, err := applyUser(context.Background(), runner, node); err == nil {
		t.Fatal("relative home was accepted")
	}
}

func TestUserProviderScriptsUseValidShellSyntax(t *testing.T) {
	for name, script := range map[string]string{
		"inspect": userInspectScript,
		"apply":   userApplyScript,
		"delete":  userDeleteScript,
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

func TestUserDeleteMembershipScanIgnoresBusyBoxPrimaryEntry(t *testing.T) {
	program := `$3 != primary_gid {
  count=split($4, members, ",")
  for (i=1; i<=count; i++) {
    if (members[i] == name) found=1
  }
}
END { exit found ? 0 : 1 }`
	tests := []struct {
		name    string
		groups  string
		present bool
	}{
		{name: "primary only", groups: "af_primary:x:24000:af_user\n", present: false},
		{name: "supplementary", groups: "af_primary:x:24000:af_user\naf_extra:x:24001:af_user\n", present: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command("awk", "-F:", "-v", "name=af_user", "-v", "primary_gid=24000", program)
			command.Stdin = strings.NewReader(test.groups)
			output, err := command.CombinedOutput()
			if test.present && err != nil {
				t.Fatalf("supplementary membership was missed: %v: %s", err, output)
			}
			if !test.present && err == nil {
				t.Fatal("BusyBox primary membership was treated as supplementary")
			}
		})
	}
}

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

func testAuthorizedKeyNode() graph.Node {
	return graph.Node{
		Host: "node", Kind: "authorized_key", Managed: true,
		Desired: map[string]any{
			"user": "app", "fingerprint": "SHA256:test", "metadata_ok": true, "ensure": "present", "delete_behavior": "destroy",
			"delete": map[string]any{"user": "app", "key_type": "ssh-ed25519", "key_blob": "AAAA"},
		},
		Payload:    map[string]any{"line": "ssh-ed25519 AAAA test", "key_type": "ssh-ed25519", "key_blob": "AAAA"},
		DigestSafe: true,
	}
}

func TestAuthorizedKeyProviderObservesMetadataDrift(t *testing.T) {
	node := testAuthorizedKeyNode()
	runner := &commandRunner{outputs: map[string][]byte{"inspect.authorized_key": []byte("key\nfalse\n")}}
	observed, err := inspectAuthorizedKey(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || observed.Values["metadata_ok"] != false || corestate.Digest(observed.Values) == corestate.Digest(node.Desired) {
		t.Fatalf("authorized key drift = %#v", observed)
	}
}

func TestAuthorizedKeyProviderUsesOnlyFixedScriptsAndArguments(t *testing.T) {
	node := testAuthorizedKeyNode()
	runner := &commandRunner{outputs: map[string][]byte{"inspect.authorized_key": []byte("key\ntrue\n")}}
	if _, err := applyAuthorizedKey(context.Background(), runner, node); err != nil {
		t.Fatal(err)
	}
	if err := deleteAuthorizedKey(context.Background(), runner, engine.Step{Node: node}); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	apply := runner.commands[0]
	remove := runner.commands[2]
	if apply.Name != "apply.authorized_key" || strings.Join(apply.Arguments, "|") != "app|ssh-ed25519 AAAA test|ssh-ed25519|AAAA" || remove.Name != "delete.authorized_key" || strings.Join(remove.Arguments, "|") != "app|ssh-ed25519|AAAA" {
		t.Fatalf("authorized key commands = %#v", runner.commands)
	}
	for _, value := range apply.Arguments {
		if strings.Contains(apply.Script, value) {
			t.Fatalf("authorized key script contains argument %q", value)
		}
	}
}

func TestAuthorizedKeyProviderDestroysOrphanFromStateIdentity(t *testing.T) {
	runner := &commandRunner{outputs: map[string][]byte{}}
	step := engine.Step{Prior: &corestate.Resource{Kind: "authorized_key", Delete: map[string]any{"user": "app", "key_type": "ssh-ed25519", "key_blob": "AAAA"}}}
	if err := deleteAuthorizedKey(context.Background(), runner, step); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 || strings.Join(runner.commands[0].Arguments, "|") != "app|ssh-ed25519|AAAA" {
		t.Fatalf("orphan authorized key delete = %#v", runner.commands)
	}
}

func TestAuthorizedKeyProviderScriptsUseValidShellSyntax(t *testing.T) {
	for name, script := range map[string]string{"inspect": authorizedKeyInspectScript, "apply": authorizedKeyApplyScript, "delete": authorizedKeyDeleteScript} {
		t.Run(name, func(t *testing.T) {
			command := exec.Command("sh", "-n")
			command.Stdin = strings.NewReader(script)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("shell syntax error: %v: %s", err, output)
			}
		})
	}
}

func TestAuthorizedKeyProviderAWKMaterialScan(t *testing.T) {
	program := `{
  for (i=1; i<NF; i++) {
    if ($i == type && $(i+1) == blob) found=1
  }
}
END { exit found ? 0 : 1 }`
	command := exec.Command("awk", "-v", "type=ssh-ed25519", "-v", "blob=AAAA", program)
	command.Stdin = strings.NewReader("restrict ssh-ed25519 AAAA comment\n")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("authorized key awk failed: %v: %s", err, output)
	}
}

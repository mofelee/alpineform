package provider

import (
	"context"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

func testNftablesPersistenceNode(content string) graph.Node {
	path := nftablesPersistencePath("inet", "edge")
	return graph.Node{
		Host: "node", Address: `host.node.nftables.table["inet/edge"]`, Kind: "nftables_table", Managed: true,
		Desired: map[string]any{
			"family": "inet", "name": "edge", "ensure": "present", "adopt_existing": false,
			"content_write_only": false, "persistence_sha256": sha256String(content), "persistence_bytes": int64(len(content)),
			"persistence_owner": "root", "persistence_group": "root", "persistence_mode": "0600", "persistence_path": path,
			"observed_marker_path": nftablesObservedMarkerPath("inet", "edge"), "activation_fingerprint": strings.Repeat("a", 64),
			"rollback_timeout_seconds": int64(30),
			"delete":                   map[string]any{"family": "inet", "name": "edge", "persistence_path": path},
		},
		Payload: map[string]any{"persistence_content": content}, Sensitive: true, DigestSafe: true,
	}
}

func testNftablesServiceNode(initScript string) graph.Node {
	return graph.Node{Host: "node", Address: "host.node.nftables.service", Kind: "nftables_service", Managed: true,
		Desired: map[string]any{
			"name": nftablesOpenRCName, "runlevel": "default", "enabled": true, "state": "running",
			"init_path": nftablesOpenRCInitPath, "init_sha256": sha256String(initScript), "init_mode": "0755",
			"persistence_directory": nftablesPersistenceDirectory, "persistence_directory_mode": "0700",
			"ensure": "present", "delete_behavior": "",
		},
		Payload: map[string]any{"init_script": initScript}, DigestSafe: true,
	}
}

func TestNftablesProvidersClassifyDegradedPersistenceAndService(t *testing.T) {
	persistence := testNftablesPersistenceNode("table inet edge {}\n")
	persistenceRunner := &commandRunner{outputs: map[string][]byte{
		"inspect.nftables_persistence": []byte("symlink\n"),
		"inspect.nftables_active":      []byte("missing\n-\nmissing\n-\n-\n-\n"),
	}}
	observed, err := inspectNftablesPersistence(context.Background(), persistenceRunner, persistence)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || !observed.Protected || observed.Digest != "" || observed.Values["persistence_state"] != "symlink" {
		t.Fatalf("degraded persistence observation = %#v", observed)
	}

	initScript := "#!/sbin/openrc-run\nstart() { :; }\n"
	service := testNftablesServiceNode(initScript)
	serviceRunner := &commandRunner{outputs: map[string][]byte{
		"inspect.nftables_service": []byte("service\nsymlink\n-\n-\n-\nother\n-\n-\n-\n-\nfalse\ncrashed\n"),
	}}
	observed, err = inspectNftablesService(context.Background(), serviceRunner, service)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Exists || observed.Digest != "" || observed.Values["persistence_directory_state"] != "symlink" || observed.Values["init_state"] != "other" || observed.Values["state"] != "crashed" {
		t.Fatalf("degraded service observation = %#v", observed)
	}
}

func TestNftablesTransactionStagesSnapshotsActivatesAndReinspects(t *testing.T) {
	content := "# Managed by AlpineForm\ntable inet edge {\n\tchain input {}\n}\n"
	node := testNftablesPersistenceNode(content)
	activeSHA := strings.Repeat("b", 64)
	fingerprint := stringValue(node.Desired, "activation_fingerprint")
	runner := &commandRunner{outputs: map[string][]byte{
		"inspect.nftables_persistence": []byte("file\nroot\nroot\n600\n" + strconv.Itoa(len(content)) + "\n" + sha256String(content) + "\n"),
		"inspect.nftables_active":      []byte("present\n" + activeSHA + "\nfile\n" + fingerprint + "\n" + activeSHA + "\npresent\n"),
	}}
	token := strings.Repeat("c", 64)
	observed, err := applyNftablesTransaction(context.Background(), runner, engine.Step{Action: engine.ActionCreate, Node: node}, func() (string, error) {
		return token, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Digest != corestate.Digest(node.Desired) || !observed.Protected {
		t.Fatalf("transaction observation = %#v", observed)
	}
	if len(runner.commands) != 3 || runner.commands[0].Name != "apply.nftables_transaction" || runner.commands[0].Arguments[0] != token || runner.commands[0].Arguments[6] != "present" || string(runner.commands[0].Stdin) != content || !runner.commands[0].RedactStdin || !runner.commands[0].RedactOutput {
		t.Fatalf("transaction commands = %#v", runner.commands)
	}
	script := runner.commands[0].Script
	preflight := strings.Index(script, `nft -c -f "$activation"`)
	activate := strings.Index(script, `nft -f "$activation"`)
	snapshot := strings.Index(script, `nft --stateless list table "$family" "$name" >"$active_snapshot"`)
	if preflight < 0 || activate < 0 || snapshot < 0 || preflight > snapshot || snapshot > activate || strings.Contains(script, content) || strings.Contains(script, "flush ruleset") || !strings.Contains(script, "restore_transaction") || !strings.Contains(script, "persistent.snapshot") || !strings.Contains(script, "marker.snapshot") {
		t.Fatalf("transaction does not preflight/snapshot/rollback safely: %s", script)
	}
	untrackedRunner := &commandRunner{outputs: map[string][]byte{}}
	_, err = applyNftablesTransaction(context.Background(), untrackedRunner, engine.Step{
		Action: engine.ActionUpdate, Node: node, Observed: engine.ObservedResource{Exists: true},
	}, func() (string, error) { return token, nil })
	if err == nil || !strings.Contains(err.Error(), "adopt_existing") || len(untrackedRunner.commands) != 0 {
		t.Fatalf("implicit transaction adoption error = %v, commands = %#v", err, untrackedRunner.commands)
	}

	invalidTokenRunner := &commandRunner{outputs: map[string][]byte{}}
	_, err = applyNftablesTransaction(context.Background(), invalidTokenRunner, engine.Step{Action: engine.ActionCreate, Node: node}, func() (string, error) {
		return "not-a-valid-token", nil
	})
	if err == nil || !strings.Contains(err.Error(), "invalid token") || len(invalidTokenRunner.commands) != 0 {
		t.Fatalf("invalid token error = %v, commands = %#v", err, invalidTokenRunner.commands)
	}
}

func TestNftablesDeleteTransactionUsesOnlyRecordedIdentity(t *testing.T) {
	path := nftablesPersistencePath("inet", "edge")
	marker := nftablesObservedMarkerPath("inet", "edge")
	fingerprint := strings.Repeat("d", 64)
	prior := &corestate.Resource{Kind: "nftables_table", Ownership: "managed", Delete: map[string]any{
		"family": "inet", "name": "edge", "persistence_path": path,
		"observed_marker_path": marker, "activation_fingerprint": fingerprint,
		"rollback_timeout_seconds": int64(30),
	}}
	runner := &commandRunner{outputs: map[string][]byte{}}
	token := strings.Repeat("e", 64)
	if err := deleteNftablesTransaction(context.Background(), runner, engine.Step{Action: engine.ActionDelete, Prior: prior}, func() (string, error) {
		return token, nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 || !reflect.DeepEqual(runner.commands[0].Arguments, []string{token, "inet", "edge", path, marker, fingerprint, "absent"}) || len(runner.commands[0].Stdin) != 0 || !runner.commands[0].RedactOutput {
		t.Fatalf("delete transaction command = %#v", runner.commands)
	}
	if strings.Contains(runner.commands[0].Script, "flush ruleset") {
		t.Fatalf("delete transaction contains global flush: %s", runner.commands[0].Script)
	}
}

func TestNftablesServiceProviderUsesDedicatedNonFlushingOpenRCService(t *testing.T) {
	initScript := `#!/sbin/openrc-run
start() {
  [ -f /var/lib/alpineform/nftables/armed ] || return 0
  nft -c -f /etc/nftables.d/alpineform/inet-edge.nft
}
`
	node := testNftablesServiceNode(initScript)
	inspectOutput := []byte("service\ndirectory\n0\n0\n700\nfile\n0\n0\n755\n" + sha256String(initScript) + "\ntrue\nstarted\n")
	runner := &commandRunner{outputs: map[string][]byte{"inspect.nftables_service": inspectOutput}}
	observed, err := applyNftablesService(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if observed.Digest != corestate.Digest(node.Desired) || len(runner.commands) != 2 || !runner.commands[0].RedactStdin || string(runner.commands[0].Stdin) != initScript {
		t.Fatalf("service observation/commands = %#v / %#v", observed, runner.commands)
	}
	for _, forbidden := range []string{"flush ruleset", "/etc/nftables.nft", "/etc/conf.d/nftables", "/etc/init.d/nftables"} {
		if strings.Contains(initScript, forbidden) || strings.Contains(runner.commands[0].Script, forbidden) {
			t.Fatalf("service may mutate stock nftables path %q", forbidden)
		}
	}
	if !strings.Contains(runner.commands[0].Script, "rc-update add") || !strings.Contains(runner.commands[0].Script, "mv -f") {
		t.Fatalf("service apply is not atomic OpenRC convergence: %s", runner.commands[0].Script)
	}
	provider := Native{NewRunner: func(string) (backend.Runner, error) { return runner, nil }}
	if err := provider.Delete(context.Background(), engine.Step{Prior: &corestate.Resource{Kind: "nftables_service"}}); err == nil || !strings.Contains(err.Error(), "only be forgotten") {
		t.Fatalf("service delete error = %v", err)
	}
}

func TestNftablesProviderScriptsHaveValidShellSyntax(t *testing.T) {
	scripts := map[string]string{
		"persistence inspect": nftablesPersistenceInspectScript,
		"active inspect":      nftablesActiveInspectScript,
		"transaction":         nftablesTransactionScript,
		"service inspect":     nftablesServiceInspectScript,
		"service apply":       nftablesServiceApplyScript,
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

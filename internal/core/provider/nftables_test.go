package provider

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
			"delete": map[string]any{"family": "inet", "name": "edge", "persistence_path": path},
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

func TestNftablesPersistenceProviderConvergesAndProtectsAdoption(t *testing.T) {
	content := "# Managed by AlpineForm\ntable inet edge {\n\tchain input {}\n}\n"
	node := testNftablesPersistenceNode(content)
	inspectOutput := []byte("file\nroot\nroot\n600\n" + strconv.Itoa(len(content)) + "\n" + sha256String(content) + "\n")
	runner := &commandRunner{outputs: map[string][]byte{"inspect.nftables_persistence": inspectOutput}}
	observed, err := applyNftablesPersistence(context.Background(), runner, engine.Step{Action: engine.ActionCreate, Node: node})
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 || runner.commands[0].Name != "apply.nftables_persistence" || !runner.commands[0].RedactStdin || !runner.commands[0].RedactOutput || string(runner.commands[0].Stdin) != content {
		t.Fatalf("persistence commands = %#v", runner.commands)
	}
	if strings.Contains(runner.commands[0].Script, content) || !strings.Contains(runner.commands[0].Script, "mv -f") || !strings.Contains(runner.commands[0].Script, "refusing symbolic-link") {
		t.Fatalf("unsafe persistence write script: %s", runner.commands[0].Script)
	}
	if !observed.Protected || observed.Digest != corestate.Digest(node.Desired) {
		t.Fatalf("persistence observation = %#v", observed)
	}

	untracked := &commandRunner{outputs: map[string][]byte{}}
	_, err = applyNftablesPersistence(context.Background(), untracked, engine.Step{Action: engine.ActionUpdate, Node: node, Observed: engine.ObservedResource{Exists: true}})
	if err == nil || !strings.Contains(err.Error(), "adopt_existing") || len(untracked.commands) != 0 {
		t.Fatalf("untracked persistence error = %v, commands = %#v", err, untracked.commands)
	}
	node.Desired["adopt_existing"] = true
	adopted := &commandRunner{outputs: map[string][]byte{"inspect.nftables_persistence": inspectOutput}}
	if _, err := applyNftablesPersistence(context.Background(), adopted, engine.Step{Action: engine.ActionUpdate, Node: node, Observed: engine.ObservedResource{Exists: true}}); err != nil {
		t.Fatal(err)
	}
}

func TestNftablesProvidersClassifyDegradedPersistenceAndService(t *testing.T) {
	persistence := testNftablesPersistenceNode("table inet edge {}\n")
	persistenceRunner := &commandRunner{outputs: map[string][]byte{"inspect.nftables_persistence": []byte("symlink\n")}}
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

func TestNftablesPersistenceScriptsReplaceAtomicallyAndRejectSymlinks(t *testing.T) {
	root := t.TempDir()
	owner := strconv.Itoa(os.Getuid())
	group := strconv.Itoa(os.Getgid())
	base := filepath.Join(root, "nftables.d")
	directory := filepath.Join(base, "alpineform")
	path := filepath.Join(directory, "inet-edge.nft")
	content := "table inet edge {}\n"
	runner := localRunner{}
	if _, err := runner.Run(context.Background(), backend.Command{
		Script: nftablesPersistenceWriteScript, Arguments: []string{base, directory, path, owner, group}, Stdin: []byte(content),
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != content {
		t.Fatalf("persistent content = %q, error = %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("persistent mode = %v, error = %v", info.Mode(), err)
	}
	external := filepath.Join(root, "external")
	if err := os.WriteFile(external, []byte("external\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, path); err != nil {
		t.Fatal(err)
	}
	_, err = runner.Run(context.Background(), backend.Command{
		Script: nftablesPersistenceWriteScript, Arguments: []string{base, directory, path, owner, group}, Stdin: []byte("replacement\n"),
	})
	if err == nil || !strings.Contains(err.Error(), "symbolic-link") {
		t.Fatalf("symlink replacement error = %v", err)
	}
	data, err = os.ReadFile(external)
	if err != nil || string(data) != "external\n" {
		t.Fatalf("external symlink target changed: %q, error = %v", data, err)
	}
}

func TestNftablesPersistenceDeleteRequiresRecordedOwnership(t *testing.T) {
	node := testNftablesPersistenceNode("table inet edge {}\n")
	node.Desired["ensure"] = "absent"
	runner := &commandRunner{outputs: map[string][]byte{}}
	step := engine.Step{Action: engine.ActionDelete, Node: node}
	if err := deleteNftablesPersistence(context.Background(), runner, step); err == nil || !strings.Contains(err.Error(), "recorded") {
		t.Fatalf("unrecorded delete error = %v", err)
	}
	step.Prior = &corestate.Resource{Kind: "nftables_table", Ownership: "managed", Delete: node.Desired["delete"].(map[string]any)}
	if err := deleteNftablesPersistence(context.Background(), runner, step); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 || runner.commands[0].Name != "delete.nftables_persistence" || !reflect.DeepEqual(runner.commands[0].Arguments, []string{nftablesPersistenceDirectory, nftablesPersistencePath("inet", "edge")}) {
		t.Fatalf("delete command = %#v", runner.commands)
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
		"persistence write":   nftablesPersistenceWriteScript,
		"persistence delete":  nftablesPersistenceDeleteScript,
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

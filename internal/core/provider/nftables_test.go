package provider

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

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
	prepareRunner := &commandRunner{outputs: map[string][]byte{}}
	confirmationRunner := &commandRunner{outputs: map[string][]byte{
		"inspect.nftables_persistence": []byte("file\nroot\nroot\n600\n" + strconv.Itoa(len(content)) + "\n" + sha256String(content) + "\n"),
		"inspect.nftables_active":      []byte("present\n" + activeSHA + "\nfile\n" + fingerprint + "\n" + activeSHA + "\npresent\n"),
	}}
	token := strings.Repeat("c", 64)
	freshCalls := 0
	observed, err := applyNftablesTransaction(context.Background(), prepareRunner, func() (backend.Runner, error) {
		freshCalls++
		return confirmationRunner, nil
	}, engine.Step{Action: engine.ActionCreate, Node: node}, nftablesTransactionRuntime{NewToken: func() (string, error) {
		return token, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Digest != corestate.Digest(node.Desired) || !observed.Protected || !observed.Managed {
		t.Fatalf("transaction observation = %#v", observed)
	}
	if freshCalls != 1 || len(prepareRunner.commands) != 1 || len(confirmationRunner.commands) != 3 {
		t.Fatalf("transaction runner split = fresh %d, prepare %#v, confirmation %#v", freshCalls, prepareRunner.commands, confirmationRunner.commands)
	}
	prepare := prepareRunner.commands[0]
	if prepare.Name != "apply.nftables_transaction_prepare" || prepare.Arguments[0] != token || prepare.Arguments[6] != "present" || prepare.Arguments[7] != "30" || string(prepare.Stdin) != content || !prepare.RedactStdin || !prepare.RedactOutput {
		t.Fatalf("prepare command = %#v", prepare)
	}
	confirmation := confirmationRunner.commands[0]
	if confirmation.Name != "apply.nftables_transaction_confirm" || confirmation.Arguments[0] != token || confirmation.Arguments[6] != "present" || len(confirmation.Stdin) != 0 || !confirmation.RedactOutput {
		t.Fatalf("confirmation command = %#v", confirmation)
	}
	script := prepare.Script
	preflight := strings.Index(script, `nft -c -f "$activation"`)
	activate := strings.Index(script, `nft -f "$activation"`)
	snapshot := strings.Index(script, `nft --stateless list table "$family" "$name" >"$active_snapshot"`)
	watchdog := strings.Index(script, `exec nohup setsid sh ./watchdog.sh`)
	if preflight < 0 || activate < 0 || snapshot < 0 || watchdog < 0 || preflight > snapshot || snapshot > watchdog || watchdog > activate || strings.Contains(script, content) || strings.Contains(script, "flush ruleset") || !strings.Contains(script, "rollback_pending") || !strings.Contains(script, "persistent.snapshot") || !strings.Contains(script, "marker.snapshot") || !strings.Contains(script, "arming.snapshot") {
		t.Fatalf("transaction does not preflight/snapshot/rollback safely: %s", script)
	}
	if strings.Contains(script[:activate], `mv -f "$tmp" "$persistence"`) || !strings.Contains(confirmation.Script, `atomic_copy "$candidate" "$persistence"`) || !strings.Contains(confirmation.Script, `"$transaction/confirmed"`) {
		t.Fatalf("persistence or confirmation crossed transaction phases")
	}
	untrackedRunner := &commandRunner{outputs: map[string][]byte{}}
	_, err = applyNftablesTransaction(context.Background(), untrackedRunner, func() (backend.Runner, error) {
		return confirmationRunner, nil
	}, engine.Step{
		Action: engine.ActionUpdate, Node: node, Observed: engine.ObservedResource{Exists: true},
	}, nftablesTransactionRuntime{NewToken: func() (string, error) { return token, nil }})
	if err == nil || !strings.Contains(err.Error(), "adopt_existing") || len(untrackedRunner.commands) != 0 {
		t.Fatalf("implicit transaction adoption error = %v, commands = %#v", err, untrackedRunner.commands)
	}

	invalidTokenRunner := &commandRunner{outputs: map[string][]byte{}}
	_, err = applyNftablesTransaction(context.Background(), invalidTokenRunner, func() (backend.Runner, error) {
		return confirmationRunner, nil
	}, engine.Step{Action: engine.ActionCreate, Node: node}, nftablesTransactionRuntime{NewToken: func() (string, error) {
		return "not-a-valid-token", nil
	}})
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
	prepareRunner := &commandRunner{outputs: map[string][]byte{}}
	confirmationRunner := &commandRunner{outputs: map[string][]byte{}}
	token := strings.Repeat("e", 64)
	if err := deleteNftablesTransaction(context.Background(), prepareRunner, func() (backend.Runner, error) {
		return confirmationRunner, nil
	}, engine.Step{Action: engine.ActionDelete, Prior: prior}, nftablesTransactionRuntime{NewToken: func() (string, error) {
		return token, nil
	}}); err != nil {
		t.Fatal(err)
	}
	prepareArguments := []string{token, "inet", "edge", path, marker, fingerprint, "absent", "30"}
	confirmArguments := []string{token, "inet", "edge", path, marker, fingerprint, "absent"}
	if len(prepareRunner.commands) != 1 || !reflect.DeepEqual(prepareRunner.commands[0].Arguments, prepareArguments) || len(prepareRunner.commands[0].Stdin) != 0 || !prepareRunner.commands[0].RedactOutput {
		t.Fatalf("delete prepare command = %#v", prepareRunner.commands)
	}
	if len(confirmationRunner.commands) != 1 || !reflect.DeepEqual(confirmationRunner.commands[0].Arguments, confirmArguments) || !confirmationRunner.commands[0].RedactOutput {
		t.Fatalf("delete confirmation command = %#v", confirmationRunner.commands)
	}
	if strings.Contains(prepareRunner.commands[0].Script, "flush ruleset") || strings.Contains(confirmationRunner.commands[0].Script, "flush ruleset") {
		t.Fatalf("delete transaction contains global flush")
	}
}

func TestNftablesConfirmationFailureLeavesWatchdogArmedWithoutLeakingProtectedData(t *testing.T) {
	content := "# Managed by AlpineForm\ntable inet edge {}\n"
	node := testNftablesPersistenceNode(content)
	token := strings.Repeat("f", 64)
	prepareRunner := &commandRunner{outputs: map[string][]byte{}}
	confirmationRunner := &commandRunner{errors: map[string]error{
		"apply.nftables_transaction_confirm": errors.New("management connection lost"),
	}, outputs: map[string][]byte{"inspect.nftables_transaction_outcome": []byte("pending\n")}}
	now := time.Unix(100, 0)
	_, err := applyNftablesTransaction(context.Background(), prepareRunner, func() (backend.Runner, error) {
		return confirmationRunner, nil
	}, engine.Step{Action: engine.ActionCreate, Node: node}, nftablesTransactionRuntime{
		NewToken: func() (string, error) { return token, nil },
		Now:      func() time.Time { return now },
		Wait: func(context.Context, time.Duration) error {
			now = now.Add(time.Minute)
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "rollback status: pending") {
		t.Fatalf("confirmation failure = %v", err)
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), content) {
		t.Fatalf("confirmation failure leaked protected data: %v", err)
	}
	if len(prepareRunner.commands) != 1 || len(confirmationRunner.commands) < 2 {
		t.Fatalf("confirmation failure commands = prepare %#v, confirmation %#v", prepareRunner.commands, confirmationRunner.commands)
	}
	if !strings.Contains(prepareRunner.commands[0].Script, "awaiting_confirmation") || !strings.Contains(prepareRunner.commands[0].Script, "rollback_pending") {
		t.Fatalf("prepare command did not leave an armed watchdog")
	}
}

func TestNftablesReconnectRetriesAndReportsConfirmedRollback(t *testing.T) {
	content := "# Managed by AlpineForm\ntable inet edge {}\n"
	node := testNftablesPersistenceNode(content)
	token := strings.Repeat("1", 64)
	prepareRunner := &commandRunner{}
	runners := []*commandRunner{
		{errors: map[string]error{"apply.nftables_transaction_confirm": errors.New("connection lost")}},
		{outputs: map[string][]byte{"inspect.nftables_transaction_outcome": []byte("pending\n")}},
		{errors: map[string]error{"apply.nftables_transaction_confirm": errors.New("connection lost again")}},
		{outputs: map[string][]byte{"inspect.nftables_transaction_outcome": []byte("rollback_confirmed\n")}},
	}
	freshCalls := 0
	now := time.Unix(200, 0)
	_, err := applyNftablesTransaction(context.Background(), prepareRunner, func() (backend.Runner, error) {
		runner := runners[freshCalls]
		freshCalls++
		return runner, nil
	}, engine.Step{Action: engine.ActionCreate, Node: node}, nftablesTransactionRuntime{
		NewToken: func() (string, error) { return token, nil },
		Now:      func() time.Time { return now },
		Wait: func(context.Context, time.Duration) error {
			now = now.Add(time.Second)
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "rollback status: confirmed") || freshCalls != 4 {
		t.Fatalf("confirmed rollback error=%v fresh calls=%d", err, freshCalls)
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "connection lost") {
		t.Fatalf("confirmed rollback leaked protected transport data: %v", err)
	}
}

func TestNftablesLostConfirmationResponseUsesDurableConfirmedOutcome(t *testing.T) {
	content := "# Managed by AlpineForm\ntable inet edge {}\n"
	node := testNftablesPersistenceNode(content)
	token := strings.Repeat("2", 64)
	activeSHA := strings.Repeat("3", 64)
	fingerprint := stringValue(node.Desired, "activation_fingerprint")
	prepareRunner := &commandRunner{}
	confirmRunner := &commandRunner{errors: map[string]error{
		"apply.nftables_transaction_confirm": errors.New("response lost after commit"),
	}}
	outcomeRunner := &commandRunner{outputs: map[string][]byte{
		"inspect.nftables_transaction_outcome": []byte("confirmed\n"),
		"inspect.nftables_persistence":         []byte("file\nroot\nroot\n600\n" + strconv.Itoa(len(content)) + "\n" + sha256String(content) + "\n"),
		"inspect.nftables_active":              []byte("present\n" + activeSHA + "\nfile\n" + fingerprint + "\n" + activeSHA + "\npresent\n"),
	}}
	runners := []backend.Runner{confirmRunner, outcomeRunner}
	freshCalls := 0
	observed, err := applyNftablesTransaction(context.Background(), prepareRunner, func() (backend.Runner, error) {
		runner := runners[freshCalls]
		freshCalls++
		return runner, nil
	}, engine.Step{Action: engine.ActionCreate, Node: node}, nftablesTransactionRuntime{
		NewToken: func() (string, error) { return token, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if freshCalls != 2 || observed.Digest != corestate.Digest(node.Desired) || len(outcomeRunner.commands) != 3 {
		t.Fatalf("durable confirmation observed=%#v calls=%d commands=%#v", observed, freshCalls, outcomeRunner.commands)
	}
}

func TestNftablesOutcomeErrorsAreExplicitAndProtected(t *testing.T) {
	content := "# Managed by AlpineForm\ntable inet edge {}\n"
	node := testNftablesPersistenceNode(content)
	token := strings.Repeat("4", 64)
	secretError := "transport-secret-must-not-leak"
	for _, test := range []struct {
		name       string
		prepareErr error
		outcome    string
		want       string
	}{
		{name: "activation failure", prepareErr: errors.New(secretError), outcome: "activation_failed", want: "rollback status: not required"},
		{name: "rollback failure", outcome: "rollback_failed", want: "rollback status: failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			prepareRunner := &commandRunner{errors: map[string]error{"apply.nftables_transaction_prepare": test.prepareErr}}
			outcomeRunner := &commandRunner{outputs: map[string][]byte{"inspect.nftables_transaction_outcome": []byte(test.outcome + "\n")}}
			if test.prepareErr == nil {
				outcomeRunner.errors = map[string]error{"apply.nftables_transaction_confirm": errors.New(secretError)}
			}
			_, err := applyNftablesTransaction(context.Background(), prepareRunner, func() (backend.Runner, error) {
				return outcomeRunner, nil
			}, engine.Step{Action: engine.ActionCreate, Node: node}, nftablesTransactionRuntime{
				NewToken: func() (string, error) { return token, nil },
			})
			if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), secretError) || strings.Contains(err.Error(), token) {
				t.Fatalf("protected outcome error = %v", err)
			}
		})
	}
}

func TestNftablesCanceledConfirmationReturnsPendingWithoutReconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	freshCalls := 0
	_, err := confirmOrRecoverNftablesTransaction(ctx, func() (backend.Runner, error) {
		freshCalls++
		return &commandRunner{}, nil
	}, strings.Repeat("5", 64), "inet", "edge", nftablesPersistencePath("inet", "edge"), nftablesObservedMarkerPath("inet", "edge"), strings.Repeat("6", 64), "present", 30, true, nftablesTransactionRuntime{}.normalized())
	if err == nil || !strings.Contains(err.Error(), "rollback status: pending") || freshCalls != 0 {
		t.Fatalf("canceled confirmation error=%v fresh calls=%d", err, freshCalls)
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
	watchdogStart := strings.Index(nftablesTransactionPrepareScript, "#!/bin/sh\n")
	watchdogEnd := strings.Index(nftablesTransactionPrepareScript, "\nALPINEFORM_NFTABLES_WATCHDOG\n")
	if watchdogStart < 0 || watchdogEnd <= watchdogStart {
		t.Fatal("embedded nftables watchdog script is missing")
	}
	scripts := map[string]string{
		"persistence inspect": nftablesPersistenceInspectScript,
		"active inspect":      nftablesActiveInspectScript,
		"transaction prepare": nftablesTransactionPrepareScript,
		"embedded watchdog":   nftablesTransactionPrepareScript[watchdogStart:watchdogEnd],
		"transaction confirm": nftablesTransactionConfirmScript,
		"transaction outcome": nftablesTransactionOutcomeScript,
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

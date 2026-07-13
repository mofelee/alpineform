package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corestate "github.com/mofelee/alpineform/internal/core/state"
)

type localShellRunner struct{}

func (localShellRunner) Run(ctx context.Context, command Command) ([]byte, error) {
	process := exec.CommandContext(ctx, "sh", "-c", command.Script)
	process.Stdin = bytes.NewReader(command.Stdin)
	output, err := process.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, output)
	}
	return output, nil
}

type recordingRunner struct {
	output   []byte
	err      error
	commands []Command
}

func (runner *recordingRunner) Run(_ context.Context, command Command) ([]byte, error) {
	copyCommand := command
	copyCommand.Stdin = append([]byte(nil), command.Stdin...)
	runner.commands = append(runner.commands, copyCommand)
	return append([]byte(nil), runner.output...), runner.err
}

func TestStateStoreReadValidatesAlpineFormEnvelope(t *testing.T) {
	valid, err := corestate.Encode(corestate.Empty("node"))
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{output: valid}
	store := StateStore{Runner: runner}
	state, err := store.Read(context.Background(), "node")
	if err != nil {
		t.Fatal(err)
	}
	if state.Product != corestate.Product || state.Host != "node" {
		t.Fatalf("state = %#v", state)
	}
	if len(runner.commands) != 1 || runner.commands[0].Name != "state.read" || runner.commands[0].Stdin != nil || !runner.commands[0].RedactOutput {
		t.Fatalf("commands = %#v", runner.commands)
	}
	for _, want := range []string{"set -eu", "/var/lib/alpineform/state.json", "[ -f", "cat"} {
		if !strings.Contains(runner.commands[0].Script, want) {
			t.Fatalf("read script missing %q:\n%s", want, runner.commands[0].Script)
		}
	}
}

func TestStateStoreReadRejectsForeignState(t *testing.T) {
	runner := &recordingRunner{output: []byte(`{"product":"debianform","schema_version":1,"host":"node","resources":{}}`)}
	_, err := (StateStore{Runner: runner}).Read(context.Background(), "node")
	if err == nil || !strings.Contains(err.Error(), "refusing foreign state") {
		t.Fatalf("Read() error = %v", err)
	}
}

func TestStateStoreWriteIsAtomicAndAdvancesSerial(t *testing.T) {
	runner := &recordingRunner{}
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	store := StateStore{Runner: runner, Now: func() time.Time { return now }}
	input := corestate.Empty("node")
	input.Serial = 7
	written, err := store.Write(context.Background(), "node", input)
	if err != nil {
		t.Fatal(err)
	}
	if written.Serial != 8 || written.UpdatedAt != "2026-07-13T08:00:00Z" || input.Serial != 7 || input.UpdatedAt != "" {
		t.Fatalf("written = %#v, input = %#v", written, input)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	command := runner.commands[0]
	if command.Name != "state.write" || !command.RedactStdin || !command.RedactOutput || len(command.Stdin) == 0 {
		t.Fatalf("write command = %#v", command)
	}
	for _, want := range []string{"umask 077", "mkdir -p '/var/lib/alpineform'", "mktemp '/var/lib/alpineform/.state.json.tmp.XXXXXX'", "trap cleanup", "cat >\"$tmp\"", "chmod 0600", "mv -f \"$tmp\" '/var/lib/alpineform/state.json'", "trap - EXIT"} {
		if !strings.Contains(command.Script, want) {
			t.Fatalf("write script missing %q:\n%s", want, command.Script)
		}
	}
	var encoded corestate.State
	if err := json.Unmarshal(command.Stdin, &encoded); err != nil {
		t.Fatal(err)
	}
	if encoded.Serial != 8 || encoded.Product != corestate.Product || encoded.Host != "node" {
		t.Fatalf("encoded state = %#v", encoded)
	}
}

func TestStateStoreWriteFailureDoesNotMutateInput(t *testing.T) {
	runner := &recordingRunner{err: errors.New("connection lost")}
	store := StateStore{Runner: runner, Now: func() time.Time { return time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC) }}
	input := corestate.Empty("node")
	input.Serial = 4
	_, err := store.Write(context.Background(), "node", input)
	if err == nil || !strings.Contains(err.Error(), "atomically write remote AlpineForm state") {
		t.Fatalf("Write() error = %v", err)
	}
	if input.Serial != 4 || input.UpdatedAt != "" {
		t.Fatalf("Write() mutated input: %#v", input)
	}
}

func TestStateStoreRejectsInvalidPathBeforeRunner(t *testing.T) {
	runner := &recordingRunner{}
	store := StateStore{Runner: runner, Path: "relative/state.json"}
	_, err := store.Read(context.Background(), "node")
	if err == nil || !strings.Contains(err.Error(), "clean absolute file path") {
		t.Fatalf("Read() error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("runner was called: %#v", runner.commands)
	}
}

func TestStateStoreAtomicScriptRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "state.json")
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	store := StateStore{Runner: localShellRunner{}, Path: path, Now: func() time.Time { return now }}
	written, err := store.Write(context.Background(), "node", corestate.Empty("node"))
	if err != nil {
		t.Fatal(err)
	}
	read, err := store.Read(context.Background(), "node")
	if err != nil {
		t.Fatal(err)
	}
	if read.Serial != written.Serial || read.UpdatedAt != written.UpdatedAt {
		t.Fatalf("round trip = %#v, written = %#v", read, written)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("state mode = %o, want 600", got)
	}
	temporary, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".state.json.tmp.*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary state files remain: %#v", temporary)
	}
}

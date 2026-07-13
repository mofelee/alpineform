package backend

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mofelee/alpineform/internal/core/ir"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

type remoteBackendRunner struct {
	commands []Command
}

func (runner *remoteBackendRunner) Run(_ context.Context, command Command) ([]byte, error) {
	runner.commands = append(runner.commands, command)
	switch command.Name {
	case "state.read":
		data, err := json.Marshal(corestate.Empty("node"))
		return data, err
	case "state.write":
		return nil, nil
	case "lock.acquire":
		return []byte("acquired\n"), nil
	case "lock.release":
		return []byte("released\n"), nil
	default:
		return nil, errors.New("unexpected remote backend command: " + command.Name)
	}
}

func remoteBackendHost() ir.HostSpec {
	return ir.HostSpec{
		Name:  "node",
		SSH:   ir.SSHSpec{Host: "alpine-alias", User: "root"},
		State: ir.StateSpec{Path: "/custom/state.json", LockPath: "/run/custom/lock"},
	}
}

func TestRemoteBackendRoutesStateAndLeaseToHostRunner(t *testing.T) {
	runner := &remoteBackendRunner{}
	var factoryHosts []ir.HostSpec
	backend := RemoteBackend{
		NewRunner: func(host ir.HostSpec) (Runner, error) {
			factoryHosts = append(factoryHosts, host)
			return runner, nil
		},
		LeaseTTL: time.Hour,
		StateNow: func() time.Time { return time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC) },
		LeaseNow: func() time.Time { return time.Unix(100, 0) },
		NewLeaseToken: func() (string, error) {
			return "00000000000000000000000000000001", nil
		},
	}
	host := remoteBackendHost()
	state, err := backend.Read(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	written, err := backend.Write(context.Background(), host, state)
	if err != nil {
		t.Fatal(err)
	}
	workCalled := false
	if err := backend.WithLease(context.Background(), host, 0, func(context.Context) error {
		workCalled = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if written.Serial != 1 || !workCalled {
		t.Fatalf("written=%#v workCalled=%v", written, workCalled)
	}
	if len(factoryHosts) != 3 {
		t.Fatalf("runner factory hosts = %#v", factoryHosts)
	}
	for _, got := range factoryHosts {
		if !reflect.DeepEqual(got.SSH, host.SSH) || !reflect.DeepEqual(got.State, host.State) {
			t.Fatalf("runner factory host = %#v, want %#v", got, host)
		}
	}
	names := make([]string, 0, len(runner.commands))
	for _, command := range runner.commands {
		names = append(names, command.Name)
	}
	if !reflect.DeepEqual(names, []string{"state.read", "state.write", "lock.acquire", "lock.release"}) {
		t.Fatalf("commands = %#v", names)
	}
	if !strings.Contains(runner.commands[0].Script, "'/custom/state.json'") || !strings.Contains(runner.commands[1].Script, "'/custom/state.json'") {
		t.Fatalf("state scripts did not use host path: %#v", runner.commands[:2])
	}
	if !strings.Contains(runner.commands[2].Script, "'/run/custom/lock'") || !strings.Contains(runner.commands[3].Script, "'/run/custom/lock'") {
		t.Fatalf("lock scripts did not use host path: %#v", runner.commands[2:])
	}
}

func TestRemoteBackendRejectsMissingOrFailedRunnerFactory(t *testing.T) {
	host := remoteBackendHost()
	if _, err := (RemoteBackend{}).Read(context.Background(), host); err == nil || !strings.Contains(err.Error(), "runner factory") {
		t.Fatalf("missing factory error = %v", err)
	}
	want := errors.New("transport setup failed")
	backend := RemoteBackend{NewRunner: func(ir.HostSpec) (Runner, error) { return nil, want }}
	if _, err := backend.Read(context.Background(), host); !errors.Is(err, want) {
		t.Fatalf("factory error = %v", err)
	}
	backend.NewRunner = func(ir.HostSpec) (Runner, error) { return nil, nil }
	if _, err := backend.Read(context.Background(), host); err == nil || !strings.Contains(err.Error(), "returned nil") {
		t.Fatalf("nil runner error = %v", err)
	}
}

func TestRemoteBackendValidatesLeaseWorkBeforeOpeningTransport(t *testing.T) {
	factoryCalled := false
	backend := RemoteBackend{NewRunner: func(ir.HostSpec) (Runner, error) {
		factoryCalled = true
		return &remoteBackendRunner{}, nil
	}}
	if err := backend.WithLease(context.Background(), remoteBackendHost(), 0, nil); err == nil || !strings.Contains(err.Error(), "work function") {
		t.Fatalf("nil work error = %v", err)
	}
	if factoryCalled {
		t.Fatal("runner factory called for invalid lease work")
	}
}

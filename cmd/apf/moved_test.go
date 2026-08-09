package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	coreengine "github.com/mofelee/alpineform/internal/core/engine"
	coregraph "github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

const movedCLISecret = "not-a-real-moved-component-secret"

type movedCLIProvider struct {
	inspectErr error
	applied    int
	deleted    int
}

func (provider *movedCLIProvider) Inspect(_ context.Context, node coregraph.Node) (coreengine.ObservedResource, error) {
	if provider.inspectErr != nil {
		return coreengine.ObservedResource{}, provider.inspectErr
	}
	observed := coreengine.ObservedResource{
		Exists:    true,
		Digest:    corestate.Digest(node.Desired),
		Protected: node.Sensitive || node.Ephemeral,
	}
	if observed.Protected {
		observed.Values = map[string]any{"protected_value": movedCLISecret}
	}
	return observed, nil
}

func (provider *movedCLIProvider) Apply(_ context.Context, step coreengine.Step) (coreengine.ObservedResource, error) {
	provider.applied++
	observed := coreengine.ObservedResource{
		Exists:    true,
		Digest:    corestate.Digest(step.Node.Desired),
		Protected: step.Node.Sensitive || step.Node.Ephemeral,
	}
	if observed.Protected {
		observed.Values = map[string]any{"protected_value": movedCLISecret}
	}
	return observed, nil
}

func (provider *movedCLIProvider) Delete(context.Context, coreengine.Step) error {
	provider.deleted++
	return nil
}

type movedCLIDocument struct {
	Summary struct {
		Move    int `json:"move"`
		Create  int `json:"create"`
		Update  int `json:"update"`
		Adopt   int `json:"adopt"`
		Delete  int `json:"delete"`
		Destroy int `json:"destroy"`
		Forget  int `json:"forget"`
		NoOp    int `json:"no_op"`
	} `json:"summary"`
	Moves []struct {
		Host string `json:"host"`
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"moves"`
	Changes []struct {
		Action  string         `json:"action"`
		Desired map[string]any `json:"desired"`
	} `json:"changes"`
}

func TestMovedCLIExposesMovesAndCompletedLifecycleRemainsClean(t *testing.T) {
	dir := t.TempDir()
	transport, provider, runtime := prepareMovedCLIState(t, dir)
	from := `host.node.component.old.files.file["/etc/moved-secret"]`
	to := `host.node.component.current.files.file["/etc/moved-secret"]`

	var planOutput bytes.Buffer
	if err := runPlanWithRuntime([]string{"--format", "json"}, &planOutput, dir, nil, runtime); err != nil {
		t.Fatal(err)
	}
	assertMovedCLISecretAbsent(t, planOutput.String())
	assertMovedCLIPlan(t, decodeMovedCLIDocument(t, planOutput.Bytes()), from, to)
	if transport.stateWrites != 0 {
		t.Fatalf("pending moved plan wrote state %d time(s)", transport.stateWrites)
	}

	var checkOutput bytes.Buffer
	checkErr := runCheckWithRuntime([]string{"--format", "json"}, &checkOutput, dir, nil, runtime)
	if checkErr == nil || !strings.Contains(checkErr.Error(), "drift or unapplied changes") {
		t.Fatalf("pending moved check error = %v", checkErr)
	}
	assertMovedCLISecretAbsent(t, checkErr.Error(), checkOutput.String())
	assertMovedCLIPlan(t, decodeMovedCLIDocument(t, checkOutput.Bytes()), from, to)
	if transport.stateWrites != 0 {
		t.Fatalf("pending moved check wrote state %d time(s)", transport.stateWrites)
	}

	var applyOutput bytes.Buffer
	if err := runApplyWithRuntime([]string{"--auto-approve", "--debug", "--lock-timeout", "0"}, &applyOutput, dir, nil, runtime); err != nil {
		t.Fatal(err)
	}
	assertMovedCLISecretAbsent(t, applyOutput.String())
	assertMovedCLIApprovalSections(t, applyOutput.String(), from, to)
	if provider.applied != 0 || provider.deleted != 0 || transport.stateWrites != 1 {
		t.Fatalf("move-only CLI apply: applied=%d deleted=%d writes=%d", provider.applied, provider.deleted, transport.stateWrites)
	}
	identity := transport.state.ComponentIdentities["host.node.component.current"]
	if identity.PhysicalName != "old" {
		t.Fatalf("moved CLI component identities = %#v", transport.state.ComponentIdentities)
	}
	if _, exists := transport.state.Resources[to]; !exists {
		t.Fatalf("moved CLI state has no target resource: %#v", transport.state.Resources)
	}
	stateData, err := corestate.Encode(transport.state)
	if err != nil {
		t.Fatal(err)
	}
	assertMovedCLISecretAbsent(t, string(stateData))

	writesAfterMove := transport.stateWrites
	for _, test := range []struct {
		name        string
		retainBlock bool
	}{
		{name: "retained block", retainBlock: true},
		{name: "removed block", retainBlock: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeMovedCLIConfig(t, dir, "current", test.retainBlock)
			var cleanPlan bytes.Buffer
			if err := runPlanWithRuntime([]string{"--format", "json"}, &cleanPlan, dir, nil, runtime); err != nil {
				t.Fatal(err)
			}
			assertMovedCLISecretAbsent(t, cleanPlan.String())
			assertCleanMovedCLIDocument(t, decodeMovedCLIDocument(t, cleanPlan.Bytes()))

			var cleanCheck bytes.Buffer
			if err := runCheckWithRuntime([]string{"--format", "json"}, &cleanCheck, dir, nil, runtime); err != nil {
				t.Fatal(err)
			}
			assertMovedCLISecretAbsent(t, cleanCheck.String())
			assertCleanMovedCLIDocument(t, decodeMovedCLIDocument(t, cleanCheck.Bytes()))
			if transport.stateWrites != writesAfterMove {
				t.Fatalf("completed moved plan/check wrote state %d time(s), want %d", transport.stateWrites, writesAfterMove)
			}
			if got := transport.state.ComponentIdentities["host.node.component.current"].PhysicalName; got != "old" {
				t.Fatalf("completed moved physical name = %q, want old", got)
			}
		})
	}
}

func TestMovedCLIProtectedProviderFailureOmitsValuesFromErrorDebugAndState(t *testing.T) {
	dir := t.TempDir()
	transport, provider, runtime := prepareMovedCLIState(t, dir)
	provider.inspectErr = errors.New(movedCLISecret)

	var output bytes.Buffer
	err := runApplyWithRuntime([]string{"--auto-approve", "--debug", "--lock-timeout", "0"}, &output, dir, nil, runtime)
	if err == nil || !strings.Contains(err.Error(), "inspect protected resource") {
		t.Fatalf("protected moved apply error = %v", err)
	}
	stateData, encodeErr := corestate.Encode(transport.state)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	assertMovedCLISecretAbsent(t, err.Error(), output.String(), string(stateData))
	if !strings.Contains(output.String(), "debug phase=inspect") || !strings.Contains(output.String(), "status=failed") {
		t.Fatalf("protected moved debug output = %s", output.String())
	}
	if transport.stateWrites != 0 || !movedCLIStateHasPrefix(transport.state, "host.node.component.old") || movedCLIStateHasPrefix(transport.state, "host.node.component.current") {
		t.Fatalf("failed protected move changed state after %d writes: %#v", transport.stateWrites, transport.state)
	}
}

func prepareMovedCLIState(t *testing.T, dir string) (*fakeOnlineTransport, *movedCLIProvider, onlineRuntime) {
	t.Helper()
	writeMovedCLIConfig(t, dir, "old", false)
	transport := newFakeOnlineTransport("alpine")
	provider := &movedCLIProvider{}
	runtime := fakeOnlineRuntime(transport, "")
	runtime.Provider = provider
	var output bytes.Buffer
	if err := runApplyWithRuntime([]string{"--auto-approve", "--lock-timeout", "0"}, &output, dir, nil, runtime); err != nil {
		t.Fatal(err)
	}
	stateData, err := corestate.Encode(transport.state)
	if err != nil {
		t.Fatal(err)
	}
	assertMovedCLISecretAbsent(t, output.String(), string(stateData))
	oldAddress := `host.node.component.old.files.file["/etc/moved-secret"]`
	resource, exists := transport.state.Resources[oldAddress]
	if !exists || !resource.Protected || resource.DesiredDigest == "" || transport.stateWrites != 1 || provider.applied != 0 || provider.deleted != 0 {
		t.Fatalf("legacy moved CLI setup: resource=%#v exists=%v writes=%d applied=%d deleted=%d", resource, exists, transport.stateWrites, provider.applied, provider.deleted)
	}

	writeMovedCLIConfig(t, dir, "current", true)
	transport.events = nil
	transport.stateWrites = 0
	provider.applied = 0
	provider.deleted = 0
	return transport, provider, runtime
}

func writeMovedCLIConfig(t *testing.T, dir, instance string, includeMove bool) {
	t.Helper()
	move := ""
	if includeMove {
		move = `moved {
  from = component.old
  to   = component.current
}

`
	}
	content := fmt.Sprintf(`%scomponent "app" {
  input "token" {
    type      = string
    default   = %s
    sensitive = true
  }

  files {
    file "/etc/moved-secret" {
      content = input.token
      mode    = "0600"
    }
  }
}

host "node" {
  ssh { host = "alpine-alias" }
  platform {
    architecture = "amd64"
    version      = "3.24.1"
  }
  component %s {
    source = component.app
  }
}
`, move, strconv.Quote(movedCLISecret), strconv.Quote(instance))
	if err := os.WriteFile(filepath.Join(dir, "main.apf.hcl"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func decodeMovedCLIDocument(t *testing.T, data []byte) movedCLIDocument {
	t.Helper()
	var document movedCLIDocument
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode moved CLI document: %v\n%s", err, data)
	}
	return document
}

func assertMovedCLIPlan(t *testing.T, document movedCLIDocument, from, to string) {
	t.Helper()
	if document.Summary.Move != 1 || document.Summary.Create != 0 || document.Summary.Update != 0 || document.Summary.Adopt != 0 || document.Summary.Delete != 0 || document.Summary.Destroy != 0 || document.Summary.Forget != 0 || document.Summary.NoOp != 1 {
		t.Fatalf("move-only CLI summary = %#v", document.Summary)
	}
	if len(document.Moves) != 1 || document.Moves[0].Host != "node" || document.Moves[0].From != from || document.Moves[0].To != to {
		t.Fatalf("move-only CLI moves = %#v", document.Moves)
	}
	if len(document.Changes) != 1 || document.Changes[0].Action != coreengine.ActionNoOp || document.Changes[0].Desired["protected"] != true {
		t.Fatalf("move-only CLI resource changes = %#v", document.Changes)
	}
}

func assertCleanMovedCLIDocument(t *testing.T, document movedCLIDocument) {
	t.Helper()
	if document.Summary.Move != 0 || document.Summary.Create != 0 || document.Summary.Update != 0 || document.Summary.Adopt != 0 || document.Summary.Delete != 0 || document.Summary.Destroy != 0 || document.Summary.Forget != 0 || document.Summary.NoOp != 1 || len(document.Moves) != 0 {
		t.Fatalf("completed moved CLI document = %#v", document)
	}
}

func assertMovedCLIApprovalSections(t *testing.T, output, from, to string) {
	t.Helper()
	previewIndex := strings.Index(output, "Preview before lock:")
	lockedIndex := strings.Index(output, "Locked execution plan:")
	if previewIndex < 0 || lockedIndex <= previewIndex {
		t.Fatalf("moved CLI approval labels missing:\n%s", output)
	}
	for label, section := range map[string]string{
		"preview": output[previewIndex:lockedIndex],
		"locked":  output[lockedIndex:],
	} {
		if !strings.Contains(section, from) || !strings.Contains(section, to) || !strings.Contains(section, "Summary: 1 move") {
			t.Fatalf("moved CLI %s approval omitted move:\n%s", label, section)
		}
	}
}

func assertMovedCLISecretAbsent(t *testing.T, values ...string) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(value, movedCLISecret) {
			t.Fatalf("moved CLI output leaked protected value: %s", value)
		}
	}
}

func movedCLIStateHasPrefix(state corestate.State, prefix string) bool {
	for address := range state.Resources {
		if address == prefix || strings.HasPrefix(address, prefix+".") {
			return true
		}
	}
	return false
}

package state

import (
	"strings"
	"testing"
	"time"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func TestDecodeEmptyReturnsCurrentAlpineFormState(t *testing.T) {
	got, err := Decode(nil, "server1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Product != Product || got.SchemaVersion != SchemaVersion || got.Host != "server1" || got.Resources == nil {
		t.Fatalf("Decode(nil) = %#v", got)
	}
}

func TestDecodeRejectsForeignNewerAndWrongHostState(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantErr string
	}{
		{name: "missing product", data: `{"schema_version":1,"host":"server1","resources":{}}`, wantErr: "no product marker"},
		{name: "DebianForm legacy state", data: `{"version":2,"host":"server1","resources":{}}`, wantErr: "no product marker"},
		{name: "foreign product", data: `{"product":"debianform","schema_version":1,"host":"server1","resources":{}}`, wantErr: "refusing foreign state"},
		{name: "older schema", data: `{"product":"alpineform","host":"server1","resources":{}}`, wantErr: "unsupported schema 0"},
		{name: "newer schema", data: `{"product":"alpineform","schema_version":2,"host":"server1","resources":{}}`, wantErr: "newer schema 2"},
		{name: "wrong host", data: `{"product":"alpineform","schema_version":1,"host":"server2","resources":{}}`, wantErr: `host "server2" does not match requested host "server1"`},
		{name: "wrong resource host", data: `{"product":"alpineform","schema_version":1,"host":"server1","resources":{"file.example":{"host":"server2","kind":"file"}}}`, wantErr: `belongs to host "server2", expected "server1"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decode([]byte(test.data), "server1")
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Decode() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestStateRoundTripAndRevision(t *testing.T) {
	input := Empty("server1")
	input.Facts = &ir.HostFacts{OSID: "alpine", Version: "3.24.1", Branch: "v3.24", Architecture: "amd64", NativeArchitecture: "x86_64", KernelArchitecture: "x86_64", Libc: "musl", DetectedAt: "2026-07-13T07:00:00Z"}
	input.Resources["file.example"] = Resource{Kind: "file"}
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	prepared, err := PrepareWrite(input, "server1", now)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Serial != 1 || prepared.UpdatedAt != "2026-07-13T08:00:00Z" || prepared.Resources["file.example"].Host != "server1" {
		t.Fatalf("PrepareWrite() = %#v", prepared)
	}
	if input.Serial != 0 || input.Resources["file.example"].Host != "" {
		t.Fatalf("PrepareWrite mutated input: %#v", input)
	}
	data, err := Encode(prepared)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data, "server1")
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Serial != prepared.Serial || decoded.Product != Product {
		t.Fatalf("round trip = %#v", decoded)
	}
	if decoded.Facts == nil || *decoded.Facts != *input.Facts {
		t.Fatalf("round-trip facts = %#v, want %#v", decoded.Facts, input.Facts)
	}
}

func TestEncodeNeverPersistsProtectedResourceValues(t *testing.T) {
	secret := "not-a-real-state-secret"
	for _, resource := range []Resource{
		{Kind: "file", Desired: map[string]any{"content": secret}, Sensitive: true, DesiredDigest: "digest"},
		{Kind: "file", Desired: map[string]any{"content": secret, "sensitive": true}, DesiredDigest: "digest"},
		{Kind: "file", Desired: map[string]any{"content": secret}, Ephemeral: true, DesiredDigest: "ephemeral-digest"},
		{Kind: "file", Desired: map[string]any{"content": secret, "ephemeral": true}, DesiredDigest: "ephemeral-digest"},
	} {
		state := Empty("node")
		state.Resources["file.secret"] = resource
		data, err := Encode(state)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if strings.Contains(text, secret) || !strings.Contains(text, `"protected": true`) {
			t.Fatalf("encoded protected state = %s", text)
		}
		if resource.Ephemeral || mapMarkedEphemeral(resource.Desired) {
			if strings.Contains(text, "ephemeral-digest") {
				t.Fatalf("encoded ephemeral state persisted digest: %s", text)
			}
		}
	}
}

func TestEncodePersistsSafeWriteOnlyVersionDigest(t *testing.T) {
	state := Empty("node")
	state.Resources["file.write_only"] = Resource{
		Kind:          "file",
		Ephemeral:     true,
		DigestSafe:    true,
		DesiredDigest: "safe-version-digest",
		Delete:        map[string]any{"path": "/etc/app/config"},
	}
	data, err := Encode(state)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "safe-version-digest") || !strings.Contains(text, `"digest_safe": true`) || !strings.Contains(text, `/etc/app/config`) || !strings.Contains(text, `"protected": true`) {
		t.Fatalf("write-only state = %s", text)
	}
}

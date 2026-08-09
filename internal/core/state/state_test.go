package state

import (
	"encoding/json"
	"reflect"
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
	if got.Product != Product || got.SchemaVersion != SchemaVersion || got.Host != "server1" || got.ComponentIdentities == nil || got.Resources == nil {
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
		{name: "newer schema", data: `{"product":"alpineform","schema_version":4,"host":"server1","resources":{}}`, wantErr: "newer schema 4"},
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

func TestDecodeV1AndEncodeV3ComponentIdentities(t *testing.T) {
	decoded, err := Decode([]byte(`{
  "product": "alpineform",
  "schema_version": 1,
  "host": "edge",
  "serial": 4,
  "resources": {
    "host.edge.component.old.files.file[\"/etc/app\"]": {
      "host": "edge",
      "kind": "file",
      "ownership": "managed",
      "desired_digest": "digest",
      "order": 1
    }
  }
}`), "edge")
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != SchemaVersion || decoded.ComponentIdentities == nil || decoded.Serial != 4 {
		t.Fatalf("decoded v1 state = %#v", decoded)
	}

	decoded.ComponentIdentities["host.edge.component.current"] = ComponentIdentity{PhysicalName: "old"}
	data, err := Encode(decoded)
	if err != nil {
		t.Fatal(err)
	}
	var header struct {
		SchemaVersion       int                          `json:"schema_version"`
		ComponentIdentities map[string]ComponentIdentity `json:"component_identities"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		t.Fatal(err)
	}
	if header.SchemaVersion != SchemaVersion || header.ComponentIdentities["host.edge.component.current"].PhysicalName != "old" {
		t.Fatalf("encoded current state = %s", data)
	}
	roundTrip, err := Decode(data, "edge")
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.ComponentIdentities["host.edge.component.current"].PhysicalName != "old" {
		t.Fatalf("round-trip component identities = %#v", roundTrip.ComponentIdentities)
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

func TestResourceDependenciesDecodeV2EncodeV3AndCloneCanonically(t *testing.T) {
	decoded, err := Decode([]byte(`{
  "product": "alpineform",
  "schema_version": 2,
  "host": "edge",
  "resources": {
    "host.edge.files.file[\"/etc/app\"]": {
      "host": "edge",
      "kind": "file",
      "ownership": "managed",
      "order": 2,
      "depends_on": [
        "host.edge.packages.package[\"zlib\"]",
        "host.edge.packages.package[\"bird\"]",
        "host.edge.packages.package[\"zlib\"]"
      ]
    }
  }
}`), "edge")
	if err != nil {
		t.Fatal(err)
	}
	address := `host.edge.files.file["/etc/app"]`
	want := []string{
		`host.edge.packages.package["bird"]`,
		`host.edge.packages.package["zlib"]`,
	}
	resource := decoded.Resources[address]
	if !reflect.DeepEqual(resource.DependsOn, want) {
		t.Fatalf("decoded dependencies = %#v, want %#v", resource.DependsOn, want)
	}

	data, err := Encode(decoded)
	if err != nil {
		t.Fatal(err)
	}
	var encoded State
	if err := json.Unmarshal(data, &encoded); err != nil {
		t.Fatal(err)
	}
	if encoded.SchemaVersion != SchemaVersion || !reflect.DeepEqual(encoded.Resources[address].DependsOn, want) {
		t.Fatalf("encoded dependencies = %s", data)
	}

	cloned := cloneResource(resource)
	cloned.DependsOn[0] = "changed"
	if !reflect.DeepEqual(resource.DependsOn, want) {
		t.Fatalf("cloneResource shared dependency storage: %#v", resource.DependsOn)
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

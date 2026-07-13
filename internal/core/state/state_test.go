package state

import (
	"strings"
	"testing"
	"time"
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
}

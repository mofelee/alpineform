// Package state owns AlpineForm's independent persisted state schema.
package state

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	Product       = "alpineform"
	SchemaVersion = 1
)

type State struct {
	Product       string              `json:"product"`
	SchemaVersion int                 `json:"schema_version"`
	Host          string              `json:"host"`
	Serial        uint64              `json:"serial"`
	UpdatedAt     string              `json:"updated_at,omitempty"`
	Resources     map[string]Resource `json:"resources"`
}

type Resource struct {
	Host          string         `json:"host"`
	Kind          string         `json:"kind"`
	Ownership     string         `json:"ownership"`
	Desired       map[string]any `json:"desired,omitempty"`
	DesiredDigest string         `json:"desired_digest"`
	Observed      map[string]any `json:"observed,omitempty"`
	Order         int            `json:"order"`
}

func Empty(host string) State {
	return State{Product: Product, SchemaVersion: SchemaVersion, Host: host, Resources: map[string]Resource{}}
}

func Decode(data []byte, expectedHost string) (State, error) {
	if expectedHost == "" {
		return State{}, fmt.Errorf("cannot decode AlpineForm state without an expected host")
	}
	if len(data) == 0 {
		return Empty(expectedHost), nil
	}
	var decoded State
	if err := json.Unmarshal(data, &decoded); err != nil {
		return State{}, fmt.Errorf("decode AlpineForm state: %w", err)
	}
	return Normalize(decoded, expectedHost)
}

func Normalize(input State, expectedHost string) (State, error) {
	if expectedHost == "" {
		return State{}, fmt.Errorf("cannot validate AlpineForm state without an expected host")
	}
	if input.Product == "" {
		return State{}, fmt.Errorf("state has no product marker; refusing non-AlpineForm state")
	}
	if input.Product != Product {
		return State{}, fmt.Errorf("state product %q is not %q; refusing foreign state", input.Product, Product)
	}
	if input.SchemaVersion > SchemaVersion {
		return State{}, fmt.Errorf("AlpineForm state for host %q uses newer schema %d; this build supports schema %d", expectedHost, input.SchemaVersion, SchemaVersion)
	}
	if input.SchemaVersion != SchemaVersion {
		return State{}, fmt.Errorf("AlpineForm state for host %q uses unsupported schema %d; no migration to schema %d is available", expectedHost, input.SchemaVersion, SchemaVersion)
	}
	if input.Host == "" {
		return State{}, fmt.Errorf("AlpineForm state host is empty; expected %q", expectedHost)
	}
	if input.Host != expectedHost {
		return State{}, fmt.Errorf("AlpineForm state host %q does not match requested host %q", input.Host, expectedHost)
	}

	normalized := input
	normalized.Resources = make(map[string]Resource, len(input.Resources))
	for address, resource := range input.Resources {
		if resource.Host == "" {
			resource.Host = expectedHost
		} else if resource.Host != expectedHost {
			return State{}, fmt.Errorf("AlpineForm state resource %q belongs to host %q, expected %q", address, resource.Host, expectedHost)
		}
		normalized.Resources[address] = resource
	}
	return normalized, nil
}

func PrepareWrite(input State, expectedHost string, now time.Time) (State, error) {
	normalized, err := Normalize(input, expectedHost)
	if err != nil {
		return State{}, err
	}
	normalized.Serial++
	normalized.UpdatedAt = now.UTC().Format(time.RFC3339)
	return normalized, nil
}

func Encode(input State) ([]byte, error) {
	normalized, err := Normalize(input, input.Host)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(normalized, "", "  ")
}

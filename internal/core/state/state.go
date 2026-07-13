// Package state owns AlpineForm's independent persisted state schema.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mofelee/alpineform/internal/core/ir"
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
	Facts         *ir.HostFacts       `json:"facts,omitempty"`
	Resources     map[string]Resource `json:"resources"`
}

type Resource struct {
	Host           string         `json:"host"`
	Kind           string         `json:"kind"`
	Ownership      string         `json:"ownership"`
	Desired        map[string]any `json:"desired,omitempty"`
	DesiredDigest  string         `json:"desired_digest,omitempty"`
	Observed       map[string]any `json:"observed,omitempty"`
	Order          int            `json:"order"`
	Protected      bool           `json:"protected,omitempty"`
	Sensitive      bool           `json:"-"`
	Ephemeral      bool           `json:"-"`
	PreventDestroy bool           `json:"prevent_destroy,omitempty"`
	DeleteBehavior string         `json:"delete_behavior,omitempty"`
	Delete         map[string]any `json:"delete,omitempty"`
	DigestSafe     bool           `json:"digest_safe,omitempty"`
}

func Digest(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		data = []byte(fmt.Sprintf("%#v", value))
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (resource Resource) MarshalJSON() ([]byte, error) {
	type resourceJSON Resource
	out := resourceJSON(resource)
	protected := resource.Protected || resource.Sensitive || resource.Ephemeral || mapMarksProtected(resource.Desired) || mapMarksProtected(resource.Observed)
	if protected {
		out.Desired = nil
		out.Observed = nil
		out.Protected = true
	}
	if (resource.Ephemeral && !resource.DigestSafe) || mapMarkedEphemeral(resource.Desired) || mapMarkedEphemeral(resource.Observed) {
		out.DesiredDigest = ""
	}
	return json.Marshal(out)
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

func mapMarksProtected(value map[string]any) bool {
	if value == nil {
		return false
	}
	sensitive, _ := value["sensitive"].(bool)
	ephemeral, _ := value["ephemeral"].(bool)
	return sensitive || ephemeral
}

func mapMarkedEphemeral(value map[string]any) bool {
	if value == nil {
		return false
	}
	ephemeral, _ := value["ephemeral"].(bool)
	return ephemeral
}

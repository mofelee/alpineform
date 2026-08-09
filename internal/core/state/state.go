// Package state owns AlpineForm's independent persisted state schema.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/mofelee/alpineform/internal/core/ir"
)

const (
	Product                      = "alpineform"
	SchemaVersion                = 3
	minimumReadableSchemaVersion = 1
)

type State struct {
	Product             string                       `json:"product"`
	SchemaVersion       int                          `json:"schema_version"`
	Host                string                       `json:"host"`
	Serial              uint64                       `json:"serial"`
	UpdatedAt           string                       `json:"updated_at,omitempty"`
	Facts               *ir.HostFacts                `json:"facts,omitempty"`
	ComponentIdentities map[string]ComponentIdentity `json:"component_identities,omitempty"`
	Resources           map[string]Resource          `json:"resources"`
}

type ComponentIdentity struct {
	PhysicalName string `json:"physical_name"`
}

type Resource struct {
	Host           string         `json:"host"`
	Kind           string         `json:"kind"`
	Ownership      string         `json:"ownership"`
	Desired        map[string]any `json:"desired,omitempty"`
	DesiredDigest  string         `json:"desired_digest,omitempty"`
	Observed       map[string]any `json:"observed,omitempty"`
	Order          int            `json:"order"`
	DependsOn      []string       `json:"depends_on,omitempty"`
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
	return State{
		Product:             Product,
		SchemaVersion:       SchemaVersion,
		Host:                host,
		ComponentIdentities: map[string]ComponentIdentity{},
		Resources:           map[string]Resource{},
	}
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
	if input.SchemaVersion < minimumReadableSchemaVersion {
		return State{}, fmt.Errorf("AlpineForm state for host %q uses unsupported schema %d; no migration to schema %d is available", expectedHost, input.SchemaVersion, SchemaVersion)
	}
	if input.Host == "" {
		return State{}, fmt.Errorf("AlpineForm state host is empty; expected %q", expectedHost)
	}
	if input.Host != expectedHost {
		return State{}, fmt.Errorf("AlpineForm state host %q does not match requested host %q", input.Host, expectedHost)
	}

	normalized := input
	normalized.SchemaVersion = SchemaVersion
	if input.Facts != nil {
		facts := *input.Facts
		normalized.Facts = &facts
	}
	normalized.ComponentIdentities = make(map[string]ComponentIdentity, len(input.ComponentIdentities))
	for logicalRoot, identity := range input.ComponentIdentities {
		normalized.ComponentIdentities[logicalRoot] = identity
	}
	normalized.Resources = make(map[string]Resource, len(input.Resources))
	for address, resource := range input.Resources {
		if resource.Host == "" {
			resource.Host = expectedHost
		} else if resource.Host != expectedHost {
			return State{}, fmt.Errorf("AlpineForm state resource %q belongs to host %q, expected %q", address, resource.Host, expectedHost)
		}
		normalized.Resources[address] = cloneResource(resource)
	}
	if err := validateComponentIdentityMappings(normalized); err != nil {
		return State{}, err
	}
	return normalized, nil
}

func PrepareWrite(input State, expectedHost string, now time.Time) (State, error) {
	normalized, err := Normalize(input, expectedHost)
	if err != nil {
		return State{}, err
	}
	normalized = pruneComponentIdentities(normalized)
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

func cloneState(input State) State {
	out := input
	if input.Facts != nil {
		facts := *input.Facts
		out.Facts = &facts
	}
	out.ComponentIdentities = make(map[string]ComponentIdentity, len(input.ComponentIdentities))
	for logicalRoot, identity := range input.ComponentIdentities {
		out.ComponentIdentities[logicalRoot] = identity
	}
	out.Resources = make(map[string]Resource, len(input.Resources))
	for address, resource := range input.Resources {
		out.Resources[address] = cloneResource(resource)
	}
	return out
}

func cloneResource(resource Resource) Resource {
	out := resource
	out.Desired = cloneMap(resource.Desired)
	out.Observed = cloneMap(resource.Observed)
	out.Delete = cloneMap(resource.Delete)
	out.DependsOn = canonicalDependencies(resource.DependsOn)
	return out
}

func canonicalDependencies(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	out := append([]string(nil), input...)
	sort.Strings(out)
	unique := out[:0]
	for _, dependency := range out {
		if len(unique) == 0 || unique[len(unique)-1] != dependency {
			unique = append(unique, dependency)
		}
	}
	return unique
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = cloneValue(value)
	}
	return out
}

func cloneValue(value any) any {
	if value == nil {
		return nil
	}
	return cloneReflectValue(reflect.ValueOf(value)).Interface()
}

func cloneReflectValue(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.New(value.Type()).Elem()
		out.Set(cloneReflectValue(value.Elem()))
		return out
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeMapWithSize(value.Type(), value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			out.SetMapIndex(cloneReflectValue(iterator.Key()), cloneReflectValue(iterator.Value()))
		}
		return out
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.New(value.Type().Elem())
		out.Elem().Set(cloneReflectValue(value.Elem()))
		return out
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for index := 0; index < value.Len(); index++ {
			out.Index(index).Set(cloneReflectValue(value.Index(index)))
		}
		return out
	case reflect.Array:
		out := reflect.New(value.Type()).Elem()
		for index := 0; index < value.Len(); index++ {
			out.Index(index).Set(cloneReflectValue(value.Index(index)))
		}
		return out
	default:
		return value
	}
}

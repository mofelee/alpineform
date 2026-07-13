package graph

import (
	"reflect"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func TestCompileOrdersAPKKeyRepositoriesAndSingleUpdate(t *testing.T) {
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name: "node", Source: source(1),
		APK: &ir.APKSpec{
			Ownership: "managed", Source: source(2),
			Keys: []ir.APKKeySpec{{Filename: "vendor.rsa.pub", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Ensure: "present", Source: source(3)}},
			Repositories: []ir.APKRepositorySpec{
				{Name: "main", Line: "https://example.test/alpine/v3.24/main", Ensure: "present", Source: source(4)},
				{Name: "community", Line: "https://example.test/alpine/v3.24/community", Ensure: "present", Source: source(5)},
			},
		},
	}}}
	compiled, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	ordered, err := compiled.Schedule()
	if err != nil {
		t.Fatal(err)
	}
	addresses := make([]string, 0, len(ordered))
	for _, node := range ordered {
		addresses = append(addresses, node.Address)
	}
	want := []string{
		"host.node",
		`host.node.apk.key["vendor.rsa.pub"]`,
		`host.node.apk.repository["community"]`,
		`host.node.apk.repository["main"]`,
		"host.node.apk.update",
	}
	if !reflect.DeepEqual(addresses, want) {
		t.Fatalf("APK schedule = %#v, want %#v", addresses, want)
	}
	update := ordered[len(ordered)-1]
	if update.Kind != "apk_update" || len(update.DependsOn) != 3 || update.Desired["fingerprint"] == "" {
		t.Fatalf("APK update node = %#v", update)
	}
}

func TestCompileAuthoritativeAPKUsesWholeFileResource(t *testing.T) {
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name: "node", Source: source(1),
		APK: &ir.APKSpec{Ownership: "authoritative", Source: source(2), Repositories: []ir.APKRepositorySpec{
			{Name: "main", Line: "https://example.test/alpine/v3.24/main", Ensure: "present", Source: source(3)},
			{Name: "old", Line: "https://example.test/alpine/v3.24/old", Ensure: "absent", Source: source(4)},
		}},
	}}}
	compiled, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.ManagedCount() != 2 {
		t.Fatalf("authoritative graph = %#v", compiled.Nodes)
	}
	repositories := compiled.Nodes[1]
	if repositories.Address != "host.node.apk.repositories" || repositories.Kind != "apk_repositories" || !reflect.DeepEqual(repositories.Desired["lines"], []string{"https://example.test/alpine/v3.24/main"}) {
		t.Fatalf("authoritative repositories node = %#v", repositories)
	}
}

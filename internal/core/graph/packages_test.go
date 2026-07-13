package graph

import (
	"reflect"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func TestCompileOrdersAPKUpdateBeforePackages(t *testing.T) {
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name: "node", Source: source(1),
		APK: &ir.APKSpec{Ownership: "managed", Source: source(2), Repositories: []ir.APKRepositorySpec{{
			Name: "main", Line: "https://example.test/alpine/v3.24/main", Ensure: "present", Source: source(3),
		}}},
		Packages: []ir.PackageSpec{
			{Name: "curl", WorldIntent: "curl", Ensure: "present", Source: source(4)},
			{Name: "old", WorldIntent: "old", Ensure: "absent", Source: source(5)},
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
		`host.node.apk.repository["main"]`,
		"host.node.apk.update",
		`host.node.packages.package["curl"]`,
		`host.node.packages.package["old"]`,
	}
	if !reflect.DeepEqual(addresses, want) {
		t.Fatalf("package schedule = %#v, want %#v", addresses, want)
	}
	for _, node := range ordered[3:] {
		if !reflect.DeepEqual(node.DependsOn, []string{"host.node", "host.node.apk.update"}) || node.Desired["delete_behavior"] != "" {
			t.Fatalf("package node = %#v", node)
		}
	}
}

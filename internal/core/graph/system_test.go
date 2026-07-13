package graph

import (
	"reflect"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func TestCompileSystemNodesAreExplicitAndTimezoneDependsOnTZData(t *testing.T) {
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name: "node", Source: source(1),
		System: &ir.SystemSpec{
			Hostname: "edge.example", Timezone: "UTC", HostnameSource: source(2), TimezoneSource: source(3), Source: source(2),
		},
		Packages: []ir.PackageSpec{{Name: "tzdata", WorldIntent: "tzdata", Ensure: "present", Source: source(3)}},
	}}}
	compiled, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]Node{}
	for _, node := range compiled.Nodes {
		nodes[node.Address] = node
	}
	hostname := nodes["host.node.system.hostname"]
	timezone := nodes["host.node.system.timezone"]
	if hostname.Kind != "system_hostname" || !reflect.DeepEqual(hostname.DependsOn, []string{"host.node"}) || hostname.Desired["delete_behavior"] != "" {
		t.Fatalf("hostname node = %#v", hostname)
	}
	wantTimezoneDeps := []string{"host.node", `host.node.packages.package["tzdata"]`}
	if timezone.Kind != "system_timezone" || !reflect.DeepEqual(timezone.DependsOn, wantTimezoneDeps) || timezone.Desired["timezone"] != "UTC" {
		t.Fatalf("timezone node = %#v", timezone)
	}
	ordered, err := compiled.Schedule()
	if err != nil {
		t.Fatal(err)
	}
	order := map[string]int{}
	for index, node := range ordered {
		order[node.Address] = index
	}
	if order[`host.node.packages.package["tzdata"]`] >= order["host.node.system.timezone"] {
		t.Fatalf("system schedule = %#v", order)
	}
}

func TestCompileHostWithoutSystemHasNoSystemNodes(t *testing.T) {
	compiled, err := Compile(&ir.Program{Hosts: []ir.HostSpec{{Name: "node", Source: source(1)}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range compiled.Nodes {
		if node.Kind == "system_hostname" || node.Kind == "system_timezone" {
			t.Fatalf("implicit system node = %#v", node)
		}
	}
}

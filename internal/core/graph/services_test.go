package graph

import (
	"reflect"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func TestCompileOrdersServiceAfterInitConfPackageAndAccount(t *testing.T) {
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name: "node", Source: source(1),
		Groups:   []ir.ManagedGroupSpec{{Name: "worker", Ensure: "present", Source: source(2)}},
		Users:    []ir.ManagedUserSpec{{Name: "worker", PrimaryGroup: "worker", Ensure: "present", Source: source(3)}},
		Packages: []ir.PackageSpec{{Name: "worker-daemon", WorldIntent: "worker-daemon", Ensure: "present", Source: source(4)}},
		Files: []ir.ManagedFileSpec{
			{Path: "/etc/init.d/worker", Ensure: "present", Source: source(5)},
			{Path: "/etc/conf.d/worker", Ensure: "present", Source: source(6)},
		},
		Services: []ir.ServiceSpec{{
			Name: "worker", Enabled: true, Runlevel: "default", State: "running", Operation: "restarted", Package: "worker-daemon", User: "worker", Group: "worker", Source: source(7),
		}},
	}}}
	compiled, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	serviceAddress := `host.node.services.service["worker"]`
	var service Node
	for _, node := range compiled.Nodes {
		if node.Address == serviceAddress {
			service = node
		}
	}
	wantDependencies := []string{
		"host.node",
		`host.node.files.file["/etc/conf.d/worker"]`,
		`host.node.files.file["/etc/init.d/worker"]`,
		`host.node.groups.group["worker"]`,
		`host.node.packages.package["worker-daemon"]`,
		`host.node.users.user["worker"]`,
	}
	wantTriggers := []string{
		`host.node.files.file["/etc/conf.d/worker"]`,
		`host.node.files.file["/etc/init.d/worker"]`,
	}
	if service.Kind != "service" || !reflect.DeepEqual(service.DependsOn, wantDependencies) || !reflect.DeepEqual(service.TriggeredBy, wantTriggers) || service.Desired["operation"] != "restarted" || service.Desired["delete_behavior"] != "" {
		t.Fatalf("service node = %#v", service)
	}
	ordered, err := compiled.Schedule()
	if err != nil {
		t.Fatal(err)
	}
	if ordered[len(ordered)-1].Address != serviceAddress {
		t.Fatalf("service was not scheduled last: %#v", ordered)
	}
}

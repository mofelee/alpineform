package graph

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func TestCompileExplicitResourceDependenciesOrderHostResources(t *testing.T) {
	const (
		packageAddress = `host.node.packages.package["bird"]`
		fileAddress    = `host.node.files.file["/etc/conf.d/bird"]`
		serviceAddress = `host.node.services.service["bird"]`
	)
	firstPackageSource := ir.SourceRef{File: "main.apf.hcl", Line: 12, Path: `host.node.services.service.bird.depends_on[1]`}
	program := &ir.Program{Hosts: []ir.HostSpec{
		{
			Name:     "node",
			Source:   source(1),
			Packages: []ir.PackageSpec{{Name: "bird", WorldIntent: "bird", Ensure: "present", Source: source(2)}},
			Files: []ir.ManagedFileSpec{
				{Path: "/etc/conf.d/bird", Ensure: "present", Source: source(3)},
				{Path: "/tmp/unrelated", Ensure: "present", Source: source(4)},
			},
			Services: []ir.ServiceSpec{{Name: "bird", Package: "bird", Operation: "restart", State: "running", Source: source(5)}},
			ExplicitDependencies: []ir.ResourceDependencySpec{
				{From: fileAddress, DependsOn: packageAddress, Source: ir.SourceRef{File: "main.apf.hcl", Line: 9, Path: `host.node.files.file.config.depends_on[0]`}},
				{From: serviceAddress, DependsOn: fileAddress, Source: ir.SourceRef{File: "main.apf.hcl", Line: 11, Path: `host.node.services.service.bird.depends_on[0]`}},
				{From: serviceAddress, DependsOn: packageAddress, Source: firstPackageSource},
				{From: serviceAddress, DependsOn: packageAddress, Source: ir.SourceRef{File: "main.apf.hcl", Line: 99, Path: `duplicate.depends_on[0]`}},
			},
		},
		{
			Name:     "other",
			Source:   source(20),
			Packages: []ir.PackageSpec{{Name: "bird", WorldIntent: "bird", Ensure: "present", Source: source(21)}},
			Files:    []ir.ManagedFileSpec{{Path: "/tmp/unrelated", Ensure: "present", Source: source(22)}},
		},
	}}

	resourceGraph, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	file := mustGraphNode(t, resourceGraph, fileAddress)
	if dependencyCount(file.DependsOn, packageAddress) != 1 || !reflect.DeepEqual(file.ExplicitDependsOn, []string{packageAddress}) {
		t.Fatalf("file dependencies = %#v, explicit = %#v", file.DependsOn, file.ExplicitDependsOn)
	}
	service := mustGraphNode(t, resourceGraph, serviceAddress)
	wantExplicit := []string{fileAddress, packageAddress}
	if dependencyCount(service.DependsOn, fileAddress) != 1 || dependencyCount(service.DependsOn, packageAddress) != 1 || !reflect.DeepEqual(service.ExplicitDependsOn, wantExplicit) {
		t.Fatalf("service dependencies = %#v, explicit = %#v", service.DependsOn, service.ExplicitDependsOn)
	}
	if !reflect.DeepEqual(service.TriggeredBy, []string{fileAddress}) {
		t.Fatalf("service triggers = %#v, want only inferred change trigger %#v", service.TriggeredBy, []string{fileAddress})
	}
	if got := service.DependencySources[packageAddress]; got != firstPackageSource {
		t.Fatalf("duplicate explicit dependency source = %#v, want first source %#v", got, firstPackageSource)
	}

	positions := scheduledPositions(t, resourceGraph)
	if !(positions[packageAddress] < positions[fileAddress] && positions[fileAddress] < positions[serviceAddress]) {
		t.Fatalf("host explicit dependency positions = %#v", positions)
	}

	unrelated := mustGraphNode(t, resourceGraph, `host.node.files.file["/tmp/unrelated"]`)
	otherPackage := `host.other.packages.package["bird"]`
	otherFile := mustGraphNode(t, resourceGraph, `host.other.files.file["/tmp/unrelated"]`)
	for _, node := range []Node{unrelated, otherFile} {
		if len(node.ExplicitDependsOn) != 0 || len(node.DependencySources) != 0 || containsAddress(node.DependsOn, packageAddress) || containsAddress(node.DependsOn, otherPackage) {
			t.Fatalf("unrelated node %s gained explicit relationships: %#v", node.Address, node)
		}
	}
}

func TestCompileExplicitResourceDependenciesOrderMountedComponentResources(t *testing.T) {
	const prefix = "host.node.component.router"
	packageAddress := prefix + `.packages.package["bird"]`
	fileAddress := prefix + `.files.file["/etc/bird.conf"]`
	serviceAddress := prefix + `.services.service["bird"]`
	component := ir.ComponentInstanceSpec{
		Name:     "router",
		Template: "bird",
		Source:   source(2),
		Packages: []ir.PackageSpec{{Name: "bird", WorldIntent: "bird", Ensure: "present", Source: source(3)}},
		Files:    []ir.ManagedFileSpec{{Path: "/etc/bird.conf", Ensure: "present", Source: source(4)}},
		Services: []ir.ServiceSpec{{Name: "bird", State: "running", Source: source(5)}},
		ExplicitDependencies: []ir.ResourceDependencySpec{
			{From: fileAddress, DependsOn: packageAddress, Source: ir.SourceRef{File: "component.apf.hcl", Line: 8, Path: `component.bird.files.file.config.depends_on[0]`}},
			{From: serviceAddress, DependsOn: fileAddress, Source: ir.SourceRef{File: "component.apf.hcl", Line: 12, Path: `component.bird.services.service.bird.depends_on[0]`}},
		},
	}
	resourceGraph, err := Compile(&ir.Program{Hosts: []ir.HostSpec{{Name: "node", Source: source(1), Components: []ir.ComponentInstanceSpec{component}}}})
	if err != nil {
		t.Fatal(err)
	}

	file := mustGraphNode(t, resourceGraph, fileAddress)
	service := mustGraphNode(t, resourceGraph, serviceAddress)
	if !reflect.DeepEqual(file.ExplicitDependsOn, []string{packageAddress}) || !reflect.DeepEqual(service.ExplicitDependsOn, []string{fileAddress}) {
		t.Fatalf("component explicit dependencies: file=%#v service=%#v", file.ExplicitDependsOn, service.ExplicitDependsOn)
	}
	positions := scheduledPositions(t, resourceGraph)
	if !(positions[packageAddress] < positions[fileAddress] && positions[fileAddress] < positions[serviceAddress]) {
		t.Fatalf("component explicit dependency positions = %#v", positions)
	}
}

func TestApplyExplicitDependenciesDoesNotMutateTriggeredByAlias(t *testing.T) {
	const (
		first  = "host.node.resources.z"
		second = "host.node.resources.a"
		from   = "host.node.resources.service"
	)
	relationships := make([]string, 1, 2)
	relationships[0] = first
	nodes := []Node{
		{Address: first},
		{Address: second},
		{Address: from, DependsOn: relationships, TriggeredBy: relationships},
	}
	if err := applyExplicitDependencies("node", nodes, []ir.ResourceDependencySpec{{From: from, DependsOn: second}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(nodes[2].DependsOn, []string{second, first}) || !reflect.DeepEqual(nodes[2].TriggeredBy, []string{first}) {
		t.Fatalf("relationships after explicit edge: depends_on=%#v triggered_by=%#v", nodes[2].DependsOn, nodes[2].TriggeredBy)
	}
}

func TestApplyExplicitDependenciesReportsExactAddressDiagnostics(t *testing.T) {
	const (
		fileAddress    = `host.node.files.file["/etc/bird.conf"]`
		packageAddress = `host.node.packages.package["bird"]`
	)
	nodes := []Node{{Address: fileAddress}, {Address: packageAddress}}
	tests := []struct {
		name       string
		dependency ir.ResourceDependencySpec
		want       string
	}{
		{
			name: "missing dependent",
			dependency: ir.ResourceDependencySpec{
				From: `host.node.files.file["/etc/missing.conf"]`, DependsOn: packageAddress,
				Source: ir.SourceRef{File: "main.apf.hcl", Line: 17, Path: `host.node.files.file.missing.depends_on[0]`},
			},
			want: `main.apf.hcl:17:host.node.files.file.missing.depends_on[0]: dependent resource graph address "host.node.files.file[\"/etc/missing.conf\"]" does not exist`,
		},
		{
			name: "missing dependency",
			dependency: ir.ResourceDependencySpec{
				From: fileAddress, DependsOn: `host.node.packages.package["missing"]`,
				Source: ir.SourceRef{File: "main.apf.hcl", Line: 18, Path: `host.node.files.file.config.depends_on[1]`},
			},
			want: `main.apf.hcl:18:host.node.files.file.config.depends_on[1]: dependency resource graph address "host.node.packages.package[\"missing\"]" does not exist`,
		},
		{
			name: "cross host",
			dependency: ir.ResourceDependencySpec{
				From: fileAddress, DependsOn: `host.other.packages.package["bird"]`,
				Source: ir.SourceRef{File: "main.apf.hcl", Line: 19, Path: `host.node.files.file.config.depends_on[2]`},
			},
			want: fmt.Sprintf(
				`main.apf.hcl:19:host.node.files.file.config.depends_on[2]: explicit dependency crosses host scope: %q depends on %q`,
				fileAddress,
				`host.other.packages.package["bird"]`,
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := applyExplicitDependencies("node", append([]Node(nil), nodes...), []ir.ResourceDependencySpec{test.dependency})
			if err == nil || err.Error() != test.want {
				t.Fatalf("applyExplicitDependencies() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDependencyCycleIsCanonicalAndSourceOriented(t *testing.T) {
	authoredSource := ir.SourceRef{File: "cycle.apf.hcl", Line: 42, Path: `host.node.services.service.bird.depends_on[0]`}
	resourceGraph := &ResourceGraph{Nodes: []Node{
		{Address: "node.z", DependsOn: []string{"node.c"}, Source: source(6)},
		{Address: "node.d", DependsOn: []string{"node.b"}, Source: source(4)},
		{Address: "node.a", DependsOn: []string{"node.b"}, Source: source(1)},
		{Address: "node.c", DependsOn: []string{"node.d"}, DependencySources: map[string]ir.SourceRef{"node.d": authoredSource}, Source: source(3)},
		{Address: "node.b", DependsOn: []string{"node.c"}, Source: source(2)},
	}}
	want := `cycle.apf.hcl:42:host.node.services.service.bird.depends_on[0]: resource dependency cycle: node.b -> node.c -> node.d -> node.b`
	validateErr := resourceGraph.Validate()
	if validateErr == nil || validateErr.Error() != want {
		t.Fatalf("Validate() error = %v, want %q", validateErr, want)
	}
	_, scheduleErr := resourceGraph.Schedule()
	if scheduleErr == nil || scheduleErr.Error() != want {
		t.Fatalf("Schedule() error = %v, want %q", scheduleErr, want)
	}
	for _, tail := range []string{"node.a", "node.z"} {
		if strings.Contains(validateErr.Error(), "cycle: "+tail) || strings.Contains(validateErr.Error(), " -> "+tail) {
			t.Fatalf("cycle diagnostic includes non-cycle tail %q: %v", tail, validateErr)
		}
	}
}

func TestDependencySelfCycleReportsCompletePath(t *testing.T) {
	source := ir.SourceRef{File: "self.apf.hcl", Line: 9, Path: `host.node.files.file.config.depends_on[0]`}
	resourceGraph := &ResourceGraph{Nodes: []Node{{
		Address: "node.self", DependsOn: []string{"node.self"},
		DependencySources: map[string]ir.SourceRef{"node.self": source}, Source: ir.SourceRef{File: "self.apf.hcl", Line: 3, Path: "file.config"},
	}}}
	want := `self.apf.hcl:9:host.node.files.file.config.depends_on[0]: resource dependency cycle: node.self -> node.self`
	if err := resourceGraph.Validate(); err == nil || err.Error() != want {
		t.Fatalf("Validate() error = %v, want %q", err, want)
	}
}

func TestExplicitDependencyMetadataIsNotSerialized(t *testing.T) {
	node := Node{
		Address:           "host.node.resources.service",
		DependsOn:         []string{"host.node.resources.file"},
		ExplicitDependsOn: []string{"host.node.resources.file"},
		DependencySources: map[string]ir.SourceRef{
			"host.node.resources.file": {File: "authored-only.apf.hcl", Line: 7, Path: "private.depends_on[0]"},
		},
		Source: source(1),
	}
	data, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, hidden := range []string{"ExplicitDependsOn", "DependencySources", "explicit_depends_on", "dependency_sources", "authored-only.apf.hcl", "private.depends_on"} {
		if strings.Contains(text, hidden) {
			t.Fatalf("graph JSON exposes internal dependency metadata %q: %s", hidden, text)
		}
	}
	if !strings.Contains(text, `"depends_on":["host.node.resources.file"]`) {
		t.Fatalf("graph JSON lost public combined dependencies: %s", text)
	}
}

func mustGraphNode(t *testing.T, resourceGraph *ResourceGraph, address string) Node {
	t.Helper()
	for _, node := range resourceGraph.Nodes {
		if node.Address == address {
			return node
		}
	}
	t.Fatalf("graph node %q not found", address)
	return Node{}
}

func scheduledPositions(t *testing.T, resourceGraph *ResourceGraph) map[string]int {
	t.Helper()
	ordered, err := resourceGraph.Schedule()
	if err != nil {
		t.Fatal(err)
	}
	positions := make(map[string]int, len(ordered))
	for index, node := range ordered {
		positions[node.Address] = index
	}
	return positions
}

func dependencyCount(dependencies []string, wanted string) int {
	count := 0
	for _, dependency := range dependencies {
		if dependency == wanted {
			count++
		}
	}
	return count
}

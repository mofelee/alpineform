package graph

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func source(line int) ir.SourceRef {
	return ir.SourceRef{File: "model.apf.hcl", Line: line, Path: "test"}
}

func TestValidateAndScheduleDeterministically(t *testing.T) {
	graph := &ResourceGraph{Nodes: []Node{
		{Address: "node.c", Source: source(3)},
		{Address: "node.b", DependsOn: []string{"node.a"}, Source: source(2)},
		{Address: "node.a", Source: source(1)},
	}}
	if err := graph.Validate(); err != nil {
		t.Fatal(err)
	}
	ordered, err := graph.Schedule()
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(ordered))
	for _, node := range ordered {
		got = append(got, node.Address)
	}
	if want := []string{"node.a", "node.b", "node.c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("schedule = %#v, want %#v", got, want)
	}
}

func TestValidateRejectsDuplicateUnknownAndCyclicDependencies(t *testing.T) {
	tests := []struct {
		name  string
		nodes []Node
		want  string
	}{
		{name: "duplicate", nodes: []Node{{Address: "node.a", Source: source(1)}, {Address: "node.a", Source: source(2)}}, want: `duplicate resource address "node.a"`},
		{name: "unknown", nodes: []Node{{Address: "node.a", DependsOn: []string{"node.missing"}, Source: source(1)}}, want: `depends on unknown address "node.missing"`},
		{name: "cycle", nodes: []Node{{Address: "node.a", DependsOn: []string{"node.b"}, Source: source(1)}, {Address: "node.b", DependsOn: []string{"node.a"}, Source: source(2)}}, want: "resource dependency cycle involves: node.a, node.b"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := (&ResourceGraph{Nodes: test.nodes}).Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestProtectedNodeJSONDoesNotLeakDesiredValues(t *testing.T) {
	secret := "not-a-real-graph-secret"
	data, err := json.Marshal(Node{Address: "node.secret", Managed: true, Desired: map[string]any{"content": secret}, Sensitive: true, Source: source(1)})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, secret) || strings.Contains(text, "content") || !strings.Contains(text, `"protected":true`) {
		t.Fatalf("protected node JSON = %s", text)
	}
}

func TestCompileFileNodeSeparatesProviderPayload(t *testing.T) {
	secret := "not-a-real-graph-file-secret"
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name:   "node",
		Source: source(1),
		Files: []ir.ManagedFileSpec{{
			Path:             "/etc/app/config",
			Content:          secret,
			ContentVersion:   "release-1",
			ContentWriteOnly: true,
			ContentBytes:     int64(len(secret)),
			Owner:            "root",
			Group:            "root",
			Mode:             "0600",
			Ensure:           "present",
			OnRemove:         "destroy",
			Sensitive:        true,
			Ephemeral:        true,
			Lifecycle:        ir.LifecycleSpec{PreventDestroy: true},
			Source:           source(3),
		}},
	}}}
	resourceGraph, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	if resourceGraph.ManagedCount() != 1 || len(resourceGraph.Nodes) != 2 {
		t.Fatalf("graph = %#v", resourceGraph)
	}
	file := resourceGraph.Nodes[1]
	if file.Address != `host.node.files.file["/etc/app/config"]` || file.Kind != "file" || !file.Managed || !file.Sensitive || !file.Ephemeral || !file.DigestSafe || file.Payload["content"] != secret || file.Desired["delete_behavior"] != "destroy" {
		t.Fatalf("file node = %#v", file)
	}
	data, err := json.Marshal(resourceGraph)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "release-1") || !strings.Contains(string(data), `"protected":true`) {
		t.Fatalf("graph JSON = %s", data)
	}
}

func TestCompileBuildsStructuralGraph(t *testing.T) {
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name:     "node",
		Source:   source(1),
		Platform: &ir.PlatformSpec{Architecture: "amd64", Version: "3.24.1", Branch: "3.24", Libc: "musl", NativeArchitecture: "x86_64", Source: source(2)},
		Components: []ir.ComponentInstanceSpec{
			{Name: "base", Template: "app", Source: source(3)},
			{Name: "worker", Template: "app", DependsOn: []string{"base"}, Source: source(4)},
		},
	}}}
	compiled, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Nodes) != 4 || compiled.ManagedCount() != 0 {
		t.Fatalf("compiled graph = %#v", compiled)
	}
	worker := Node{}
	for _, node := range compiled.Nodes {
		if node.Address == "host.node.component.worker" {
			worker = node
		}
	}
	wantDeps := []string{"host.node", "host.node.component.base"}
	if worker.Address != "host.node.component.worker" || !reflect.DeepEqual(worker.DependsOn, wantDeps) {
		t.Fatalf("worker node = %#v", worker)
	}
}

func TestCompileOrdersPresentPathResourcesParentFirst(t *testing.T) {
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name:   "node",
		Source: source(1),
		Directories: []ir.ManagedDirectorySpec{
			{Path: "/srv/app", Ensure: "present", Source: source(2)},
			{Path: "/srv/app/data", Ensure: "present", Source: source(3)},
		},
		Files: []ir.ManagedFileSpec{{Path: "/srv/app/data/config", Ensure: "present", Source: source(4)}},
	}}}
	compiled, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	ordered, err := compiled.Schedule()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"host.node",
		`host.node.directories.directory["/srv/app"]`,
		`host.node.directories.directory["/srv/app/data"]`,
		`host.node.files.file["/srv/app/data/config"]`,
	}
	got := make([]string, 0, len(ordered))
	for _, node := range ordered {
		got = append(got, node.Address)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("present path schedule = %#v, want %#v", got, want)
	}
}

func TestCompileOrdersAbsentPathResourcesLeafFirst(t *testing.T) {
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name:   "node",
		Source: source(1),
		Directories: []ir.ManagedDirectorySpec{
			{Path: "/srv/app", Ensure: "absent", Source: source(2)},
			{Path: "/srv/app/data", Ensure: "absent", Source: source(3)},
		},
		Files: []ir.ManagedFileSpec{{Path: "/srv/app/data/config", Ensure: "absent", Source: source(4)}},
	}}}
	compiled, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	ordered, err := compiled.Schedule()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"host.node",
		`host.node.files.file["/srv/app/data/config"]`,
		`host.node.directories.directory["/srv/app/data"]`,
		`host.node.directories.directory["/srv/app"]`,
	}
	got := make([]string, 0, len(ordered))
	for _, node := range ordered {
		got = append(got, node.Address)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("absent path schedule = %#v, want %#v", got, want)
	}
}

func TestCompileOrdersManagedGroupBeforeOwnedPaths(t *testing.T) {
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name:   "node",
		Source: source(1),
		Groups: []ir.ManagedGroupSpec{{Name: "app", GID: "1500", Ensure: "present", Source: source(2)}},
		Directories: []ir.ManagedDirectorySpec{{
			Path: "/srv/app", Group: "app", Ensure: "present", Source: source(3),
		}},
		Files: []ir.ManagedFileSpec{{Path: "/srv/app/config", Group: "1500", Ensure: "present", Source: source(4)}},
	}}}
	compiled, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	ordered, err := compiled.Schedule()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"host.node",
		`host.node.groups.group["app"]`,
		`host.node.directories.directory["/srv/app"]`,
		`host.node.files.file["/srv/app/config"]`,
	}
	got := make([]string, 0, len(ordered))
	for _, node := range ordered {
		got = append(got, node.Address)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("owned path schedule = %#v, want %#v", got, want)
	}
}

func TestCompileOrdersAbsentOwnedPathsBeforeGroup(t *testing.T) {
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name:   "node",
		Source: source(1),
		Groups: []ir.ManagedGroupSpec{{Name: "app", Ensure: "absent", Source: source(2)}},
		Directories: []ir.ManagedDirectorySpec{{
			Path: "/srv/app", Group: "app", Ensure: "absent", Source: source(3),
		}},
		Files: []ir.ManagedFileSpec{{Path: "/srv/app/config", Group: "app", Ensure: "absent", Source: source(4)}},
	}}}
	compiled, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	ordered, err := compiled.Schedule()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"host.node",
		`host.node.files.file["/srv/app/config"]`,
		`host.node.directories.directory["/srv/app"]`,
		`host.node.groups.group["app"]`,
	}
	got := make([]string, 0, len(ordered))
	for _, node := range ordered {
		got = append(got, node.Address)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("absent ownership schedule = %#v, want %#v", got, want)
	}
}

func TestCompileOrdersGroupUserAndOwnedPaths(t *testing.T) {
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name:   "node",
		Source: source(1),
		Groups: []ir.ManagedGroupSpec{{Name: "app", Ensure: "present", Source: source(2)}},
		Users: []ir.ManagedUserSpec{{
			Name: "app", UID: "1500", PrimaryGroup: "app", Ensure: "present", Source: source(3),
		}},
		Directories: []ir.ManagedDirectorySpec{{
			Path: "/srv/app", Owner: "app", Group: "app", Ensure: "present", Source: source(4),
		}},
		Files: []ir.ManagedFileSpec{{Path: "/srv/app/config", Owner: "1500", Group: "app", Ensure: "present", Source: source(5)}},
	}}}
	compiled, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	ordered, err := compiled.Schedule()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"host.node",
		`host.node.groups.group["app"]`,
		`host.node.users.user["app"]`,
		`host.node.directories.directory["/srv/app"]`,
		`host.node.files.file["/srv/app/config"]`,
	}
	got := make([]string, 0, len(ordered))
	for _, node := range ordered {
		got = append(got, node.Address)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("account ownership schedule = %#v, want %#v", got, want)
	}
}

func TestCompileOrdersAbsentOwnedPathsUserAndGroup(t *testing.T) {
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name:   "node",
		Source: source(1),
		Groups: []ir.ManagedGroupSpec{{Name: "app", Ensure: "absent", Source: source(2)}},
		Users: []ir.ManagedUserSpec{{
			Name: "app", PrimaryGroup: "app", Ensure: "absent", Source: source(3),
		}},
		Directories: []ir.ManagedDirectorySpec{{
			Path: "/srv/app", Owner: "app", Group: "app", Ensure: "absent", Source: source(4),
		}},
		Files: []ir.ManagedFileSpec{{Path: "/srv/app/config", Owner: "app", Group: "app", Ensure: "absent", Source: source(5)}},
	}}}
	compiled, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	ordered, err := compiled.Schedule()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"host.node",
		`host.node.files.file["/srv/app/config"]`,
		`host.node.directories.directory["/srv/app"]`,
		`host.node.users.user["app"]`,
		`host.node.groups.group["app"]`,
	}
	got := make([]string, 0, len(ordered))
	for _, node := range ordered {
		got = append(got, node.Address)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("absent account schedule = %#v, want %#v", got, want)
	}
}

package graph

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
)

const componentArtifactSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestCompileArtifactSourceAndInstallNodes(t *testing.T) {
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name: "node", Source: source(1),
		Components: []ir.ComponentInstanceSpec{{
			Name: "cli", Template: "tool", ArtifactType: "binary", Version: "1.2.3", Source: source(2),
			SelectedSource: &ir.ComponentArtifactSourceSpec{Architecture: "amd64", URL: "https://example.invalid/tool", SHA256: componentArtifactSHA, Source: source(3)},
			Install:        &ir.ComponentArtifactInstallSpec{Path: "/usr/local/bin/tool", Owner: "root", Group: "root", Mode: "0755", Source: source(4)},
		}},
	}}}
	resourceGraph, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	byAddress := map[string]Node{}
	for _, node := range resourceGraph.Nodes {
		byAddress[node.Address] = node
	}
	componentAddress := "host.node.component.cli"
	sourceAddress := componentAddress + `.artifact.source["amd64"]`
	installAddress := componentAddress + `.artifact.install["/usr/local/bin/tool"]`
	sourceNode := byAddress[sourceAddress]
	installNode := byAddress[installAddress]
	if sourceNode.Kind != "component_artifact_source" || !reflect.DeepEqual(sourceNode.DependsOn, []string{componentAddress}) || sourceNode.Desired["sha256"] != componentArtifactSHA {
		t.Fatalf("source node = %#v", sourceNode)
	}
	if installNode.Kind != "component_binary" || !reflect.DeepEqual(installNode.DependsOn, []string{sourceAddress}) || installNode.Desired["content_sha256"] != componentArtifactSHA || installNode.Desired["version"] != "1.2.3" {
		t.Fatalf("install node = %#v", installNode)
	}
}

func TestCompileUsesPhysicalComponentNameOnlyForArtifactCache(t *testing.T) {
	component := ir.ComponentInstanceSpec{
		Name: "current", PhysicalName: "legacy", Template: "tool", ArtifactType: "binary", Version: "1.2.3", Source: source(2),
		SelectedSource: &ir.ComponentArtifactSourceSpec{Architecture: "amd64", URL: "https://example.invalid/tool", SHA256: componentArtifactSHA, Source: source(3)},
		Install:        &ir.ComponentArtifactInstallSpec{Path: "/usr/local/bin/tool", Owner: "root", Group: "root", Mode: "0755", Source: source(4)},
	}
	resourceGraph, err := Compile(&ir.Program{Hosts: []ir.HostSpec{{Name: "node", Source: source(1), Components: []ir.ComponentInstanceSpec{component}}}})
	if err != nil {
		t.Fatal(err)
	}
	logicalPrefix := "host.node.component.current"
	foundSource := false
	for _, node := range resourceGraph.Nodes {
		if strings.Contains(node.Address, ".component.legacy") {
			t.Fatalf("physical name leaked into graph address %q", node.Address)
		}
		if node.Address == logicalPrefix+`.artifact.source["amd64"]` {
			foundSource = true
			if got := node.Desired["path"]; got != "/var/cache/alpineform/components/legacy/"+componentArtifactSHA+"/artifact" {
				t.Fatalf("artifact cache path = %#v", got)
			}
		}
	}
	if !foundSource {
		t.Fatal("component artifact source node not found")
	}
}

func TestCompileSourceBuildStablePhaseGraphAndRedaction(t *testing.T) {
	build := &ir.ComponentBuildSpec{
		Identity: componentArtifactSHA, WorkingDirectory: ".", Output: "tool", MaxOutputBytes: 1024,
		Network: "none", OnRemove: "forget", Sensitive: true, Environment: map[string]string{"TOKEN": "secret"},
		EnvironmentNames: []string{"TOKEN"}, EnvironmentVersion: "token-v1", Dependencies: []string{"build-base"},
		Inputs:   []ir.ComponentBuildInputSpec{{Name: "source", Kind: "content", Content: []byte("source"), SHA256: componentArtifactSHA, PayloadSHA256: componentArtifactSHA, Destination: "main.c", Source: source(3)}},
		Commands: []ir.ComponentBuildCommandSpec{{Argv: []string{"cc", "-o", "tool", "main.c"}, Source: source(4)}}, Source: source(2),
	}
	component := ir.ComponentInstanceSpec{
		Name: "cli", Template: "tool", ArtifactType: "source", Build: build, Source: source(2),
		Install: &ir.ComponentArtifactInstallSpec{Path: "/usr/local/bin/tool", Owner: "root", Group: "root", Mode: "0755", Source: source(5)},
	}
	resourceGraph, err := Compile(&ir.Program{Hosts: []ir.HostSpec{{Name: "node", Source: source(1), Components: []ir.ComponentInstanceSpec{component}}}})
	if err != nil {
		t.Fatal(err)
	}
	prefix := "host.node.component.cli.build"
	wantKinds := map[string]string{
		prefix + `.input["source"]`: "component_build_input", prefix + ".dependencies": "component_build_dependencies",
		prefix + ".workspace": "component_build_workspace", prefix + `.output["tool"]`: "component_build_output",
		prefix + ".cleanup": "component_build_cleanup", prefix + `.install["/usr/local/bin/tool"]`: "component_build_install",
	}
	byAddress := map[string]Node{}
	for _, node := range resourceGraph.Nodes {
		byAddress[node.Address] = node
	}
	for address, kind := range wantKinds {
		if byAddress[address].Kind != kind {
			t.Fatalf("node %s = %#v", address, byAddress[address])
		}
	}
	workspace := byAddress[prefix+".workspace"]
	if got := byAddress[prefix+`.install["/usr/local/bin/tool"]`].Desired["delete_behavior"]; got != "" {
		t.Fatalf("default source-build removal = %#v, want forget", got)
	}
	encoded, err := workspace.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "desired") || !strings.Contains(string(encoded), `"protected":true`) {
		t.Fatalf("protected workspace JSON = %s", encoded)
	}
	again, err := Compile(&ir.Program{Hosts: []ir.HostSpec{{Name: "node", Source: source(1), Components: []ir.ComponentInstanceSpec{component}}}})
	if err != nil || !reflect.DeepEqual(resourceGraph, again) {
		t.Fatalf("source-build graph is not deterministic: err=%v", err)
	}
}

func TestCompileSourceBuildOwnershipIdentitiesDoNotCollide(t *testing.T) {
	makeComponent := func(name, path string) ir.ComponentInstanceSpec {
		return ir.ComponentInstanceSpec{
			Name: name, Template: "tool", ArtifactType: "source", Source: source(2),
			Build: &ir.ComponentBuildSpec{
				Identity: componentArtifactSHA, WorkingDirectory: ".", Output: "tool", MaxOutputBytes: 1024,
				Network: "none", OnRemove: "destroy", Inputs: []ir.ComponentBuildInputSpec{{Name: "source", Kind: "content", SHA256: componentArtifactSHA, PayloadSHA256: componentArtifactSHA, Destination: "main.c", Source: source(3)}},
				Commands: []ir.ComponentBuildCommandSpec{{Argv: []string{"cc"}, Source: source(4)}}, Source: source(2),
			},
			Install: &ir.ComponentArtifactInstallSpec{Path: path, Owner: "root", Group: "root", Mode: "0755", Source: source(5)},
		}
	}
	resourceGraph, err := Compile(&ir.Program{Hosts: []ir.HostSpec{{Name: "node", Source: source(1), Components: []ir.ComponentInstanceSpec{
		makeComponent("first", "/usr/local/bin/first"), makeComponent("second", "/usr/local/bin/second"),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	virtuals := map[string]bool{}
	for _, node := range resourceGraph.Nodes {
		if node.Kind != "component_build_dependencies" {
			continue
		}
		virtual, _ := node.Desired["virtual_package"].(string)
		if virtuals[virtual] {
			t.Fatalf("virtual package collision for %q", virtual)
		}
		virtuals[virtual] = true
		if node.Desired["delete_behavior"] != "destroy" {
			t.Fatalf("explicit destroy was not retained: %#v", node.Desired)
		}
	}
	if len(virtuals) != 2 {
		t.Fatalf("virtual packages = %#v", virtuals)
	}
}

func TestCompileSourceBuildRetainsPhysicalOwnershipNamespace(t *testing.T) {
	type physicalSnapshot struct {
		BuildIdentity       string
		OwnerID             string
		VirtualPackage      string
		DependencyMarker    string
		InstallMarker       string
		Workspace           string
		OutputCache         string
		OutputMarker        string
		ProtectedInputCache string
		CleanupDesired      map[string]any
	}
	component := func(logicalName, inputVersion string) ir.ComponentInstanceSpec {
		payloadSHA := fmt.Sprintf("%x", sha256.Sum256([]byte(inputVersion)))
		document := &ir.ComponentBuildIdentityDocument{
			Template: "tool", Instance: logicalName,
			Inputs:           []ir.ComponentBuildInputIdentity{{Name: "source", Kind: "content", Identity: "version:" + inputVersion, Destination: "main.c"}},
			Commands:         []ir.ComponentBuildCommandIdentity{{Argv: []string{"cc", "-o", "tool", "main.c"}}},
			WorkingDirectory: ".", Output: "tool", MaxOutputBytes: 1024, Dependencies: []string{"build-base"}, Network: "none",
			Install: ir.ComponentBuildInstallIdentity{Path: "/usr/local/bin/tool", Owner: "root", Group: "root", Mode: "0755"},
		}
		return ir.ComponentInstanceSpec{
			Name: logicalName, PhysicalName: logicalName, Template: "tool", ArtifactType: "source", Source: source(2),
			Build: &ir.ComponentBuildSpec{
				Identity: document.DigestForInstance(logicalName), IdentityDocument: document,
				WorkingDirectory: ".", Output: "tool", MaxOutputBytes: 1024, Dependencies: []string{"build-base"}, Network: "none", OnRemove: "destroy", Sensitive: true,
				Inputs: []ir.ComponentBuildInputSpec{{
					Name: "source", Kind: "content", SHA256: payloadSHA, PayloadSHA256: payloadSHA, ContentVersion: inputVersion,
					Destination: "main.c", Sensitive: true, Source: source(3),
				}},
				Commands: []ir.ComponentBuildCommandSpec{{Argv: []string{"cc", "-o", "tool", "main.c"}, Source: source(4)}}, Source: source(2),
			},
			Install: &ir.ComponentArtifactInstallSpec{Path: "/usr/local/bin/tool", Owner: "root", Group: "root", Mode: "0755", Source: source(5)},
		}
	}
	compile := func(component ir.ComponentInstanceSpec) physicalSnapshot {
		t.Helper()
		resourceGraph, err := Compile(&ir.Program{Hosts: []ir.HostSpec{{Name: "node", Source: source(1), Components: []ir.ComponentInstanceSpec{component}}}})
		if err != nil {
			t.Fatal(err)
		}
		byKind := map[string]Node{}
		physicalFragment := ".component." + component.PhysicalComponentName()
		for _, node := range resourceGraph.Nodes {
			byKind[node.Kind] = node
			identities := append(append([]string{node.Address}, node.DependsOn...), node.TriggeredBy...)
			for _, identity := range identities {
				if component.Name != component.PhysicalComponentName() && strings.Contains(identity, physicalFragment) {
					t.Fatalf("physical component name leaked into logical graph identity %q", identity)
				}
			}
		}
		for _, kind := range []string{"component_build_input", "component_build_dependencies", "component_build_workspace", "component_build_output", "component_build_cleanup", "component_build_install"} {
			if byKind[kind].Kind == "" {
				t.Fatalf("source-build graph missing %s node", kind)
			}
		}
		value := func(kind, key string) string {
			t.Helper()
			got, ok := byKind[kind].Desired[key].(string)
			if !ok || got == "" {
				t.Fatalf("%s desired %s = %#v", kind, key, byKind[kind].Desired[key])
			}
			return got
		}
		snapshot := physicalSnapshot{
			BuildIdentity:       value("component_build_dependencies", "build_identity"),
			OwnerID:             value("component_build_dependencies", "owner_id"),
			VirtualPackage:      value("component_build_dependencies", "virtual_package"),
			DependencyMarker:    value("component_build_dependencies", "marker_path"),
			InstallMarker:       value("component_build_install", "install_marker"),
			Workspace:           value("component_build_workspace", "workspace"),
			OutputCache:         value("component_build_output", "cache_path"),
			OutputMarker:        value("component_build_output", "marker_path"),
			ProtectedInputCache: value("component_build_input", "path"),
			CleanupDesired:      byKind["component_build_cleanup"].Desired,
		}
		for kind, key := range map[string]string{
			"component_build_workspace": "build_identity", "component_build_output": "build_identity",
			"component_build_cleanup": "build_identity", "component_build_install": "build_identity",
		} {
			if got := value(kind, key); got != snapshot.BuildIdentity {
				t.Fatalf("%s build identity = %q, want %q", kind, got, snapshot.BuildIdentity)
			}
		}
		for kind, key := range map[string]string{
			"component_build_workspace": "dependency_marker", "component_build_output": "dependency_marker", "component_build_cleanup": "dependency_marker",
		} {
			if got := value(kind, key); got != snapshot.DependencyMarker {
				t.Fatalf("%s dependency marker = %q, want %q", kind, got, snapshot.DependencyMarker)
			}
		}
		if value("component_build_cleanup", "workspace") != snapshot.Workspace || value("component_build_cleanup", "output_marker") != snapshot.OutputMarker {
			t.Fatalf("cleanup physical paths = %#v", snapshot.CleanupDesired)
		}
		if value("component_build_install", "cache_path") != snapshot.OutputCache || value("component_build_install", "output_marker") != snapshot.OutputMarker {
			t.Fatalf("install physical paths = %#v", byKind["component_build_install"].Desired)
		}
		return snapshot
	}

	legacy := compile(component("legacy", "input-v1"))
	moved := compile(component("current", "input-v1").WithPhysicalName("legacy"))
	changed := compile(component("current", "input-v2").WithPhysicalName("legacy"))
	ownerDigest := sha256.Sum256([]byte("host.node.component.legacy"))
	wantOwner := fmt.Sprintf("%x", ownerDigest[:16])
	if legacy.OwnerID != wantOwner || legacy.VirtualPackage != ".alpineform-build-"+wantOwner[:24] {
		t.Fatalf("legacy physical ownership = %#v", legacy)
	}
	if !reflect.DeepEqual(moved, legacy) {
		t.Fatalf("rename-only physical identity changed:\nlegacy=%#v\nmoved=%#v", legacy, moved)
	}
	for name, values := range map[string][2]string{
		"owner ID": {moved.OwnerID, changed.OwnerID}, "virtual package": {moved.VirtualPackage, changed.VirtualPackage},
		"dependency marker": {moved.DependencyMarker, changed.DependencyMarker}, "install marker": {moved.InstallMarker, changed.InstallMarker},
	} {
		if values[0] != values[1] {
			t.Fatalf("definition change changed retained %s: %q != %q", name, values[0], values[1])
		}
	}
	for name, values := range map[string][2]string{
		"build identity": {moved.BuildIdentity, changed.BuildIdentity}, "workspace": {moved.Workspace, changed.Workspace},
		"output cache": {moved.OutputCache, changed.OutputCache}, "output marker": {moved.OutputMarker, changed.OutputMarker},
		"protected input cache": {moved.ProtectedInputCache, changed.ProtectedInputCache},
	} {
		if values[0] == values[1] {
			t.Fatalf("definition change retained stale %s %q", name, values[0])
		}
	}
	if reflect.DeepEqual(moved.CleanupDesired, changed.CleanupDesired) || changed.CleanupDesired["owner_id"] != moved.OwnerID || changed.CleanupDesired["virtual_package"] != moved.VirtualPackage {
		t.Fatalf("changed cleanup did not retain owner namespace with new build paths: before=%#v after=%#v", moved.CleanupDesired, changed.CleanupDesired)
	}
}

func TestCompileComponentScriptKeepsLogicalDeclarationAndPhysicalMarker(t *testing.T) {
	compile := func(name, physical string) Node {
		declarationID := `component.` + name + `.script["refresh"]`
		script := ir.ScriptSpec{Name: "refresh", DeclarationID: declarationID, Commands: [][]string{{"refresh"}}, ScriptDigest: componentArtifactSHA, Executable: true, Source: source(5)}
		component := ir.ComponentInstanceSpec{
			Name: name, PhysicalName: physical, Template: "worker", Source: source(2), Scripts: map[string]ir.ScriptSpec{"refresh": script},
			Files: []ir.ManagedFileSpec{{
				Path: "/etc/worker.conf", Content: "value", ContentSHA256: componentArtifactSHA, Owner: "root", Group: "root", Mode: "0644", Ensure: "present",
				OnChange: &ir.ScriptReferenceSpec{Name: "refresh", Scope: "component", DeclarationID: declarationID}, Source: source(3),
			}},
		}
		resourceGraph, err := Compile(&ir.Program{Hosts: []ir.HostSpec{{Name: "node", Source: source(1), Components: []ir.ComponentInstanceSpec{component}}}})
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range resourceGraph.Nodes {
			if node.Kind == "component_script" {
				return node
			}
		}
		t.Fatal("component script node not found")
		return Node{}
	}
	legacy := compile("legacy", "legacy")
	moved := compile("current", "legacy")
	if legacy.Desired["marker_path"] != moved.Desired["marker_path"] {
		t.Fatalf("script marker changed across rename: %#v != %#v", legacy.Desired["marker_path"], moved.Desired["marker_path"])
	}
	if moved.Address != `host.node.component.current.script["refresh"]` || moved.Desired["declaration_id"] != `component.current.script["refresh"]` {
		t.Fatalf("moved script logical identity = %#v", moved)
	}
}

func TestCompileDeduplicatesRootScriptByResolvedDeclaration(t *testing.T) {
	root := ir.ScriptSpec{Name: "refresh", DeclarationID: `script["refresh"]`, Commands: [][]string{{"refresh"}}, ScriptDigest: componentArtifactSHA, Executable: true, Source: source(5)}
	makeComponent := func(name, path string) ir.ComponentInstanceSpec {
		return ir.ComponentInstanceSpec{
			Name: name, Template: name, ArtifactType: "file", Source: source(2),
			SelectedSource: &ir.ComponentArtifactSourceSpec{URL: "https://example.invalid/" + name, SHA256: componentArtifactSHA, Source: source(3)},
			Install:        &ir.ComponentArtifactInstallSpec{Path: path, Owner: "root", Group: "root", Mode: "0644", OnChange: &ir.ScriptReferenceSpec{Name: "refresh", Scope: "root", DeclarationID: root.DeclarationID}, Source: source(4)},
		}
	}
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name: "node", Source: source(1), Scripts: map[string]ir.ScriptSpec{"refresh": root},
		Components: []ir.ComponentInstanceSpec{makeComponent("first", "/etc/first"), makeComponent("second", "/etc/second")},
	}}}
	resourceGraph, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	var scripts []Node
	for _, node := range resourceGraph.Nodes {
		if node.Kind == "component_script" {
			scripts = append(scripts, node)
		}
	}
	if len(scripts) != 1 || scripts[0].Address != `host.node.script["refresh"]` || len(scripts[0].TriggeredBy) != 2 || !reflect.DeepEqual(scripts[0].DependsOn, scripts[0].TriggeredBy) {
		t.Fatalf("script nodes = %#v", scripts)
	}
	marker := scripts[0].Desired["marker_path"]
	for i := range program.Hosts[0].Components {
		program.Hosts[0].Components[i].PhysicalName = "retained_" + program.Hosts[0].Components[i].Name
	}
	physicalGraph, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	foundRootScript := false
	for _, node := range physicalGraph.Nodes {
		if node.Kind == "component_script" {
			foundRootScript = true
			if node.Desired["marker_path"] != marker {
				t.Fatalf("component physical names changed root script marker: %v != %v", node.Desired["marker_path"], marker)
			}
		}
	}
	if !foundRootScript {
		t.Fatal("root script node not found after changing component physical names")
	}
}

func TestCompileComponentNativeResourcesUseScopedAddresses(t *testing.T) {
	refresh := ir.ScriptSpec{Name: "refresh", DeclarationID: `script["refresh"]`, Commands: [][]string{{"refresh"}}, ScriptDigest: componentArtifactSHA, Executable: true, Source: source(9)}
	component := ir.ComponentInstanceSpec{
		Name: "app", Template: "worker", Source: source(2),
		Groups:      []ir.ManagedGroupSpec{{Name: "worker", Ensure: "present", Source: source(3)}},
		Users:       []ir.ManagedUserSpec{{Name: "worker", PrimaryGroup: "worker", Ensure: "present", Source: source(4)}},
		Directories: []ir.ManagedDirectorySpec{{Path: "/etc/worker", Owner: "worker", Group: "worker", Mode: "0755", Ensure: "present", Source: source(5)}},
		Files: []ir.ManagedFileSpec{
			{Path: "/etc/worker/one", Content: "one", ContentSHA256: componentArtifactSHA, Owner: "worker", Group: "worker", Mode: "0644", Ensure: "present", OnChange: &ir.ScriptReferenceSpec{Name: "refresh", Scope: "root", DeclarationID: refresh.DeclarationID}, Source: source(6)},
			{Path: "/etc/worker/two", Content: "two", ContentSHA256: componentArtifactSHA, Owner: "worker", Group: "worker", Mode: "0644", Ensure: "present", OnChange: &ir.ScriptReferenceSpec{Name: "refresh", Scope: "root", DeclarationID: refresh.DeclarationID}, Source: source(7)},
		},
		Services: []ir.ServiceSpec{{Name: "worker", Enabled: true, Runlevel: "default", State: "running", User: "worker", Group: "worker", Source: source(8)}},
	}
	program := &ir.Program{Hosts: []ir.HostSpec{{Name: "node", Source: source(1), Scripts: map[string]ir.ScriptSpec{"refresh": refresh}, Components: []ir.ComponentInstanceSpec{component}}}}
	resourceGraph, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	byAddress := map[string]Node{}
	for _, node := range resourceGraph.Nodes {
		byAddress[node.Address] = node
	}
	prefix := "host.node.component.app"
	fileAddress := prefix + `.files.file["/etc/worker/one"]`
	file := byAddress[fileAddress]
	wantFileDependencies := []string{prefix, prefix + `.directories.directory["/etc/worker"]`, prefix + `.groups.group["worker"]`, prefix + `.users.user["worker"]`}
	if file.Kind != "file" || !reflect.DeepEqual(file.DependsOn, wantFileDependencies) {
		t.Fatalf("component file node = %#v", file)
	}
	script := byAddress[`host.node.script["refresh"]`]
	if script.Kind != "component_script" || len(script.TriggeredBy) != 2 {
		t.Fatalf("component script node = %#v", script)
	}
}

func TestCompileCACertificateDependsOnSynthesizedPackage(t *testing.T) {
	component := ir.ComponentInstanceSpec{
		Name: "ca", Template: "ca", ArtifactType: "ca_certificate", Source: source(2),
		SelectedSource: &ir.ComponentArtifactSourceSpec{URL: "https://example.invalid/root.crt", SHA256: componentArtifactSHA, Source: source(3)},
		Install:        &ir.ComponentArtifactInstallSpec{Path: "/usr/local/share/ca-certificates/root.crt", Owner: "root", Group: "root", Mode: "0644", Source: source(4)},
		Packages:       []ir.PackageSpec{{Name: "ca-certificates", WorldIntent: "ca-certificates", Ensure: "present", Source: source(2)}},
	}
	resourceGraph, err := Compile(&ir.Program{Hosts: []ir.HostSpec{{Name: "node", Source: source(1), Components: []ir.ComponentInstanceSpec{component}}}})
	if err != nil {
		t.Fatal(err)
	}
	prefix := "host.node.component.ca"
	installAddress := prefix + `.artifact.install["/usr/local/share/ca-certificates/root.crt"]`
	var install Node
	for _, node := range resourceGraph.Nodes {
		if node.Address == installAddress {
			install = node
		}
	}
	want := []string{prefix + `.artifact.source["any"]`, prefix + `.packages.package["ca-certificates"]`}
	if !reflect.DeepEqual(install.DependsOn, want) {
		t.Fatalf("CA dependencies = %#v, want %#v", install.DependsOn, want)
	}
}

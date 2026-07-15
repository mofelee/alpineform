package graph

import (
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

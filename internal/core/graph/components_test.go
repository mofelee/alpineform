package graph

import (
	"reflect"
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

package graph

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/product"
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
	cachePath := "/var/cache/alpineform/components/cli/" + componentArtifactSHA + "/artifact"
	wantSourceDesired := map[string]any{
		"path": cachePath, "url": "https://example.invalid/tool", "sha256": componentArtifactSHA, "ensure": "present",
		"delete_behavior": "delete", "delete": map[string]any{"path": cachePath}, "prevent_destroy": false,
	}
	wantInstallDesired := map[string]any{
		"path": "/usr/local/bin/tool", "owner": "root", "group": "root", "mode": "0755",
		"content_sha256": componentArtifactSHA, "cache_path": cachePath, "artifact_type": "binary", "version": "1.2.3",
		"ensure": "present", "delete_behavior": "destroy", "delete": map[string]any{"path": "/usr/local/bin/tool"}, "prevent_destroy": false,
	}
	if sourceNode.Kind != "component_artifact_source" || !reflect.DeepEqual(sourceNode.DependsOn, []string{componentAddress}) || !reflect.DeepEqual(sourceNode.Desired, wantSourceDesired) {
		t.Fatalf("source node = %#v", sourceNode)
	}
	if sourceNode.Payload != nil || sourceNode.Sensitive || sourceNode.Ephemeral || sourceNode.ProtectedIntentDigest != "" {
		t.Fatalf("public source protection metadata = %#v", sourceNode)
	}
	if installNode.Kind != "component_binary" || !reflect.DeepEqual(installNode.DependsOn, []string{sourceAddress}) || !reflect.DeepEqual(installNode.Desired, wantInstallDesired) {
		t.Fatalf("install node = %#v", installNode)
	}
	if installNode.Payload != nil || installNode.Sensitive || installNode.Ephemeral || installNode.ProtectedIntentDigest != "" || installNode.TriggeredBy != nil {
		t.Fatalf("public install protection metadata = %#v", installNode)
	}
}

func TestCompileProtectedArtifactSourceDependsOnDownloaderPackage(t *testing.T) {
	tests := []struct {
		name              string
		hostPackages      []ir.PackageSpec
		componentPackages []ir.PackageSpec
		wantPackage       string
	}{
		{
			name: "host package", hostPackages: []ir.PackageSpec{{Name: "wget", WorldIntent: "wget", Ensure: "present", Source: source(2)}},
			wantPackage: `host.node.packages.package["wget"]`,
		},
		{
			name: "component package", componentPackages: []ir.PackageSpec{{Name: "wget", WorldIntent: "wget", Ensure: "present", Source: source(2)}},
			wantPackage: `host.node.component.cli.packages.package["wget"]`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			component := ir.ComponentInstanceSpec{
				Name: "cli", Template: "tool", ArtifactType: "binary", Source: source(2), Packages: test.componentPackages,
				SelectedSource: &ir.ComponentArtifactSourceSpec{
					URL: "https://example.invalid/tool", SHA256: componentArtifactSHA, SHA256Sensitive: true, Source: source(3),
				},
				Install: &ir.ComponentArtifactInstallSpec{Path: "/usr/local/bin/tool", Owner: "root", Group: "root", Mode: "0755", Source: source(4)},
			}
			resourceGraph, err := Compile(&ir.Program{Hosts: []ir.HostSpec{{
				Name: "node", Source: source(1), Packages: test.hostPackages, Components: []ir.ComponentInstanceSpec{component},
			}}})
			if err != nil {
				t.Fatal(err)
			}
			sourceAddress := `host.node.component.cli.artifact.source["any"]`
			for _, node := range resourceGraph.Nodes {
				if node.Address != sourceAddress {
					continue
				}
				want := []string{"host.node.component.cli", test.wantPackage}
				sort.Strings(want)
				if !reflect.DeepEqual(node.DependsOn, want) {
					t.Fatalf("protected source dependencies = %#v, want %#v", node.DependsOn, want)
				}
				return
			}
			t.Fatal("protected source node not found")
		})
	}
}

func TestCompilePublicArchiveDesiredMapRemainsCompatible(t *testing.T) {
	component := ir.ComponentInstanceSpec{
		Name: "bundle", Template: "bundle", ArtifactType: "archive", Version: "2.0.0", Source: source(2),
		SelectedSource: &ir.ComponentArtifactSourceSpec{URL: "https://example.invalid/bundle.tar.gz", SHA256: componentArtifactSHA, Source: source(3)},
		Extract:        &ir.ComponentArtifactExtractSpec{Format: "tar.gz", StripComponents: 1, Source: source(4)},
		Install:        &ir.ComponentArtifactInstallSpec{Path: "/opt/bundle", Owner: "root", Group: "root", Mode: "0755", Source: source(4)},
	}
	resourceGraph, err := Compile(&ir.Program{Hosts: []ir.HostSpec{{Name: "node", Source: source(1), Components: []ir.ComponentInstanceSpec{component}}}})
	if err != nil {
		t.Fatal(err)
	}
	address := `host.node.component.bundle.artifact.install["/opt/bundle"]`
	var install Node
	for _, node := range resourceGraph.Nodes {
		if node.Address == address {
			install = node
		}
	}
	cachePath := "/var/cache/alpineform/components/bundle/" + componentArtifactSHA + "/artifact"
	want := map[string]any{
		"path": "/opt/bundle", "owner": "root", "group": "root", "mode": "0755",
		"content_sha256": componentArtifactSHA, "cache_path": cachePath, "artifact_type": "archive", "version": "2.0.0",
		"ensure": "present", "delete_behavior": "destroy", "delete": map[string]any{"path": "/opt/bundle"}, "prevent_destroy": false,
		"extract_format": "tar.gz", "strip_components": 1,
	}
	if !reflect.DeepEqual(install.Desired, want) || install.Payload != nil || install.TriggeredBy != nil || install.ProtectedIntentDigest != "" {
		t.Fatalf("public archive install = %#v", install)
	}
}

func TestCompileProtectedArtifactPayloadsAreHostScopedAndNonSerializable(t *testing.T) {
	type protectedFixture struct {
		Host            string
		Sentinel        string
		URLSensitive    bool
		URLEphemeral    bool
		SHA256Sensitive bool
		SHA256Ephemeral bool
	}
	fixtures := []protectedFixture{
		{Host: "alpha", Sentinel: "not-a-real-alpha-artifact-secret", URLSensitive: true, SHA256Ephemeral: true},
		{Host: "beta", Sentinel: "not-a-real-beta-artifact-secret", URLEphemeral: true, SHA256Sensitive: true},
	}
	program := &ir.Program{}
	digests := map[string]string{}
	for _, fixture := range fixtures {
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(fixture.Sentinel)))
		digests[fixture.Host] = digest
		program.Hosts = append(program.Hosts, ir.HostSpec{
			Name: fixture.Host, Source: source(1),
			Components: []ir.ComponentInstanceSpec{{
				Name: "cli", PhysicalName: "retained_cli", Template: "tool", ArtifactType: "binary", Version: "1.2.3", Source: source(2),
				SelectedSource: &ir.ComponentArtifactSourceSpec{
					Architecture: "amd64", URL: "https://example.invalid/tool?token=" + fixture.Sentinel, SHA256: digest,
					URLSensitive: fixture.URLSensitive, URLEphemeral: fixture.URLEphemeral,
					SHA256Sensitive: fixture.SHA256Sensitive, SHA256Ephemeral: fixture.SHA256Ephemeral, Source: source(3),
				},
				Install: &ir.ComponentArtifactInstallSpec{Path: "/usr/local/bin/tool", Owner: "root", Group: "root", Mode: "0755", Source: source(4)},
			}},
		})
	}
	resourceGraph, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	byAddress := map[string]Node{}
	for _, node := range resourceGraph.Nodes {
		byAddress[node.Address] = node
	}
	intentDigests := map[string]bool{}
	for _, fixture := range fixtures {
		prefix := "host." + fixture.Host + ".component.cli"
		sourceAddress := prefix + `.artifact.source["amd64"]`
		installAddress := prefix + `.artifact.install["/usr/local/bin/tool"]`
		sourceNode := byAddress[sourceAddress]
		installNode := byAddress[installAddress]
		cachePath := "/var/cache/alpineform/components/retained_cli/protected/amd64/artifact"
		if sourceNode.Address != sourceAddress || sourceNode.Desired["path"] != cachePath || sourceNode.Desired["verified"] != true {
			t.Fatalf("%s protected source identity = %#v", fixture.Host, sourceNode)
		}
		if _, exists := sourceNode.Desired["url"]; exists {
			t.Fatalf("%s protected URL retained in Desired: %#v", fixture.Host, sourceNode.Desired)
		}
		if _, exists := sourceNode.Desired["sha256"]; exists {
			t.Fatalf("%s protected SHA retained in Desired: %#v", fixture.Host, sourceNode.Desired)
		}
		wantURL := "https://example.invalid/tool?token=" + fixture.Sentinel
		if sourceNode.Payload["url"] != wantURL || sourceNode.Payload["sha256"] != digests[fixture.Host] {
			t.Fatalf("%s source payload = %#v", fixture.Host, sourceNode.Payload)
		}
		for name, want := range map[string]bool{
			"url_sensitive": fixture.URLSensitive, "url_ephemeral": fixture.URLEphemeral,
			"sha256_sensitive": fixture.SHA256Sensitive, "sha256_ephemeral": fixture.SHA256Ephemeral,
		} {
			got, _ := sourceNode.Desired[name].(bool)
			if got != want {
				t.Fatalf("%s independent source mark %s = %v, want %v: %#v", fixture.Host, name, got, want, sourceNode.Desired)
			}
		}
		if sourceNode.ProtectedIntentDigest == "" || intentDigests[sourceNode.ProtectedIntentDigest] {
			t.Fatalf("%s source protected intent = %q", fixture.Host, sourceNode.ProtectedIntentDigest)
		}
		intentDigests[sourceNode.ProtectedIntentDigest] = true
		if installNode.Address != installAddress || installNode.Desired["cache_path"] != cachePath || installNode.Desired["content_verified"] != true || installNode.Payload["content_sha256"] != digests[fixture.Host] {
			t.Fatalf("%s protected install identity = %#v", fixture.Host, installNode)
		}
		if _, exists := installNode.Desired["content_sha256"]; exists {
			t.Fatalf("%s protected install SHA retained in Desired: %#v", fixture.Host, installNode.Desired)
		}
		for name, want := range map[string]bool{
			"content_sha256_sensitive": fixture.SHA256Sensitive,
			"content_sha256_ephemeral": fixture.SHA256Ephemeral,
		} {
			got, _ := installNode.Desired[name].(bool)
			if got != want {
				t.Fatalf("%s independent install mark %s = %v, want %v: %#v", fixture.Host, name, got, want, installNode.Desired)
			}
		}
		if !reflect.DeepEqual(installNode.DependsOn, []string{sourceAddress}) || !reflect.DeepEqual(installNode.TriggeredBy, []string{sourceAddress}) {
			t.Fatalf("%s protected install relationships = depends %#v triggered %#v", fixture.Host, installNode.DependsOn, installNode.TriggeredBy)
		}
		if installNode.Sensitive != fixture.SHA256Sensitive || installNode.Ephemeral != fixture.SHA256Ephemeral || installNode.ProtectedIntentDigest != "" {
			t.Fatalf("%s protected install flags = %#v", fixture.Host, installNode)
		}
	}
	for name, value := range map[string]any{"host spec": program, "resource graph": resourceGraph} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		text := string(encoded)
		for _, fixture := range fixtures {
			for _, forbidden := range []string{fixture.Sentinel, digests[fixture.Host]} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("%s leaked %q: %s", name, forbidden, text)
				}
			}
		}
		for digest := range intentDigests {
			if strings.Contains(text, digest) {
				t.Fatalf("%s leaked protected intent digest %q: %s", name, digest, text)
			}
		}
	}
}

func TestCompileProtectedArtifactKindsUseSafeDesiredIdentity(t *testing.T) {
	tests := []struct {
		name        string
		kind        string
		installPath string
	}{
		{name: "binary", kind: "binary", installPath: "/usr/local/bin/tool"},
		{name: "file", kind: "file", installPath: "/etc/tool.conf"},
		{name: "archive", kind: "archive", installPath: "/opt/tool"},
		{name: "ca", kind: "ca_certificate", installPath: "/usr/local/share/ca-certificates/tool.crt"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			component := ir.ComponentInstanceSpec{
				Name: "current", PhysicalName: "legacy", Template: "tool", ArtifactType: test.kind, Version: "1.2.3", Source: source(2),
				SelectedSource: &ir.ComponentArtifactSourceSpec{
					URL: "https://example.invalid/tool", SHA256: componentArtifactSHA, SHA256Sensitive: true, Source: source(3),
				},
				Install: &ir.ComponentArtifactInstallSpec{Path: test.installPath, Owner: "root", Group: "root", Mode: "0644", Source: source(4)},
			}
			if test.kind == "archive" {
				component.Extract = &ir.ComponentArtifactExtractSpec{Format: "tar.gz", StripComponents: 1, Source: source(4)}
			}
			resourceGraph, err := Compile(&ir.Program{Hosts: []ir.HostSpec{{Name: "node", Source: source(1), Components: []ir.ComponentInstanceSpec{component}}}})
			if err != nil {
				t.Fatal(err)
			}
			byAddress := map[string]Node{}
			for _, node := range resourceGraph.Nodes {
				byAddress[node.Address] = node
			}
			prefix := "host.node.component.current"
			sourceAddress := prefix + `.artifact.source["any"]`
			installAddress := prefix + ".artifact.install[" + strconv.Quote(test.installPath) + "]"
			sourceNode := byAddress[sourceAddress]
			installNode := byAddress[installAddress]
			cachePath := "/var/cache/alpineform/components/legacy/protected/any/artifact"
			if sourceNode.Desired["path"] != cachePath || sourceNode.Desired["url"] != "https://example.invalid/tool" || sourceNode.Desired["verified"] != true || sourceNode.Desired["sha256_sensitive"] != true || sourceNode.Payload["sha256"] != componentArtifactSHA {
				t.Fatalf("protected %s source = %#v", test.kind, sourceNode)
			}
			if _, exists := sourceNode.Payload["url"]; exists {
				t.Fatalf("public URL moved out of Desired: %#v", sourceNode.Payload)
			}
			if installNode.Kind != "component_"+test.kind || installNode.Desired["cache_path"] != cachePath || installNode.Desired["content_verified"] != true || installNode.Desired["content_sha256_sensitive"] != true || installNode.Payload["content_sha256"] != componentArtifactSHA {
				t.Fatalf("protected %s install = %#v", test.kind, installNode)
			}
			if !reflect.DeepEqual(installNode.DependsOn, []string{sourceAddress}) || !reflect.DeepEqual(installNode.TriggeredBy, []string{sourceAddress}) {
				t.Fatalf("protected %s relationships = %#v / %#v", test.kind, installNode.DependsOn, installNode.TriggeredBy)
			}
			if test.kind == "archive" && installNode.Desired["tree_integrity"] != "clean" {
				t.Fatalf("protected archive desired = %#v", installNode.Desired)
			}
			if test.kind == "ca_certificate" {
				marker := "/var/lib/alpineform/ca-certificates/legacy/protected/any.updated"
				if installNode.Desired["trust_marker"] != marker || !reflect.DeepEqual(installNode.Desired["delete"], map[string]any{"path": test.installPath, "trust_marker": marker}) {
					t.Fatalf("protected CA marker = %#v", installNode.Desired)
				}
			}
		})
	}
}

func TestCompileURLProtectedCAUsesStableMarkerWithoutProtectingInstall(t *testing.T) {
	component := ir.ComponentInstanceSpec{
		Name: "current", PhysicalName: "legacy", Template: "ca", ArtifactType: "ca_certificate", Source: source(2),
		SelectedSource: &ir.ComponentArtifactSourceSpec{
			Architecture: "arm64", URL: "https://example.invalid/root.crt?token=protected", SHA256: componentArtifactSHA, URLSensitive: true, Source: source(3),
		},
		Install: &ir.ComponentArtifactInstallSpec{Path: "/usr/local/share/ca-certificates/root.crt", Owner: "root", Group: "root", Mode: "0644", Source: source(4)},
	}
	resourceGraph, err := Compile(&ir.Program{Hosts: []ir.HostSpec{{Name: "node", Source: source(1), Components: []ir.ComponentInstanceSpec{component}}}})
	if err != nil {
		t.Fatal(err)
	}
	byAddress := map[string]Node{}
	for _, node := range resourceGraph.Nodes {
		byAddress[node.Address] = node
	}
	prefix := "host.node.component.current"
	sourceAddress := prefix + `.artifact.source["arm64"]`
	installNode := byAddress[prefix+`.artifact.install["/usr/local/share/ca-certificates/root.crt"]`]
	marker := "/var/lib/alpineform/ca-certificates/legacy/protected/arm64.updated"
	if installNode.Sensitive || installNode.Ephemeral || installNode.ProtectedIntentDigest != "" || installNode.Payload != nil || installNode.TriggeredBy != nil {
		t.Fatalf("URL-only protection spread to CA install: %#v", installNode)
	}
	if !reflect.DeepEqual(installNode.DependsOn, []string{sourceAddress}) || installNode.Desired["content_sha256"] != componentArtifactSHA || installNode.Desired["trust_marker"] != marker {
		t.Fatalf("URL-protected CA identity = %#v", installNode)
	}
}

func TestProtectedArtifactPathsStayStableAcrossRawIntentChanges(t *testing.T) {
	compile := func(logicalName, rawURL, rawSHA string) (Node, Node) {
		t.Helper()
		component := ir.ComponentInstanceSpec{
			Name: logicalName, PhysicalName: "retained_ca", Template: "ca", ArtifactType: "ca_certificate", Source: source(2),
			SelectedSource: &ir.ComponentArtifactSourceSpec{
				Architecture: "arm64", URL: rawURL, SHA256: rawSHA, URLSensitive: true, SHA256Ephemeral: true, Source: source(3),
			},
			Install: &ir.ComponentArtifactInstallSpec{Path: "/usr/local/share/ca-certificates/root.crt", Owner: "root", Group: "root", Mode: "0644", Source: source(4)},
		}
		resourceGraph, err := Compile(&ir.Program{Hosts: []ir.HostSpec{{Name: "node", Source: source(1), Components: []ir.ComponentInstanceSpec{component}}}})
		if err != nil {
			t.Fatal(err)
		}
		prefix := "host.node.component." + logicalName
		var sourceNode, installNode Node
		for _, node := range resourceGraph.Nodes {
			switch node.Address {
			case prefix + `.artifact.source["arm64"]`:
				sourceNode = node
			case prefix + `.artifact.install["/usr/local/share/ca-certificates/root.crt"]`:
				installNode = node
			}
		}
		return sourceNode, installNode
	}
	firstSource, firstInstall := compile("old", "https://mirror-a.invalid/root.crt?token=alpha", strings.Repeat("a", 64))
	secondSource, secondInstall := compile("current", "https://mirror-b.invalid/root.crt?token=beta", strings.Repeat("b", 64))
	wantCache := "/var/cache/alpineform/components/retained_ca/protected/arm64/artifact"
	wantMarker := "/var/lib/alpineform/ca-certificates/retained_ca/protected/arm64.updated"
	if firstSource.Desired["path"] != wantCache || secondSource.Desired["path"] != wantCache || firstInstall.Desired["cache_path"] != wantCache || secondInstall.Desired["cache_path"] != wantCache {
		t.Fatalf("protected cache paths changed: first=%#v/%#v second=%#v/%#v", firstSource.Desired, firstInstall.Desired, secondSource.Desired, secondInstall.Desired)
	}
	if firstInstall.Desired["trust_marker"] != wantMarker || secondInstall.Desired["trust_marker"] != wantMarker {
		t.Fatalf("protected CA marker changed: first=%#v second=%#v", firstInstall.Desired, secondInstall.Desired)
	}
	if !reflect.DeepEqual(firstSource.Desired, secondSource.Desired) || !reflect.DeepEqual(firstInstall.Desired, secondInstall.Desired) {
		t.Fatalf("protected durable identities changed:\nfirst=%#v/%#v\nsecond=%#v/%#v", firstSource.Desired, firstInstall.Desired, secondSource.Desired, secondInstall.Desired)
	}
	if reflect.DeepEqual(firstSource.Payload, secondSource.Payload) || firstSource.ProtectedIntentDigest == secondSource.ProtectedIntentDigest {
		t.Fatalf("protected raw intent did not change: first=%#v/%q second=%#v/%q", firstSource.Payload, firstSource.ProtectedIntentDigest, secondSource.Payload, secondSource.ProtectedIntentDigest)
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
	outputAddress := prefix + `.output["tool"]`
	cleanupAddress := prefix + ".cleanup"
	install := byAddress[prefix+`.install["/usr/local/bin/tool"]`]
	if !reflect.DeepEqual(install.DependsOn, []string{outputAddress, cleanupAddress}) || !reflect.DeepEqual(install.TriggeredBy, []string{outputAddress}) {
		t.Fatalf("source-build install relationships = depends %#v triggered %#v", install.DependsOn, install.TriggeredBy)
	}
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

func TestCompileSourceBuildWorkspaceRootIsRuntimeOnly(t *testing.T) {
	compile := func(workspaceRoot string) *ResourceGraph {
		t.Helper()
		declarationID := `component.cli.script["refresh"]`
		build := &ir.ComponentBuildSpec{
			Identity: componentArtifactSHA, WorkspaceRoot: workspaceRoot, WorkingDirectory: ".", Output: "tool", MaxOutputBytes: 1024,
			Network: "none", OnRemove: "destroy", Dependencies: []string{"build-base"},
			Inputs: []ir.ComponentBuildInputSpec{{
				Name: "source", Kind: "content", Content: []byte("source"), SHA256: componentArtifactSHA,
				PayloadSHA256: componentArtifactSHA, Destination: "main.c", Source: source(3),
			}},
			Commands: []ir.ComponentBuildCommandSpec{{Argv: []string{"cc", "-o", "tool", "main.c"}, Source: source(4)}}, Source: source(2),
		}
		component := ir.ComponentInstanceSpec{
			Name: "cli", Template: "tool", ArtifactType: "source", Build: build, Source: source(2),
			Install: &ir.ComponentArtifactInstallSpec{
				Path: "/usr/local/bin/tool", Owner: "root", Group: "root", Mode: "0755",
				OnChange: &ir.ScriptReferenceSpec{Name: "refresh", Scope: "component", DeclarationID: declarationID}, Source: source(5),
			},
			Scripts: map[string]ir.ScriptSpec{"refresh": {
				Name: "refresh", DeclarationID: declarationID, Commands: [][]string{{"refresh"}}, ScriptDigest: componentArtifactSHA, Executable: true, Source: source(6),
			}},
		}
		resourceGraph, err := Compile(&ir.Program{Hosts: []ir.HostSpec{{Name: "node", Source: source(1), Components: []ir.ComponentInstanceSpec{component}}}})
		if err != nil {
			t.Fatal(err)
		}
		return resourceGraph
	}

	implicitDefault := compile("")
	explicitDefault := compile(product.DefaultComponentBuildWorkspaceRoot)
	if !reflect.DeepEqual(implicitDefault, explicitDefault) {
		t.Fatal("implicit and explicit default workspace roots produced different graphs")
	}

	firstRoot := "/srv/alpineform-staging"
	secondRoot := "/mnt/build-work"
	first := compile(firstRoot)
	second := compile(secondRoot)
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstJSON, secondJSON) ||
		strings.Contains(string(firstJSON), firstRoot) ||
		strings.Contains(string(secondJSON), secondRoot) ||
		strings.Contains(string(firstJSON), `"output_cache"`) ||
		strings.Contains(string(secondJSON), `"output_cache"`) {
		t.Fatalf("workspace placement leaked into serialized graph:\nfirst=%s\nsecond=%s", firstJSON, secondJSON)
	}
	if len(first.Nodes) != len(second.Nodes) {
		t.Fatalf("root-only graph sizes differ: %d != %d", len(first.Nodes), len(second.Nodes))
	}
	runtimeKinds := map[string]bool{
		"component_build_dependencies": true,
		"component_build_workspace":    true,
		"component_build_output":       true,
		"component_build_cleanup":      true,
	}
	for index, firstNode := range first.Nodes {
		secondNode := second.Nodes[index]
		if firstNode.Address != secondNode.Address || firstNode.Kind != secondNode.Kind ||
			!reflect.DeepEqual(firstNode.Desired, secondNode.Desired) ||
			!reflect.DeepEqual(firstNode.DependsOn, secondNode.DependsOn) ||
			!reflect.DeepEqual(firstNode.TriggeredBy, secondNode.TriggeredBy) {
			t.Fatalf("root-only change altered graph identity for %s:\nfirst=%#v\nsecond=%#v", firstNode.Address, firstNode, secondNode)
		}
		if !strings.HasPrefix(firstNode.Kind, "component_build_") {
			continue
		}
		if runtimeKinds[firstNode.Kind] {
			if firstNode.Payload["workspace_root"] != firstRoot || secondNode.Payload["workspace_root"] != secondRoot {
				t.Fatalf("%s workspace payloads = %#v / %#v", firstNode.Kind, firstNode.Payload, secondNode.Payload)
			}
			if firstNode.Kind == "component_build_dependencies" || firstNode.Kind == "component_build_workspace" {
				wantCache := "/var/cache/alpineform/builds/outputs/" + componentArtifactSHA + "/artifact"
				if firstNode.Payload["output_cache"] != wantCache || secondNode.Payload["output_cache"] != wantCache {
					t.Fatalf("%s output-cache payloads = %#v / %#v", firstNode.Kind, firstNode.Payload, secondNode.Payload)
				}
			} else if _, exists := firstNode.Payload["output_cache"]; exists {
				t.Fatalf("%s received unexpected output cache payload: %#v", firstNode.Kind, firstNode.Payload)
			}
			if firstNode.RuntimeIntentDigest == "" || firstNode.RuntimeIntentDigest == secondNode.RuntimeIntentDigest {
				t.Fatalf("%s runtime intent digests = %q / %q", firstNode.Kind, firstNode.RuntimeIntentDigest, secondNode.RuntimeIntentDigest)
			}
			continue
		}
		if _, exists := firstNode.Payload["workspace_root"]; exists || firstNode.RuntimeIntentDigest != "" {
			t.Fatalf("%s received workspace runtime intent: %#v", firstNode.Kind, firstNode)
		}
	}
	legacyWorkspace := product.DefaultComponentBuildWorkspaceRoot + "/" + componentArtifactSHA
	for _, node := range first.Nodes {
		if node.Kind == "component_build_workspace" && node.Desired["workspace"] != legacyWorkspace {
			t.Fatalf("workspace desired identity = %#v, want %q", node.Desired["workspace"], legacyWorkspace)
		}
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
	cachePath := "/var/cache/alpineform/components/ca/" + componentArtifactSHA + "/artifact"
	marker := "/var/lib/alpineform/ca-certificates/" + componentArtifactSHA + ".updated"
	wantDesired := map[string]any{
		"path": "/usr/local/share/ca-certificates/root.crt", "owner": "root", "group": "root", "mode": "0644",
		"content_sha256": componentArtifactSHA, "cache_path": cachePath, "artifact_type": "ca_certificate", "version": "",
		"ensure": "present", "delete_behavior": "destroy",
		"delete": map[string]any{"path": "/usr/local/share/ca-certificates/root.crt", "trust_marker": marker}, "prevent_destroy": false,
		"trust_marker": marker, "trust_updated": true,
	}
	if !reflect.DeepEqual(install.Desired, wantDesired) || install.Payload != nil || install.TriggeredBy != nil || install.ProtectedIntentDigest != "" {
		t.Fatalf("public CA install = %#v", install)
	}
}

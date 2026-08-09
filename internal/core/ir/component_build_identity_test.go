package ir

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestComponentInstanceWithPhysicalNameRecomputesBuildIdentityWithoutMutation(t *testing.T) {
	document := &ComponentBuildIdentityDocument{
		Template:         "tool",
		Instance:         "current",
		Inputs:           []ComponentBuildInputIdentity{{Name: "source", Kind: "content", Identity: "sha256:first", Destination: "main.c"}},
		Commands:         []ComponentBuildCommandIdentity{{Argv: []string{"cc", "-o", "tool", "main.c"}}},
		WorkingDirectory: ".",
		Environment:      [][]string{{"TOKEN", "<protected>"}},
		Output:           "tool",
		Dependencies:     []string{"build-base"},
		Network:          "none",
		Install:          ComponentBuildInstallIdentity{Path: "/usr/local/bin/tool", Owner: "root", Group: "root", Mode: "0755"},
	}
	originalIdentity := document.DigestForInstance("current")
	component := ComponentInstanceSpec{
		Name:         "current",
		PhysicalName: "current",
		Build:        &ComponentBuildSpec{Identity: originalIdentity, IdentityDocument: document},
	}
	documentBefore := *document
	buildBefore := *component.Build

	defaulted := component.WithPhysicalName("")
	if defaulted.PhysicalName != component.Name || defaulted.Build.Identity != originalIdentity {
		t.Fatalf("empty physical name did not normalize to logical identity: %#v", defaulted)
	}

	moved := component.WithPhysicalName("legacy")
	wantMovedIdentity := document.DigestForInstance("legacy")
	if moved.Name != "current" || moved.PhysicalComponentName() != "legacy" {
		t.Fatalf("moved component identity = logical:%q physical:%q", moved.Name, moved.PhysicalComponentName())
	}
	if moved.Build == component.Build || moved.Build.Identity != wantMovedIdentity || moved.Build.Identity == originalIdentity {
		t.Fatalf("moved build identity = %#v, want recomputed %q on a copied build", moved.Build, wantMovedIdentity)
	}
	if component.PhysicalName != "current" || component.Build.Identity != originalIdentity || !reflect.DeepEqual(*component.Build, buildBefore) || !reflect.DeepEqual(*document, documentBefore) {
		t.Fatalf("WithPhysicalName mutated original: component=%#v document=%#v", component, document)
	}
	moved.Build.IdentityDocument.Inputs[0].Identity = "mutated-input"
	moved.Build.IdentityDocument.Commands[0].Argv[0] = "mutated-command"
	moved.Build.IdentityDocument.Environment[0][1] = "mutated-environment"
	moved.Build.IdentityDocument.Dependencies[0] = "mutated-dependency"
	if !reflect.DeepEqual(*document, documentBefore) {
		t.Fatalf("mutating moved identity document changed original: got=%#v want=%#v", document, documentBefore)
	}

	changedDocument := documentBefore
	changedDocument.Inputs = append([]ComponentBuildInputIdentity(nil), documentBefore.Inputs...)
	changedDocument.Inputs[0].Identity = "sha256:changed"
	changedBuild := buildBefore
	changedBuild.IdentityDocument = &changedDocument
	changedBuild.Identity = changedDocument.DigestForInstance("current")
	changedComponent := component
	changedComponent.Build = &changedBuild
	changedMoved := changedComponent.WithPhysicalName("legacy")
	if changedMoved.PhysicalComponentName() != moved.PhysicalComponentName() || changedMoved.Build.Identity == moved.Build.Identity {
		t.Fatalf("definition change did not rebuild under retained physical name: before=%#v after=%#v", moved, changedMoved)
	}
}

func TestComponentPhysicalIdentityIsNotSerialized(t *testing.T) {
	component := ComponentInstanceSpec{
		Name:         "current",
		PhysicalName: "legacy-physical-name",
		Build: &ComponentBuildSpec{
			Identity: "public-build-digest",
			IdentityDocument: &ComponentBuildIdentityDocument{
				Template: "protected-identity-document",
				Commands: []ComponentBuildCommandIdentity{{Argv: []string{"command", "protected-argument"}}},
			},
		},
	}
	encoded, err := json.Marshal(component)
	if err != nil {
		t.Fatal(err)
	}
	for _, protected := range []string{"legacy-physical-name", "protected-identity-document", "protected-argument"} {
		if strings.Contains(string(encoded), protected) {
			t.Fatalf("serialized component leaked %q: %s", protected, encoded)
		}
	}
	if !strings.Contains(string(encoded), `"name":"current"`) || !strings.Contains(string(encoded), `"identity":"public-build-digest"`) {
		t.Fatalf("serialized component omitted public logical identity: %s", encoded)
	}
}

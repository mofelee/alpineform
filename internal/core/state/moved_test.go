package state

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func TestResolveMovesRebasesKeysAndPreservesPhysicalState(t *testing.T) {
	oldRoot := "host.edge.component.old"
	newRoot := "host.edge.component.current"
	oldAddress := oldRoot + `.files.file["/etc/app/config"]`
	newAddress := newRoot + `.files.file["/etc/app/config"]`
	boundaryAddress := "host.edge.component.oldish" + `.files.file["/etc/app/config"]`

	desired := map[string]any{
		"path": "/etc/app/config",
		"nested": map[string]any{
			"addresses": []any{oldAddress, map[string]any{"value": oldRoot}},
		},
		"typed": map[string][]string{"values": {"one", "two"}},
	}
	observed := map[string]any{
		"remote": map[string]any{"sha256": "remote-sha", "values": []string{"a", "b"}},
	}
	deletion := map[string]any{
		"path":   "/etc/app/config",
		"nested": []map[string]any{{"remote_id": "unchanged"}},
	}
	st := Empty("edge")
	st.Serial = 9
	st.ComponentIdentities[oldRoot] = ComponentIdentity{PhysicalName: "first_name"}
	st.Resources[oldAddress] = Resource{
		Host: "edge", Kind: "file", Ownership: "managed", Desired: desired,
		DesiredDigest: "legacy-digest", Observed: observed, Delete: deletion,
		Order: 7, PreventDestroy: true, DeleteBehavior: "destroy", DigestSafe: true,
	}
	st.Resources[boundaryAddress] = Resource{Host: "edge", Kind: "file", DesiredDigest: "boundary"}
	before, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}

	result, err := ResolveMoves(
		st,
		[]ir.MovedSpec{movedSpec(oldRoot, newRoot)},
		map[string]bool{newRoot: true},
		map[string]MoveTarget{newAddress: {
			LegacyDesiredDigest: "legacy-digest",
			TargetDesiredDigest: "target-digest",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("ResolveMoves mutated input\ngot:  %s\nwant: %s", after, before)
	}
	if result.State.Serial != 9 || result.State.SchemaVersion != SchemaVersion {
		t.Fatalf("state metadata changed during pure move: %#v", result.State)
	}
	if len(result.Moves) != 1 || result.Moves[0] != (RealizedMove{Host: "edge", From: oldAddress, To: newAddress}) {
		t.Fatalf("realized moves = %#v", result.Moves)
	}
	if _, exists := result.State.Resources[oldAddress]; exists {
		t.Fatal("source resource remains after move")
	}
	if _, exists := result.State.Resources[boundaryAddress]; !exists {
		t.Fatal("segment-boundary neighbor was moved")
	}
	moved := result.State.Resources[newAddress]
	if !reflect.DeepEqual(moved.Desired, desired) || !reflect.DeepEqual(moved.Observed, observed) || !reflect.DeepEqual(moved.Delete, deletion) {
		t.Fatalf("remote state payload changed: %#v", moved)
	}
	if moved.DesiredDigest != "target-digest" || moved.Order != 7 || !moved.PreventDestroy || moved.DeleteBehavior != "destroy" || !moved.DigestSafe {
		t.Fatalf("resource metadata changed unexpectedly: %#v", moved)
	}
	if _, exists := result.State.ComponentIdentities[oldRoot]; exists {
		t.Fatal("source component identity remains after move")
	}
	if got := result.State.ComponentIdentities[newRoot].PhysicalName; got != "first_name" {
		t.Fatalf("persisted physical name = %q, want first_name", got)
	}
	if got := result.Bindings[newRoot].PhysicalName; got != "first_name" {
		t.Fatalf("resolved physical binding = %q, want first_name", got)
	}

	moved.Desired["nested"].(map[string]any)["addresses"].([]any)[1].(map[string]any)["value"] = "changed"
	moved.Desired["typed"].(map[string][]string)["values"][0] = "changed"
	moved.Observed["remote"].(map[string]any)["values"].([]string)[0] = "changed"
	moved.Delete["nested"].([]map[string]any)[0]["remote_id"] = "changed"
	if desired["nested"].(map[string]any)["addresses"].([]any)[1].(map[string]any)["value"] != oldRoot ||
		desired["typed"].(map[string][]string)["values"][0] != "one" ||
		observed["remote"].(map[string]any)["values"].([]string)[0] != "a" ||
		deletion["nested"].([]map[string]any)[0]["remote_id"] != "unchanged" {
		t.Fatal("moved resource shares nested payload storage with input")
	}
}

func TestResolveMovesRejectsWholeRootCollisionWithoutMutation(t *testing.T) {
	oldRoot := "host.edge.component.old"
	newRoot := "host.edge.component.current"
	st := Empty("edge")
	st.Resources[oldRoot+`.files.file["/etc/old"]`] = Resource{Host: "edge", Kind: "file"}
	st.Resources[newRoot+`.packages.package["curl"]`] = Resource{Host: "edge", Kind: "package"}
	before, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ResolveMoves(st, []ir.MovedSpec{movedSpec(oldRoot, newRoot)}, map[string]bool{newRoot: true}, nil)
	if err == nil || !strings.Contains(err.Error(), "both roots have tracked state") || !strings.Contains(err.Error(), "moved.from") {
		t.Fatalf("collision error = %v", err)
	}
	after, marshalErr := json.Marshal(st)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if string(after) != string(before) {
		t.Fatalf("collision mutated input\ngot:  %s\nwant: %s", after, before)
	}
}

func TestResolveMovesStagedHostAndIdempotentCases(t *testing.T) {
	oldRoot := "host.edge.component.old"
	newRoot := "host.edge.component.current"
	suffix := `.files.file["/etc/app"]`
	declaration := []ir.MovedSpec{movedSpec(oldRoot, newRoot)}

	t.Run("source present target desired", func(t *testing.T) {
		st := Empty("edge")
		st.Resources[oldRoot+suffix] = Resource{Host: "edge", Kind: "file"}
		result, err := ResolveMoves(st, declaration, map[string]bool{newRoot: true}, nil)
		if err != nil || len(result.Moves) != 1 || result.State.ComponentIdentities[newRoot].PhysicalName != "old" {
			t.Fatalf("result = %#v, err = %v", result, err)
		}
	})

	t.Run("already migrated", func(t *testing.T) {
		st := Empty("edge")
		st.Resources[newRoot+suffix] = Resource{Host: "edge", Kind: "file"}
		st.ComponentIdentities[newRoot] = ComponentIdentity{PhysicalName: "old"}
		result, err := ResolveMoves(st, declaration, map[string]bool{newRoot: true}, nil)
		if err != nil || len(result.Moves) != 0 || result.Bindings[newRoot].PhysicalName != "old" {
			t.Fatalf("result = %#v, err = %v", result, err)
		}
	})

	t.Run("neither present", func(t *testing.T) {
		result, err := ResolveMoves(Empty("edge"), declaration, map[string]bool{newRoot: true}, nil)
		if err != nil || len(result.Moves) != 0 || len(result.State.Resources) != 0 || len(result.State.ComponentIdentities) != 0 {
			t.Fatalf("result = %#v, err = %v", result, err)
		}
	})

	t.Run("source host not rolled out", func(t *testing.T) {
		st := Empty("edge")
		st.Resources[oldRoot+suffix] = Resource{Host: "edge", Kind: "file"}
		result, err := ResolveMoves(st, declaration, map[string]bool{oldRoot: true}, nil)
		if err != nil || len(result.Moves) != 0 || result.Bindings[oldRoot].PhysicalName != "old" {
			t.Fatalf("result = %#v, err = %v", result, err)
		}
		if _, exists := result.State.Resources[oldRoot+suffix]; !exists {
			t.Fatal("staged source state moved before its desired graph changed")
		}
	})

	t.Run("destination is not desired", func(t *testing.T) {
		st := Empty("edge")
		st.Resources[oldRoot+suffix] = Resource{Host: "edge", Kind: "file"}
		_, err := ResolveMoves(st, declaration, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "not present in the desired graph") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestResolveMovesStagedHostsIndependently(t *testing.T) {
	type hostCase struct {
		name         string
		alreadyMoved bool
	}
	for _, test := range []hostCase{{name: "edge1"}, {name: "edge2", alreadyMoved: true}} {
		t.Run(test.name, func(t *testing.T) {
			oldRoot := "host." + test.name + ".component.old"
			newRoot := "host." + test.name + ".component.current"
			suffix := `.files.file["/etc/app"]`
			st := Empty(test.name)
			if test.alreadyMoved {
				st.Resources[newRoot+suffix] = Resource{Host: test.name, Kind: "file"}
				st.ComponentIdentities[newRoot] = ComponentIdentity{PhysicalName: "old"}
			} else {
				st.Resources[oldRoot+suffix] = Resource{Host: test.name, Kind: "file"}
			}
			result, err := ResolveMoves(st, []ir.MovedSpec{movedSpec(oldRoot, newRoot)}, map[string]bool{newRoot: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			wantMoves := 1
			if test.alreadyMoved {
				wantMoves = 0
			}
			if len(result.Moves) != wantMoves || result.Bindings[newRoot].PhysicalName != "old" {
				t.Fatalf("staged host result = %#v", result)
			}
		})
	}
}

func TestResolveMovesChainsKeepEarliestPhysicalNameDeterministically(t *testing.T) {
	oldRoot := "host.edge.component.old"
	middleRoot := "host.edge.component.middle"
	currentRoot := "host.edge.component.current"
	suffix := `.build.install["/usr/local/bin/app"]`
	st := Empty("edge")
	st.ComponentIdentities[oldRoot] = ComponentIdentity{PhysicalName: "first_name"}
	st.Resources[oldRoot+suffix] = Resource{Host: "edge", Kind: "component_build_install", DesiredDigest: "legacy"}
	declarations := []ir.MovedSpec{
		movedSpec(middleRoot, currentRoot),
		movedSpec(oldRoot, middleRoot),
	}

	result, err := ResolveMoves(st, declarations, map[string]bool{currentRoot: true}, map[string]MoveTarget{
		currentRoot + suffix: {LegacyDesiredDigest: "legacy", TargetDesiredDigest: "target"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantMoves := []RealizedMove{
		{Host: "edge", From: oldRoot + suffix, To: middleRoot + suffix},
		{Host: "edge", From: middleRoot + suffix, To: currentRoot + suffix},
	}
	if !reflect.DeepEqual(result.Moves, wantMoves) {
		t.Fatalf("chain moves = %#v, want %#v", result.Moves, wantMoves)
	}
	if result.State.ComponentIdentities[currentRoot].PhysicalName != "first_name" || result.State.Resources[currentRoot+suffix].DesiredDigest != "target" {
		t.Fatalf("chained state = %#v", result.State)
	}

	retry, err := ResolveMoves(result.State, declarations, map[string]bool{currentRoot: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(retry.Moves) != 0 || retry.State.ComponentIdentities[currentRoot].PhysicalName != "first_name" || !reflect.DeepEqual(retry.State.Resources, result.State.Resources) {
		t.Fatalf("idempotent retry = %#v", retry)
	}
}

func TestResolveMovesRebasesCanonicalDependencyChainsAndExternalSurvivors(t *testing.T) {
	oldRoot := "host.edge.component.old"
	middleRoot := "host.edge.component.middle"
	currentRoot := "host.edge.component.current"
	boundaryRoot := "host.edge.component.oldish"
	fileSuffix := `.files.file["/etc/app"]`
	packageSuffix := `.packages.package["bird"]`
	oldFile := oldRoot + fileSuffix
	currentFile := currentRoot + fileSuffix
	oldPackage := oldRoot + packageSuffix
	currentPackage := currentRoot + packageSuffix
	boundaryDependency := boundaryRoot + packageSuffix
	externalAddress := `host.edge.files.file["/etc/external"]`

	st := Empty("edge")
	st.Resources[oldFile] = Resource{
		Host:      "edge",
		Kind:      "file",
		DependsOn: []string{boundaryDependency, oldPackage, oldPackage},
	}
	st.Resources[externalAddress] = Resource{
		Host:      "edge",
		Kind:      "file",
		DependsOn: []string{boundaryDependency, oldFile},
	}
	before, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}

	result, err := ResolveMoves(st, []ir.MovedSpec{
		movedSpec(middleRoot, currentRoot),
		movedSpec(oldRoot, middleRoot),
	}, map[string]bool{currentRoot: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("ResolveMoves mutated dependency input\ngot:  %s\nwant: %s", after, before)
	}
	wantMoves := []RealizedMove{
		{Host: "edge", From: oldFile, To: middleRoot + fileSuffix},
		{Host: "edge", From: middleRoot + fileSuffix, To: currentFile},
	}
	if !reflect.DeepEqual(result.Moves, wantMoves) {
		t.Fatalf("dependency chain moves = %#v, want %#v", result.Moves, wantMoves)
	}
	if got, want := result.State.Resources[currentFile].DependsOn, []string{currentPackage, boundaryDependency}; !reflect.DeepEqual(got, want) {
		t.Fatalf("moved dependencies = %#v, want %#v", got, want)
	}
	if got, want := result.State.Resources[externalAddress].DependsOn, []string{currentFile, boundaryDependency}; !reflect.DeepEqual(got, want) {
		t.Fatalf("external survivor dependencies = %#v, want %#v", got, want)
	}

	moved := result.State.Resources[currentFile]
	moved.DependsOn[0] = "changed"
	if got := st.Resources[oldFile].DependsOn[0]; got != boundaryDependency {
		t.Fatalf("moved dependencies share input storage: %#v", st.Resources[oldFile].DependsOn)
	}
}

func TestResolveMovesValidatesMappingsAndBindingsWithoutDeclarations(t *testing.T) {
	newRoot := "host.edge.component.current"
	oldRoot := "host.edge.component.old"

	t.Run("retained physical name collides with fresh logical component", func(t *testing.T) {
		st := Empty("edge")
		st.ComponentIdentities[newRoot] = ComponentIdentity{PhysicalName: "old"}
		st.Resources[newRoot+`.files.file["/etc/app"]`] = Resource{Host: "edge", Kind: "file"}
		_, err := ResolveMoves(st, nil, map[string]bool{newRoot: true, oldRoot: true}, nil)
		if err == nil || !strings.Contains(err.Error(), `both resolve to physical component name "old"`) {
			t.Fatalf("physical collision error = %v", err)
		}
	})

	tests := []struct {
		name       string
		identities map[string]ComponentIdentity
		desired    map[string]bool
		want       string
	}{
		{
			name:       "cross-host logical root",
			identities: map[string]ComponentIdentity{"host.other.component.current": {PhysicalName: "old"}},
			want:       "not valid for host",
		},
		{
			name:       "leaf logical root",
			identities: map[string]ComponentIdentity{newRoot + ".files": {PhysicalName: "old"}},
			want:       "not valid for host",
		},
		{
			name:       "malformed physical name",
			identities: map[string]ComponentIdentity{newRoot: {PhysicalName: "bad.name"}},
			want:       "invalid physical name",
		},
		{
			name: "duplicate mapped physical name",
			identities: map[string]ComponentIdentity{
				newRoot:                              {PhysicalName: "legacy"},
				"host.edge.component.another_target": {PhysicalName: "legacy"},
			},
			want: "both use physical component name",
		},
		{
			name:    "cross-host desired root",
			desired: map[string]bool{"host.other.component.current": true},
			want:    "desired component root",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			st := Empty("edge")
			st.ComponentIdentities = test.identities
			_, err := ResolveMoves(st, nil, test.desired, nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestResolveMovesDefendsAgainstMalformedMoveGraphs(t *testing.T) {
	oldRoot := "host.edge.component.old"
	newRoot := "host.edge.component.current"
	otherRoot := "host.edge.component.other"
	tests := []struct {
		name  string
		moves []ir.MovedSpec
		want  string
	}{
		{name: "cross host", moves: []ir.MovedSpec{movedSpec("host.other.component.old", newRoot)}, want: "not a component root"},
		{name: "leaf", moves: []ir.MovedSpec{movedSpec(oldRoot+".files", newRoot)}, want: "not a component root"},
		{name: "self", moves: []ir.MovedSpec{movedSpec(oldRoot, oldRoot)}, want: "cannot move to itself"},
		{name: "duplicate source", moves: []ir.MovedSpec{movedSpec(oldRoot, newRoot), movedSpec(oldRoot, otherRoot)}, want: "declared more than once"},
		{name: "many to one", moves: []ir.MovedSpec{movedSpec(oldRoot, newRoot), movedSpec(otherRoot, newRoot)}, want: "both target"},
		{name: "cycle", moves: []ir.MovedSpec{movedSpec(newRoot, oldRoot), movedSpec(oldRoot, newRoot)}, want: "cycle through " + newRoot},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for range 10 {
				_, err := ResolveMoves(Empty("edge"), test.moves, nil, nil)
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("error = %v, want containing %q", err, test.want)
				}
			}
		})
	}
}

func TestComponentIdentityCleanupUsesSegmentBoundaries(t *testing.T) {
	root := "host.edge.component.current"
	st := Empty("edge")
	st.ComponentIdentities[root] = ComponentIdentity{PhysicalName: "old"}
	st.Resources[root+`.files.file["/etc/app"]`] = Resource{Host: "edge", Kind: "file"}
	st.Resources["host.edge.component.currently"+`.files.file["/etc/other"]`] = Resource{Host: "edge", Kind: "file"}

	retained, err := PruneComponentIdentities(st)
	if err != nil {
		t.Fatal(err)
	}
	if retained.ComponentIdentities[root].PhysicalName != "old" {
		t.Fatalf("identity was cleared while resources remained: %#v", retained.ComponentIdentities)
	}
	preparedWithResource, err := PrepareWrite(st, "edge", time.Unix(99, 0))
	if err != nil {
		t.Fatal(err)
	}
	if preparedWithResource.ComponentIdentities[root].PhysicalName != "old" {
		t.Fatalf("write cleared identity while resources remained: %#v", preparedWithResource.ComponentIdentities)
	}
	delete(retained.Resources, root+`.files.file["/etc/app"]`)
	pruned, err := PruneComponentIdentities(retained)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := pruned.ComponentIdentities[root]; exists {
		t.Fatalf("identity remained after logical root emptied: %#v", pruned.ComponentIdentities)
	}
	if _, exists := st.ComponentIdentities[root]; !exists {
		t.Fatal("identity cleanup mutated its input")
	}
	prepared, err := PrepareWrite(retained, "edge", time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := prepared.ComponentIdentities[root]; exists {
		t.Fatalf("write retained identity after logical root emptied: %#v", prepared.ComponentIdentities)
	}
}

func TestResolveMovesRejectsIncompleteTargetDigestPair(t *testing.T) {
	oldRoot := "host.edge.component.old"
	newRoot := "host.edge.component.current"
	suffix := `.files.file["/etc/app"]`
	st := Empty("edge")
	st.Resources[oldRoot+suffix] = Resource{Host: "edge", Kind: "file", DesiredDigest: "legacy"}
	_, err := ResolveMoves(st, []ir.MovedSpec{movedSpec(oldRoot, newRoot)}, map[string]bool{newRoot: true}, map[string]MoveTarget{
		newRoot + suffix: {LegacyDesiredDigest: "legacy"},
	})
	if err == nil || !strings.Contains(err.Error(), "must set both legacy and target desired digests") {
		t.Fatalf("target metadata error = %v", err)
	}
}

func TestResolveMovesDoesNotMaskRealDesiredChange(t *testing.T) {
	oldRoot := "host.edge.component.old"
	newRoot := "host.edge.component.current"
	suffix := `.files.file["/etc/app"]`
	st := Empty("edge")
	st.Resources[oldRoot+suffix] = Resource{Host: "edge", Kind: "file", DesiredDigest: "independent-change"}
	result, err := ResolveMoves(st, []ir.MovedSpec{movedSpec(oldRoot, newRoot)}, map[string]bool{newRoot: true}, map[string]MoveTarget{
		newRoot + suffix: {LegacyDesiredDigest: "legacy", TargetDesiredDigest: "target"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.State.Resources[newRoot+suffix].DesiredDigest; got != "independent-change" {
		t.Fatalf("desired digest = %q, want independent-change", got)
	}
}

func movedSpec(from, to string) ir.MovedSpec {
	return ir.MovedSpec{
		From:       from,
		To:         to,
		Source:     ir.SourceRef{File: "main.apf.hcl", Line: 1, Path: "moved"},
		FromSource: ir.SourceRef{File: "main.apf.hcl", Line: 2, Path: "moved.from"},
		ToSource:   ir.SourceRef{File: "main.apf.hcl", Line: 3, Path: "moved.to"},
	}
}

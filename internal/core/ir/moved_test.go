package ir

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestHostSpecMovedJSONPreservesEndpointSources(t *testing.T) {
	want := MovedSpec{
		From:       "host.edge.component.old",
		To:         "host.edge.component.new",
		Source:     SourceRef{File: "main.apf.hcl", Line: 1, Path: "moved[component.old]"},
		FromSource: SourceRef{File: "main.apf.hcl", Line: 2, Path: "moved[component.old].from"},
		ToSource:   SourceRef{File: "main.apf.hcl", Line: 3, Path: "moved[component.old].to"},
	}
	encoded, err := json.Marshal(HostSpec{Name: "edge", Moves: []MovedSpec{want}})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"moves"`, `"from_source"`, `"to_source"`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("HostSpec JSON = %s, missing %s", encoded, field)
		}
	}

	var decoded HostSpec
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Moves) != 1 || !reflect.DeepEqual(decoded.Moves[0], want) {
		t.Fatalf("decoded moves = %#v, want %#v", decoded.Moves, want)
	}
}

func TestHostSpecMovedJSONOmitsEmptyMoves(t *testing.T) {
	encoded, err := json.Marshal(HostSpec{Name: "edge"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"moves"`) {
		t.Fatalf("empty HostSpec JSON includes moves: %s", encoded)
	}
}

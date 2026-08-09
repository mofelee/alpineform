package merge

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCompileProjectsOnlyRealizedMovedChainsToEachHost(t *testing.T) {
	secret := "not-a-real-moved-input-secret"
	config, err := compileConfig(t, `moved {
  from = component.z_old
  to   = component.z_new
}

moved {
  from = component.a_old
  to   = component.a_middle
}

moved {
  from = component.a_middle
  to   = component.a_new
}

component "app" {
  input "token" {
    type      = string
    default   = "`+secret+`"
    sensitive = true
    ephemeral = true
  }
}

host "source_only" {
  component "a_old" {
    source = component.app
  }
}

host "middle" {
  component "a_middle" {
    source = component.app
  }
}

host "targets" {
  component "a_new" {
    source = component.app
  }
  component "z_new" {
    source = component.app
  }
}

host "unrelated" {
  component "other" {
    source = component.app
  }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{program.Hosts[0].Name, program.Hosts[1].Name, program.Hosts[2].Name, program.Hosts[3].Name}; !reflect.DeepEqual(got, []string{"middle", "source_only", "targets", "unrelated"}) {
		t.Fatalf("hosts = %#v", program.Hosts)
	}

	if program.Hosts[1].Moves != nil || program.Hosts[3].Moves != nil {
		t.Fatalf("source-only/unrelated moves = %#v / %#v", program.Hosts[1].Moves, program.Hosts[3].Moves)
	}
	middleMove := program.Hosts[0].Moves
	if len(middleMove) != 1 || middleMove[0].From != "host.middle.component.a_old" || middleMove[0].To != "host.middle.component.a_middle" {
		t.Fatalf("middle moves = %#v", middleMove)
	}
	targetMoves := program.Hosts[2].Moves
	wantEndpoints := []string{
		"host.targets.component.a_old",
		"host.targets.component.a_middle",
		"host.targets.component.a_middle",
		"host.targets.component.a_new",
		"host.targets.component.z_old",
		"host.targets.component.z_new",
	}
	gotEndpoints := make([]string, 0, len(targetMoves)*2)
	for _, move := range targetMoves {
		gotEndpoints = append(gotEndpoints, move.From, move.To)
	}
	if !reflect.DeepEqual(gotEndpoints, wantEndpoints) {
		t.Fatalf("target move endpoints = %#v, want %#v", gotEndpoints, wantEndpoints)
	}
	for _, move := range append(middleMove, targetMoves...) {
		if move.Source.File == "" || move.FromSource.Path == "" || move.ToSource.Path == "" {
			t.Fatalf("projected move lost source references: %#v", move)
		}
	}

	encoded, err := json.Marshal(program)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("program JSON leaked protected input: %s", encoded)
	}
}

func TestCompileRejectsAmbiguousMountedMovedChainWithSource(t *testing.T) {
	config, err := compileConfig(t, `moved {
  from = component.old
  to   = component.middle
}

moved {
  from = component.middle
  to   = component.new
}

component "app" {}

host "edge" {
  component "old" {
    source = component.app
  }
  component "new" {
    source = component.app
  }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(config)
	if err == nil || !strings.Contains(err.Error(), "ambiguous moved chain on host edge") || !strings.Contains(err.Error(), "component.old, component.new") || !strings.Contains(err.Error(), `host["edge"].component["new"]`) {
		t.Fatalf("Compile() error = %v, want source-located ambiguous mount diagnostic", err)
	}
}

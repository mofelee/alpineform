package merge

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestCompileSourceBuildRejectsUnsafeContractBeforeExecution(t *testing.T) {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte("source")))
	tests := []struct {
		name, build, want string
	}{
		{"shell command string", "command { argv = \"cc -o tool source.c\" }\noutput = \"tool\"", "argv must"},
		{"workspace escape", "command { argv = [\"cc\"] }\noutput = \"../tool\"", "must not escape the workspace"},
		{"output overlap", "command { argv = [\"cc\"] }\noutput = \"source.c\"", "must not overlap"},
		{"package intent", "command { argv = [\"cc\"] }\noutput = \"tool\"\ndependencies = [\"gcc@edge\"]", "unversioned APK package"},
		{"environment key", "command { argv = [\"cc\"] }\noutput = \"tool\"\nenvironment = { PATH = \"/tmp\" }", "key \"PATH\" is not allowed"},
		{"network", "command { argv = [\"cc\"] }\noutput = \"tool\"\nnetwork = \"host\"", "network-enabled target builds are unsupported"},
		{"removal", "command { argv = [\"cc\"] }\noutput = \"tool\"\non_remove = \"delete\"", "forget\" or \"destroy"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, `
component "tool" {
  type = "source"
  build {
    input "source" {
      content     = "source"
      sha256      = "`+digest+`"
      destination = "source.c"
    }
    `+test.build+`
  }
  install { path = "/usr/local/bin/tool" }
}
host "node" {
  platform { architecture = "x86_64" }
  component "tool" { source = component.tool }
}
`)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Compile(config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compile() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCompileSourceBuildIdentityIsDeterministicAndDefinitionSensitive(t *testing.T) {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte("source")))
	compileIdentity := func(optimization string) string {
		config, err := compileConfig(t, `
component "tool" {
  type = "source"
  build {
    input "source" {
      content     = "source"
      sha256      = "`+digest+`"
      destination = "source.c"
    }
    command { argv = ["cc", "`+optimization+`", "-o", "tool", "source.c"] }
    output = "tool"
  }
  install { path = "/usr/local/bin/tool" }
}
host "node" {
  platform { architecture = "x86_64" }
  component "tool" { source = component.tool }
}
`)
		if err != nil {
			t.Fatal(err)
		}
		program, err := Compile(config)
		if err != nil {
			t.Fatal(err)
		}
		return program.Hosts[0].Components[0].Build.Identity
	}
	first := compileIdentity("-Os")
	if again := compileIdentity("-Os"); first != again {
		t.Fatalf("identical definitions produced %q and %q", first, again)
	}
	if changed := compileIdentity("-O2"); first == changed {
		t.Fatalf("definition drift did not change identity %q", first)
	}
}

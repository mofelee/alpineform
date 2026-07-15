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
		{"ambiguous output", "command { argv = [\"cc\"] }\noutput = \"dist/*.bin\"", "clean relative workspace path"},
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

func TestCompileSourceBuildInputOrderingIsDeterministic(t *testing.T) {
	oneDigest := fmt.Sprintf("%x", sha256.Sum256([]byte("one")))
	twoDigest := fmt.Sprintf("%x", sha256.Sum256([]byte("two")))
	first := `
    input "one" {
      content = "one"
      sha256 = "` + oneDigest + `"
      destination = "one.txt"
    }
`
	second := `
    input "two" {
      content = "two"
      sha256 = "` + twoDigest + `"
      destination = "two.txt"
    }
`
	compileIdentity := func(inputs string) string {
		config, err := compileConfig(t, `
component "tool" {
  type = "source"
  build {
`+inputs+`
    command { argv = ["cp", "one.txt", "tool"] }
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
	if left, right := compileIdentity(first+second), compileIdentity(second+first); left != right {
		t.Fatalf("input declaration order changed identity: %q != %q", left, right)
	}
}

func TestCompileSourceBuildArchiveInputContract(t *testing.T) {
	config, err := compileConfig(t, `
component "tool" {
  type = "source"
  build {
    input "source" {
      url = "https://example.invalid/tool-1.0.tar.gz"
      sha256 = "`+artifactSHA+`"
      destination = "src"
      extract {
        format = "tar.gz"
        strip_components = 1
      }
    }
    command { argv = ["make", "-C", "src"] }
    output = "src/tool"
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
	if err == nil || !strings.Contains(err.Error(), "must not overlap declared output") {
		t.Fatalf("overlapping archive output error = %v", err)
	}

	config, err = compileConfig(t, strings.ReplaceAll(`
component "tool" {
  type = "source"
  build {
    input "source" {
      url = "https://example.invalid/tool-1.0.tar.gz"
      sha256 = "SHA"
      destination = "src"
      extract { strip_components = 1 }
    }
    command { argv = ["make", "-C", "src", "BUILD_DIR=../build"] }
    output = "build/tool"
  }
  install { path = "/usr/local/bin/tool" }
}
host "node" {
  platform { architecture = "x86_64" }
  component "tool" { source = component.tool }
}
`, "SHA", artifactSHA))
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	extract := program.Hosts[0].Components[0].Build.Inputs[0].Extract
	if extract == nil || extract.Format != "tar.gz" || extract.StripComponents != 1 {
		t.Fatalf("compiled extract = %#v", extract)
	}
}

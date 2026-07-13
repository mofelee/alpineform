package parser

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseScriptsAndArtifactOnChangeReferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scripts.apf.hcl")
	writeConfig(t, path, `
script "refresh" {
  commands = [["rc-service", "worker", "reload"]]
  outputs  = ["/run/worker.refreshed"]
}
component "worker" {
  script "local" {
    interpreter = ["/bin/sh", "-eu"]
    content     = "echo refreshed"
  }
  type = "file"
  source {
    url    = "https://example.invalid/worker"
    sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  }
  install {
    path      = "/etc/worker"
    on_change = global.script.refresh
  }
}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	root, err := EvaluateScript(config.Scripts["refresh"], EvalContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !root.Executable || len(root.Commands) != 1 || len(root.Outputs) != 1 {
		t.Fatalf("root script = %#v", root)
	}
	component := config.Components["worker"]
	if len(component.Scripts) != 1 || component.Install == nil || component.Install.OnChange == nil || component.Install.OnChange.Name != "refresh" || component.Install.OnChange.Scope != ScriptReferenceGlobal {
		t.Fatalf("component scripts/install = %#v / %#v", component.Scripts, component.Install)
	}
}

func TestEvaluateScriptRejectsAmbiguousOrUnsafeBodies(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "ambiguous", body: `commands = [["true"]]
content = "true"`, want: "mutually exclusive"},
		{name: "relative output", body: `commands = [["true"]]
outputs = ["relative"]`, want: "clean absolute"},
		{name: "empty command", body: `commands = [[]]`, want: "at least one string"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.apf.hcl")
			writeConfig(t, path, "script \"bad\" {\n"+test.body+"\n}\n")
			config, err := ParseFiles([]string{path})
			if err != nil {
				t.Fatal(err)
			}
			_, err = EvaluateScript(config.Scripts["bad"], EvalContext{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("EvaluateScript() error = %v, want %q", err, test.want)
			}
		})
	}
}

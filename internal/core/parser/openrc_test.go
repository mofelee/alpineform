package parser

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBoundedOpenRCService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.apf.hcl")
	writeConfig(t, path, `
host "node" {
  openrc {
    service "worker" {
      command            = "/usr/local/bin/worker"
      command_args       = ["--listen", "127.0.0.1:9000"]
      command_user       = "worker"
      directory          = "/srv/worker"
      command_background = true
      pidfile            = "/run/worker.pid"
      description        = "Example worker"
      need               = ["net"]
      use                = ["logger"]
      conf               = "WORKERS=2\n"
      lifecycle { prevent_destroy = true }
    }
  }
}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	openrc := config.Hosts["node"].OpenRC
	if openrc == nil || len(openrc.Services) != 1 || openrc.Services[0].Kind != ResourceOpenRCService || openrc.Services[0].Label != "worker" || !openrc.Services[0].Lifecycle.PreventDestroy {
		t.Fatalf("parsed OpenRC = %#v", openrc)
	}
}

func TestParseOpenRCRejectsUnboundedSurface(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "top attribute", body: `stack = "custom"`, wantErr: "containing only service blocks"},
		{name: "custom hook", body: `service "app" { start_pre = "echo unsafe" }`, wantErr: "unsupported attribute"},
		{name: "nested function", body: "service \"app\" {\n      function \"start\" {}\n    }", wantErr: "unsupported block"},
		{name: "wrong child", body: `runlevel "custom" {}`, wantErr: "supports only service blocks"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.apf.hcl")
			writeConfig(t, path, "host \"node\" {\n  openrc {\n    "+test.body+"\n  }\n}\n")
			_, err := ParseFiles([]string{path})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ParseFiles() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

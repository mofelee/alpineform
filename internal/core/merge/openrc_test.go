package merge

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCompileOpenRCGeneratesDeterministicQuotedFiles(t *testing.T) {
	config, err := compileConfig(t, `
host "node" {
  openrc {
    service "worker" {
      command            = "/usr/local/bin/worker"
      command_args       = ["--label", "O'Reilly worker", "$(not-a-command)"]
      command_user       = "worker"
      directory          = "/srv/worker data"
      command_background = true
      pidfile            = "/run/worker.pid"
      description        = "Worker's daemon"
      need               = ["net", "localmount"]
      use                = ["logger"]
      before             = ["shutdown"]
      conf               = "WORKERS=2\n"
    }
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
	host := program.Hosts[0]
	if len(host.OpenRC) != 1 || len(host.Files) != 2 {
		t.Fatalf("compiled OpenRC host = %#v", host)
	}
	init := host.Files[0]
	want := `#!/sbin/openrc-run
description='Worker'"'"'s daemon'
command='/usr/local/bin/worker'
__ARGS_LINE__
command_user='worker'
directory='/srv/worker data'
command_background='yes'
pidfile='/run/worker.pid'

depend() {
	need 'localmount' 'net'
	use 'logger'
	before 'shutdown'
}
`
	argsValue := strings.Join([]string{shellQuote("--label"), shellQuote("O'Reilly worker"), shellQuote("$(not-a-command)")}, " ")
	want = strings.Replace(want, "__ARGS_LINE__", "command_args="+shellQuote(argsValue), 1)
	if init.Path != "/etc/init.d/worker" || init.Mode != "0755" || init.Content != want || len(init.ContentSHA256) != 64 {
		t.Fatalf("generated init file = %#v\ncontent:\n%s\nwant:\n%s", init, init.Content, want)
	}
	conf := host.Files[1]
	if conf.Path != "/etc/conf.d/worker" || conf.Mode != "0644" || conf.Content != "WORKERS=2\n" || conf.OnRemove != "forget" {
		t.Fatalf("generated conf file = %#v", conf)
	}
	if strings.Contains(init.Content, "command_args='--label O'Reilly") || !strings.Contains(init.Content, `'"'"'`) || !strings.Contains(init.Content, "$(not-a-command)") {
		t.Fatalf("command arguments were not deterministically shell quoted:\n%s", init.Content)
	}
	command := exec.Command("sh", "-c", init.Content+"\nprintf '%s' \"$command_args\"\n")
	output, err := command.CombinedOutput()
	if err != nil || string(output) != argsValue {
		t.Fatalf("generated init script argument value = %q, error = %v, want %q", output, err, argsValue)
	}
}

func TestCompileOpenRCValidationRejectsUnsafeOrAmbiguousDeclarations(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "name", body: "service \"bad/name\" {\n      command = \"/bin/true\"\n    }", wantErr: "service name"},
		{name: "missing command", body: `service "app" {}`, wantErr: "command"},
		{name: "relative command", body: "service \"app\" {\n      command = \"bin/app\"\n    }", wantErr: "clean absolute path"},
		{name: "background pidfile", body: "service \"app\" {\n      command = \"/bin/app\"\n      command_background = true\n    }", wantErr: "pidfile"},
		{name: "account", body: "service \"app\" {\n      command = \"/bin/app\"\n      command_user = \"bad.name\"\n    }", wantErr: "account name"},
		{name: "dependency", body: "service \"app\" {\n      command = \"/bin/app\"\n      need = [\"net; reboot\"]\n    }", wantErr: "dependency name"},
		{name: "duplicate dependency", body: "service \"app\" {\n      command = \"/bin/app\"\n      need = [\"net\", \"net\"]\n    }", wantErr: "duplicate OpenRC dependency"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, "host \"node\" {\n  openrc {\n    "+test.body+"\n  }\n}\n")
			if err != nil {
				if strings.Contains(err.Error(), test.wantErr) {
					return
				}
				t.Fatal(err)
			}
			_, err = Compile(config)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Compile() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestCompileOpenRCRejectsGeneratedFileConflict(t *testing.T) {
	config, err := compileConfig(t, `
host "node" {
  files {
    file "/etc/init.d/app" { content = "raw" }
  }
  openrc {
    service "app" { command = "/bin/true" }
  }
}
`)
	if err == nil {
		_, err = Compile(config)
	}
	if err == nil || !strings.Contains(err.Error(), "duplicates a file declared") {
		t.Fatalf("generated file conflict error = %v", err)
	}
}

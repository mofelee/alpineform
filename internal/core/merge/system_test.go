package merge

import (
	"strings"
	"testing"
)

func TestCompileSystemInjectsTimezonePackage(t *testing.T) {
	config, err := compileConfig(t, `
host "node" {
  system {
    hostname = "edge.example"
    timezone = "Asia/Shanghai"
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
	if host.System == nil || host.System.Hostname != "edge.example" || host.System.Timezone != "Asia/Shanghai" || len(host.Packages) != 1 || host.Packages[0].Name != "tzdata" || host.Packages[0].Ensure != "present" {
		t.Fatalf("compiled system host = %#v", host)
	}
}

func TestCompileSystemOmissionHasNoResources(t *testing.T) {
	config, err := compileConfig(t, `host "node" {}`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	if program.Hosts[0].System != nil || len(program.Hosts[0].Packages) != 0 {
		t.Fatalf("implicit system resources = %#v", program.Hosts[0])
	}
}

func TestCompileSystemRejectsInvalidValuesAndTZDataConflict(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "empty hostname", body: `system { hostname = "" }`, want: "RFC 1123 hostname"},
		{name: "hostname label", body: `system { hostname = "bad_name" }`, want: "invalid RFC 1123 label"},
		{name: "absolute timezone", body: `system { timezone = "/etc/passwd" }`, want: "relative zoneinfo name"},
		{name: "timezone traversal", body: `system { timezone = "Europe/../passwd" }`, want: "parent path segments"},
		{name: "tzdata absent", body: "system { timezone = \"UTC\" }\npackages {\n  package \"tzdata\" {\n    ensure = \"absent\"\n  }\n}", want: "requires package tzdata"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, "host \"node\" {\n"+test.body+"\n}\n")
			if err == nil {
				_, err = Compile(config)
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("system validation error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCompileSystemReusesExplicitTZDataPackage(t *testing.T) {
	config, err := compileConfig(t, `
host "node" {
  system { timezone = "UTC" }
  packages {
    package "tzdata" {}
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
	if len(program.Hosts[0].Packages) != 1 || program.Hosts[0].Packages[0].Name != "tzdata" {
		t.Fatalf("timezone packages = %#v", program.Hosts[0].Packages)
	}
}

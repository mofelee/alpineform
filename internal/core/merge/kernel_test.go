package merge

import (
	"strings"
	"testing"
)

func TestCompileKernelDefaultsAndRuntimeControl(t *testing.T) {
	config, err := compileConfig(t, `
host "node" {
  kernel {
    module "loop" {}
    sysctl "vm.swappiness" {
      value = "10"
    }
    sysctl "net.ipv4.ip_forward" {
      value         = "1"
      apply_runtime = false
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
	kernel := program.Hosts[0].Kernel
	if kernel == nil || len(kernel.Modules) != 1 || kernel.Modules[0].Name != "loop" || len(kernel.Sysctls) != 2 {
		t.Fatalf("compiled kernel = %#v", kernel)
	}
	values := map[string]bool{}
	for _, sysctl := range kernel.Sysctls {
		values[sysctl.Key] = sysctl.ApplyRuntime
	}
	if values["net.ipv4.ip_forward"] || !values["vm.swappiness"] {
		t.Fatalf("sysctl runtime values = %#v", values)
	}
}

func TestCompileKernelRejectsUnsupportedOrUnsafeValues(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "module absent", body: `module "loop" { ensure = "absent" }`, want: "automatic absence or unload is unsupported"},
		{name: "module name", body: `module "bad/name" {}`, want: "module name"},
		{name: "sysctl key", body: `sysctl "net..forward" { value = "1" }`, want: "sysctl key"},
		{name: "missing value", body: `sysctl "vm.swappiness" {}`, want: "required non-empty string"},
		{name: "empty value", body: `sysctl "vm.swappiness" { value = "" }`, want: "required non-empty string"},
		{name: "line break", body: `sysctl "vm.swappiness" { value = "one\ntwo" }`, want: "without NUL or line breaks"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, "host \"node\" {\n  kernel {\n    "+test.body+"\n  }\n}\n")
			if err == nil {
				_, err = Compile(config)
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("kernel validation error = %v, want %q", err, test.want)
			}
		})
	}
}

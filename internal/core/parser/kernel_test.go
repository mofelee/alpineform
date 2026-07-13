package parser

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseKernelResources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.apf.hcl")
	writeConfig(t, path, `
host "node" {
  kernel {
    module "br_netfilter" {
      lifecycle { prevent_destroy = true }
    }
    sysctl "net.ipv4.ip_forward" {
      value         = "1"
      apply_runtime = false
    }
  }
}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	kernel := config.Hosts["node"].Kernel
	if kernel == nil || len(kernel.Modules) != 1 || len(kernel.Sysctls) != 1 || kernel.Modules[0].Kind != ResourceKernelModule || !kernel.Modules[0].Lifecycle.PreventDestroy || kernel.Sysctls[0].Attributes["value"].Expression == nil {
		t.Fatalf("parsed kernel = %#v", kernel)
	}
}

func TestParseKernelRejectsInvalidShapeAndDuplicates(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "attribute", body: `kernel { modules = ["loop"] }`, want: "must be an unlabeled block"},
		{name: "unknown block", body: "kernel {\n  parameter \"x\" {}\n}", want: "unsupported block"},
		{name: "unknown module attribute", body: "kernel {\n  module \"loop\" { persist = true }\n}", want: "unsupported attribute"},
		{name: "duplicate module", body: "kernel {\n  module \"loop\" {}\n  module \"loop\" {}\n}", want: "duplicate module label"},
		{name: "duplicate sysctl", body: "kernel {\n  sysctl \"vm.swappiness\" { value = \"1\" }\n  sysctl \"vm.swappiness\" { value = \"2\" }\n}", want: "duplicate sysctl label"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.apf.hcl")
			writeConfig(t, path, "host \"node\" {\n"+test.body+"\n}\n")
			_, err := ParseFiles([]string{path})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseFiles() error = %v, want %q", err, test.want)
			}
		})
	}
}

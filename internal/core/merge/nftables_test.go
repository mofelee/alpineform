package merge

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompileNftablesIdentityDefaultsAndProtection(t *testing.T) {
	secret := "not-a-real-firewall-secret"
	config, err := compileConfig(t, `
variable "rules" {
  type      = string
  sensitive = true
  default   = "chain input { ip saddr 192.0.2.9 counter accept comment \"not-a-real-firewall-secret\"; }"
}
host "node" {
  nftables {
    table "edge" {
      family           = "ip6"
      content          = "chain output { type filter hook output priority 0; policy accept; }"
      rollback_timeout = "1m"
      on_remove        = "delete"
      lifecycle { prevent_destroy = true }
    }
    table "edge" {
      content        = var.rules
      adopt_existing = true
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
	tables := program.Hosts[0].Nftables.Tables
	if len(tables) != 2 || tables[0].Family != "inet" || tables[1].Family != "ip6" {
		t.Fatalf("compiled table order = %#v", tables)
	}
	if !tables[0].AdoptExisting || tables[0].RollbackTimeoutSeconds != 30 || !tables[0].Sensitive || tables[0].ContentSHA256 == "" {
		t.Fatalf("inet defaults = %#v", tables[0])
	}
	if tables[1].OnRemove != "delete" || tables[1].RollbackTimeoutSeconds != 60 || !tables[1].Lifecycle.PreventDestroy {
		t.Fatalf("ip6 lifecycle = %#v", tables[1])
	}
	data, err := json.Marshal(program)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "type filter hook output") {
		t.Fatalf("IR JSON leaked nftables content: %s", data)
	}
}

func TestCompileNftablesRejectsUnsafeIdentityLifecycleAndContent(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "invalid family", body: "table \"edge\" {\n      family = \"ruleset\"\n      content = \"chain input {}\"\n    }", want: "must be one of"},
		{name: "unsafe name", body: `table "bad/name" { content = "chain input {}" }`, want: "table name"},
		{name: "missing content", body: `table "edge" {}`, want: "required non-empty string"},
		{name: "whole ruleset", body: `table "edge" { content = "flush ruleset" }`, want: "global or external ruleset mutation is forbidden"},
		{name: "external table", body: `table "edge" { content = "delete table inet external" }`, want: "global or external ruleset mutation is forbidden"},
		{name: "nested table", body: `table "edge" { content = "table inet external {}" }`, want: `unsupported "table" directive`},
		{name: "include", body: `table "edge" { content = "include \"/etc/nftables.conf\"" }`, want: `unsupported "include" directive`},
		{name: "wrapper escape", body: `table "edge" { content = "} flush ruleset" }`, want: "must not close"},
		{name: "unbalanced", body: `table "edge" { content = "chain input {" }`, want: "unbalanced braces"},
		{name: "short timeout", body: "table \"edge\" {\n      content = \"chain input {}\"\n      rollback_timeout = \"9s\"\n    }", want: "whole-second duration"},
		{name: "fraction timeout", body: "table \"edge\" {\n      content = \"chain input {}\"\n      rollback_timeout = \"10.5s\"\n    }", want: "whole-second duration"},
		{name: "long timeout", body: "table \"edge\" {\n      content = \"chain input {}\"\n      rollback_timeout = \"301s\"\n    }", want: "whole-second duration"},
		{name: "bad ensure", body: "table \"edge\" {\n      content = \"chain input {}\"\n      ensure = \"destroyed\"\n    }", want: `must be "present" or "absent"`},
		{name: "bad removal", body: "table \"edge\" {\n      content = \"chain input {}\"\n      on_remove = \"flush\"\n    }", want: `must be "forget" or "delete"`},
		{name: "adopt absent", body: "table \"edge\" {\n      ensure = \"absent\"\n      adopt_existing = true\n    }", want: "cannot be true"},
		{name: "content absent", body: "table \"edge\" {\n      ensure = \"absent\"\n      content = \"chain input {}\"\n    }", want: "must not be set"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, "host \"node\" {\n  nftables {\n    "+test.body+"\n  }\n}\n")
			if err == nil {
				_, err = Compile(config)
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("nftables validation error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCompileNftablesRejectsDuplicateResolvedIdentity(t *testing.T) {
	config, err := compileConfig(t, `
host "node" {
  nftables {
    table "edge" {
      family  = "inet"
      content = "chain input {}"
    }
    table "edge" {
      family  = "inet"
      content = "chain output {}"
    }
  }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(config)
	if err == nil || !strings.Contains(err.Error(), "duplicate nftables table inet") {
		t.Fatalf("duplicate identity error = %v", err)
	}
}

func TestCompileNftablesRequiresVersionForEphemeralContent(t *testing.T) {
	config, err := compileConfig(t, `
variable "rules" {
  type      = string
  ephemeral = true
  default   = "chain input { policy accept; }"
}
host "node" {
  nftables {
    table "edge" {
      content = var.rules
    }
  }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(config)
	if err == nil || !strings.Contains(err.Error(), "content_version") {
		t.Fatalf("ephemeral version error = %v", err)
	}
}

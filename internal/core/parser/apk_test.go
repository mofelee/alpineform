package parser

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAPKOwnershipRepositoriesAndKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.apf.hcl")
	writeConfig(t, path, `
host "node" {
  apk {
    ownership = "authoritative"
    repository "main" {
      url       = "https://dl-cdn.alpinelinux.org/alpine"
      component = "main"
    }
    key "vendor.rsa.pub" {
      source = "keys/vendor.rsa.pub"
      sha256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
      lifecycle { prevent_destroy = true }
    }
  }
}
`)
	config, err := ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	apk := config.Hosts["node"].APK
	if apk == nil || apk.Ownership != "authoritative" || len(apk.Repositories) != 1 || len(apk.Keys) != 1 || apk.Repositories[0].Label != "main" || !apk.Keys[0].Lifecycle.PreventDestroy {
		t.Fatalf("parsed APK model = %#v", apk)
	}
}

func TestParseAPKRejectsUnsafeShape(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "ownership", body: `ownership = "implicit"`, wantErr: `must be "managed" or "authoritative"`},
		{name: "attribute", body: `upgrade = true`, wantErr: "unsupported attribute"},
		{name: "child", body: `package "curl" {}`, wantErr: "unsupported block"},
		{name: "duplicate repository", body: `repository "main" { url = "https://example.test/alpine" }
    repository "main" { url = "https://example.test/alpine" }`, wantErr: "duplicate repository label"},
		{name: "repository attribute", body: `repository "main" { command = "echo unsafe" }`, wantErr: "unsupported attribute"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.apf.hcl")
			writeConfig(t, path, "host \"node\" {\n  apk {\n    "+test.body+"\n  }\n}\n")
			_, err := ParseFiles([]string{path})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ParseFiles() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

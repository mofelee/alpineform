package merge

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
)

func TestCompileAPKBuildsStructuredRepositoryAndVerifiedKey(t *testing.T) {
	dir := t.TempDir()
	keyContent := []byte("test-only-apk-public-key\n")
	if err := os.Mkdir(filepath.Join(dir, "keys"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keys", "vendor.rsa.pub"), keyContent, 0600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(keyContent))
	configPath := filepath.Join(dir, "main.apf.hcl")
	content := fmt.Sprintf(`
host "node" {
  platform { version = "3.24.1" }
  apk {
    repository "community" {
      url = "https://dl-cdn.alpinelinux.org/alpine/"
      tag = "stable"
    }
    key "vendor.rsa.pub" {
      source = "keys/vendor.rsa.pub"
      sha256 = %q
    }
  }
}
`, digest)
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := parser.ParseFiles([]string{configPath})
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	apk := program.Hosts[0].APK
	if apk == nil || apk.Ownership != "managed" || len(apk.Repositories) != 1 || len(apk.Keys) != 1 {
		t.Fatalf("compiled APK = %#v", apk)
	}
	repository := apk.Repositories[0]
	if repository.Branch != "3.24" || repository.Component != "community" || repository.Line != "@stable https://dl-cdn.alpinelinux.org/alpine/v3.24/community" {
		t.Fatalf("compiled repository = %#v", repository)
	}
	if apk.Keys[0].SHA256 != digest || string(apk.Keys[0].Content) != string(keyContent) {
		t.Fatalf("compiled key = %#v", apk.Keys[0])
	}
}

func TestCompileAPKRejectsUnsafeRepositoryAndKeyInputs(t *testing.T) {
	facts := map[string]ir.HostFacts{"node": {
		OSID: "alpine", Version: "3.24.1", Branch: "v3.24", Architecture: "amd64", NativeArchitecture: "x86_64", KernelArchitecture: "x86_64", Libc: "musl",
	}}
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "http URL", body: `repository "main" { url = "http://example.test/alpine" }`, wantErr: "must be an HTTPS base URL"},
		{name: "credentials", body: `repository "main" { url = "https://user:pass@example.test/alpine" }`, wantErr: "without credentials"},
		{name: "query", body: `repository "main" { url = "https://example.test/alpine?command=bad" }`, wantErr: "without credentials, query, or fragment"},
		{name: "branch", body: "repository \"main\" {\n      url = \"https://example.test/alpine\"\n      branch = \"edge\"\n    }", wantErr: "must match supported target branch"},
		{name: "tag", body: "repository \"main\" {\n      url = \"https://example.test/alpine\"\n      tag = \"bad tag\"\n    }", wantErr: "tag"},
		{name: "key traversal", body: `key "../vendor.rsa.pub" { ensure = "absent" }`, wantErr: "safe basename"},
		{name: "missing key source", body: `key "vendor.rsa.pub" { sha256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" }`, wantErr: "source"},
		{name: "missing key digest", body: `key "vendor.rsa.pub" { source = "vendor.rsa.pub" }`, wantErr: "sha256"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, "host \"node\" {\n  apk {\n    "+test.body+"\n  }\n}\n")
			if err != nil {
				if strings.Contains(err.Error(), test.wantErr) {
					return
				}
				t.Fatal(err)
			}
			_, err = CompileWithOptions(config, CompileOptions{HostFacts: facts})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("CompileWithOptions() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

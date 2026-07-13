package merge

import (
	"strings"
	"testing"
)

func TestCompilePackagesCreatesExplicitWorldIntentAndTag(t *testing.T) {
	config, err := compileConfig(t, `
host "node" {
  platform { version = "3.24.1" }
  apk {
    repository "vendor" {
      url = "https://packages.example.test/alpine"
      tag = "vendor"
    }
  }
  packages {
    package "curl" {}
    package "vendor-agent" { repository = "vendor" }
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
	packages := program.Hosts[0].Packages
	if len(packages) != 2 || packages[0].WorldIntent != "curl" || packages[0].Ensure != "present" || packages[1].WorldIntent != "vendor-agent@vendor" || packages[1].RepositoryTag != "vendor" {
		t.Fatalf("compiled packages = %#v", packages)
	}
}

func TestCompilePackagesRejectsPinsInjectionAndUnknownTags(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "version pin", body: `package "curl=8.0" {}`, wantErr: "unversioned APK package name"},
		{name: "shell characters", body: `package "curl;reboot" {}`, wantErr: "unversioned APK package name"},
		{name: "unknown tag", body: `package "curl" { repository = "testing" }`, wantErr: "unknown or absent APK repository tag"},
		{name: "invalid ensure", body: `package "curl" { ensure = "latest" }`, wantErr: `must be "present" or "absent"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, "host \"node\" {\n  platform { version = \"3.24.1\" }\n  apk {}\n  packages {\n    "+test.body+"\n  }\n}\n")
			if err != nil {
				t.Fatal(err)
			}
			_, err = Compile(config)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Compile() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestCompileAPKRejectsDuplicateRepositoryTags(t *testing.T) {
	config, err := compileConfig(t, `
host "node" {
  platform { version = "3.24.1" }
  apk {
    repository "first" {
      url = "https://one.example.test/alpine"
      tag = "vendor"
    }
    repository "second" {
      url = "https://two.example.test/alpine"
      tag = "vendor"
    }
  }
}
`)
	if err == nil {
		_, err = Compile(config)
	}
	if err == nil || !strings.Contains(err.Error(), "duplicates the tag") {
		t.Fatalf("duplicate repository tag error = %v", err)
	}
}

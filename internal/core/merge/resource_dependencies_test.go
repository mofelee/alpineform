package merge

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func TestResourceDependenciesResolveAfterProfileMerge(t *testing.T) {
	config, err := compileConfig(t, `
profile "package" {
  packages {
    package "bird" {}
  }
}
profile "configuration" {
  imports = [profile.package]
  files {
    file "/etc/bird.conf" {
      content    = "# managed\n"
      depends_on = [package.bird]
    }
  }
}
profile "service" {
  imports = [profile.configuration]
  services {
    service "bird" {
      depends_on = [file["/etc/bird.conf"]]
    }
  }
}
host "node" { imports = [profile.service] }
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	want := []ir.ResourceDependencySpec{
		{From: `host.node.files.file["/etc/bird.conf"]`, DependsOn: `host.node.packages.package["bird"]`},
		{From: `host.node.services.service["bird"]`, DependsOn: `host.node.files.file["/etc/bird.conf"]`},
	}
	got := program.Hosts[0].ExplicitDependencies
	for index := range got {
		got[index].Source = ir.SourceRef{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("explicit dependencies = %#v, want %#v", got, want)
	}
}

func TestResourceDependenciesUseDeterministicHostOverride(t *testing.T) {
	config, err := compileConfig(t, `
profile "base" {
  packages {
    package "base" {}
    package "host" {}
  }
  files {
    file "/etc/app.conf" {
      content    = "profile\n"
      depends_on = [package.base]
    }
  }
}
host "node" {
  imports = [profile.base]
  files {
    file "/etc/app.conf" {
      content    = "host\n"
      depends_on = [package.host]
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
	if len(host.Files) != 1 || host.Files[0].Content != "host\n" {
		t.Fatalf("host resource override = %#v", host.Files)
	}
	if len(host.ExplicitDependencies) != 1 || host.ExplicitDependencies[0].DependsOn != `host.node.packages.package["host"]` {
		t.Fatalf("override dependencies = %#v", host.ExplicitDependencies)
	}
}

func TestResourceDependenciesFollowProfileImportAndLocalPrecedence(t *testing.T) {
	config, err := compileConfig(t, `
profile "first" {
  packages {
    package "first" {}
    package "second" {}
    package "local" {}
  }
  files {
    file "/etc/app.conf" {
      content    = "first\n"
      depends_on = [package.first]
    }
  }
}
profile "second" {
  files {
    file "/etc/app.conf" {
      content    = "second\n"
      depends_on = [package.second]
    }
  }
}
profile "combined" {
  imports = [profile.first, profile.second]
  files {
    file "/etc/app.conf" {
      content    = "local\n"
      depends_on = [package.local]
    }
  }
}
host "imported" { imports = [profile.first, profile.second] }
host "local" { imports = [profile.combined] }
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range program.Hosts {
		wantPackage := host.Name
		if host.Name == "imported" {
			wantPackage = "second"
		}
		if len(host.ExplicitDependencies) != 1 || host.ExplicitDependencies[0].DependsOn != `host.`+host.Name+`.packages.package["`+wantPackage+`"]` {
			t.Fatalf("%s precedence dependencies = %#v", host.Name, host.ExplicitDependencies)
		}
		if len(host.Files) != 1 || host.Files[0].Content != wantPackage+"\n" {
			t.Fatalf("%s precedence file = %#v", host.Name, host.Files)
		}
	}
}

func TestResourceDependenciesResolveInsideMountedComponentScope(t *testing.T) {
	config, err := compileConfig(t, `
component "bird" {
  packages {
    package "bird" {}
  }
  files {
    file "/etc/bird.conf" {
      content    = "# managed\n"
      depends_on = [package.bird]
    }
  }
  services {
    service "bird" { depends_on = [file["/etc/bird.conf"]] }
  }
}
component "bird_backup" {
  packages {
    package "bird-backup" {}
  }
  files {
    file "/etc/bird-backup.conf" {
      content    = "# managed backup\n"
      depends_on = [package["bird-backup"]]
    }
  }
  services {
    service "bird-backup" { depends_on = [file["/etc/bird-backup.conf"]] }
  }
}
host "node" {
  component "routing" { source = component.bird }
  component "backup" { source = component.bird_backup }
}
host "edge" {
  component "routing" { source = component.bird }
}
`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	componentCount := 0
	for _, host := range program.Hosts {
		for _, component := range host.Components {
			componentCount++
			packageName := "bird"
			filePath := "/etc/bird.conf"
			serviceName := "bird"
			if component.Name == "backup" {
				packageName = "bird-backup"
				filePath = "/etc/bird-backup.conf"
				serviceName = "bird-backup"
			}
			got := component.ExplicitDependencies
			if len(got) != 2 {
				t.Fatalf("%s component %s dependencies = %#v", host.Name, component.Name, got)
			}
			prefix := "host." + host.Name + ".component." + component.Name
			fileAddress := prefix + `.files.file["` + filePath + `"]`
			if got[0].From != fileAddress || got[0].DependsOn != prefix+`.packages.package["`+packageName+`"]` {
				t.Fatalf("%s component %s file dependency = %#v", host.Name, component.Name, got[0])
			}
			if got[1].From != prefix+`.services.service["`+serviceName+`"]` || got[1].DependsOn != fileAddress {
				t.Fatalf("%s component %s service dependency = %#v", host.Name, component.Name, got[1])
			}
		}
	}
	if componentCount != 3 {
		t.Fatalf("compiled component count = %d, want 3", componentCount)
	}
}

func TestResourceDependenciesRejectUnknownAndCrossScopeTargets(t *testing.T) {
	tests := []struct {
		name string
		hcl  string
		want string
	}{
		{
			name: "host unknown",
			hcl: `
host "node" {
  files {
    file "/tmp/app" {
      content    = "ok"
      depends_on = [package.missing]
    }
  }
}`,
			want: "unknown or out-of-scope package.missing",
		},
		{
			name: "component cannot reference host package",
			hcl: `
component "app" {
  files {
    file "/tmp/app" {
      content    = "ok"
      depends_on = [package.host_only]
    }
  }
}
host "node" {
  packages {
    package "host_only" {}
  }
  component "app" { source = component.app }
}`,
			want: "unknown or out-of-scope package.host_only",
		},
		{
			name: "component cannot reference sibling component package",
			hcl: `
component "consumer" {
  files {
    file "/tmp/consumer" {
      content    = "ok"
      depends_on = [package.sibling_only]
    }
  }
}
component "producer" {
  packages {
    package "sibling_only" {}
  }
}
host "node" {
  component "consumer" { source = component.consumer }
  component "producer" { source = component.producer }
}`,
			want: "unknown or out-of-scope package.sibling_only",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := compileConfig(t, test.hcl)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Compile(config)
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), ".depends_on[0]") {
				t.Fatalf("Compile() error = %v, want %q with source path", err, test.want)
			}
		})
	}
}

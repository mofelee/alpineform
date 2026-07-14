package graph

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func TestCompileDockerOrdersConfigServiceAndCompose(t *testing.T) {
	secret := "not-a-real-compose-secret"
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name: "node", Source: source(1),
		Packages: []ir.PackageSpec{{Name: "docker", WorldIntent: "docker", Ensure: "present", Source: source(2)}, {Name: "docker-cli-compose", WorldIntent: "docker-cli-compose", Ensure: "present", Source: source(2)}},
		Groups:   []ir.ManagedGroupSpec{{Name: "docker", Ensure: "present", Source: source(2)}},
		Docker: &ir.DockerSpec{
			Ensure: "present", Enabled: true, PackageSource: "alpine", Members: []string{"deploy"}, Source: source(2),
			DaemonConfig: &ir.DockerDaemonConfigSpec{Content: "{}\n", ContentSHA256: strings.Repeat("a", 64), ContentBytes: 3, Source: source(3)},
			Projects:     []ir.DockerProjectSpec{{Name: "app", Directory: "/srv/app", Compose: secret, ComposeSHA256: strings.Repeat("b", 64), ComposeBytes: int64(len(secret)), State: "running", OnRemove: "destroy", Sensitive: true, Source: source(4)}},
		},
	}}}
	compiled, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	byAddress := map[string]Node{}
	for _, node := range compiled.Nodes {
		byAddress[node.Address] = node
	}
	config := byAddress["host.node.docker.daemon_config"]
	service := byAddress["host.node.docker.service"]
	project := byAddress[`host.node.docker.project["app"]`]
	if config.Kind != "docker_daemon_config" || !reflect.DeepEqual(service.TriggeredBy, []string{"host.node.docker.daemon_config"}) {
		t.Fatalf("config/service = %#v / %#v", config, service)
	}
	if !containsString(service.DependsOn, config.Address) || !containsString(project.DependsOn, service.Address) || !project.Sensitive || project.Payload["compose"] != secret {
		t.Fatalf("Docker graph = service %#v project %#v", service, project)
	}
	data, err := json.Marshal(compiled)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || !strings.Contains(string(data), `"protected":true`) {
		t.Fatalf("graph JSON = %s", data)
	}
}

func TestCompileDockerAbsentOrdersProjectsBeforeServiceBeforePackages(t *testing.T) {
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name: "node", Source: source(1),
		Packages: []ir.PackageSpec{{Name: "docker", WorldIntent: "docker", Ensure: "absent", Source: source(2)}, {Name: "docker-cli-compose", WorldIntent: "docker-cli-compose", Ensure: "absent", Source: source(2)}},
		Docker:   &ir.DockerSpec{Ensure: "absent", PackageSource: "alpine", Source: source(2), Projects: []ir.DockerProjectSpec{{Name: "app", Directory: "/srv/app", Compose: "services: {}", State: "absent", Source: source(3)}}},
	}}}
	compiled, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	ordered, err := compiled.Schedule()
	if err != nil {
		t.Fatal(err)
	}
	positions := map[string]int{}
	for index, node := range ordered {
		positions[node.Address] = index
	}
	project := positions[`host.node.docker.project["app"]`]
	service := positions["host.node.docker.service"]
	pkg := positions[`host.node.packages.package["docker"]`]
	config := positions["host.node.docker.daemon_config"]
	if !(project < service && service < pkg && service < config) {
		t.Fatalf("absent schedule = %#v", positions)
	}
}

func TestCompileDockerMembershipDependsOnComponentUser(t *testing.T) {
	program := &ir.Program{Hosts: []ir.HostSpec{{
		Name:   "node",
		Source: source(1),
		Groups: []ir.ManagedGroupSpec{{Name: "docker", Ensure: "present", Source: source(2)}},
		Docker: &ir.DockerSpec{Ensure: "present", PackageSource: "none", Members: []string{"deploy"}, Source: source(2)},
		Components: []ir.ComponentInstanceSpec{{
			Name: "agent", Source: source(3),
			Users: []ir.ManagedUserSpec{{Name: "deploy", Ensure: "present", Source: source(4)}},
		}},
	}}}
	compiled, err := Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range compiled.Nodes {
		if node.Address == `host.node.docker.membership["deploy"]` {
			want := `host.node.component.agent.users.user["deploy"]`
			if !containsString(node.DependsOn, want) {
				t.Fatalf("Docker membership dependencies = %#v, want %q", node.DependsOn, want)
			}
			return
		}
	}
	t.Fatal("Docker membership node not found")
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

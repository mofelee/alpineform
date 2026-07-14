package provider

import (
	"context"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

func testDockerDaemonNode(content string) graph.Node {
	return graph.Node{Host: "node", Kind: "docker_daemon_config", Managed: true, DigestSafe: true,
		Desired: map[string]any{
			"path": dockerDaemonConfigPath, "owner": "root", "group": "root", "mode": "0644",
			"ensure": "present", "content_sha256": sha256String(content),
			"content_bytes": int64(len(content)), "content_version": "", "content_write_only": false,
			"delete_behavior": "", "delete": map[string]any{"path": dockerDaemonConfigPath},
		},
		Payload: map[string]any{"content": content},
	}
}

func testDockerProjectNode(compose, env, state string, sensitive bool) graph.Node {
	directory := "/srv/app"
	return graph.Node{Host: "node", Kind: "docker_compose_project", Managed: true, Sensitive: sensitive, DigestSafe: true,
		Desired: map[string]any{
			"name": "app", "directory": directory, "compose_path": directory + "/compose.yaml", "env_path": directory + "/.env",
			"directory_owner": "root", "directory_group": "root", "directory_mode": "0755",
			"compose_owner": "root", "compose_group": "root", "compose_mode": "0600",
			"env_owner": "root", "env_group": "root", "env_mode": "0600",
			"has_env": true, "state": state, "compose_sha256": sha256String(compose), "compose_bytes": int64(len(compose)),
			"compose_version": "", "compose_write_only": false, "env_sha256": sha256String(env), "env_bytes": int64(len(env)),
			"env_version": "", "env_write_only": false, "content_write_only": false, "ensure": "present",
			"delete_behavior": "", "delete": map[string]any{"name": "app", "directory": directory, "compose_path": directory + "/compose.yaml", "env_path": directory + "/.env", "has_env": true},
		},
		Payload: map[string]any{"compose": compose, "env": env},
	}
}

func TestDockerDaemonProviderValidatesBeforeAtomicWriteAndReinspects(t *testing.T) {
	content := "{\n  \"log-driver\": \"json-file\"\n}\n"
	node := testDockerDaemonNode(content)
	runner := &commandRunner{outputs: map[string][]byte{
		"inspect.docker_daemon_config": []byte("file\nroot\nroot\n644\n" + strconv.Itoa(len(content)) + "\n" + sha256String(content) + "\n"),
	}}
	observed, err := applyDockerDaemonConfig(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 || runner.commands[0].Name != "apply.docker_daemon_config" || !runner.commands[0].RedactStdin || string(runner.commands[0].Stdin) != content {
		t.Fatalf("daemon commands = %#v", runner.commands)
	}
	if strings.Contains(runner.commands[0].Script, content) || !strings.Contains(runner.commands[0].Script, "dockerd --validate") || !strings.Contains(runner.commands[0].Script, "mv -f") {
		t.Fatalf("daemon apply script does not validate and replace safely")
	}
	if corestate.Digest(observed.Values) != corestate.Digest(node.Desired) {
		t.Fatalf("daemon observation = %#v", observed)
	}
}

func TestDockerComposeProviderPreflightsPayloadAndProtectsSecrets(t *testing.T) {
	compose := "services:\n  app:\n    image: alpine:3.24\n"
	env := "TOKEN=not-a-real-compose-secret\n"
	node := testDockerProjectNode(compose, env, "running", true)
	runner := &commandRunner{outputs: map[string][]byte{
		"inspect.docker_compose_project": dockerProjectInspectOutput("running", compose, env, true),
	}}
	observed, err := applyDockerComposeProject(context.Background(), runner, node)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 || runner.commands[0].Name != "apply.docker_compose_project" {
		t.Fatalf("Compose commands = %#v", runner.commands)
	}
	command := runner.commands[0]
	if !command.RedactStdin || !command.RedactOutput || string(command.Stdin) != compose+env || strings.Contains(command.Script, env) || strings.Contains(strings.Join(command.Arguments, " "), env) {
		t.Fatalf("protected Compose command = %#v", command)
	}
	if !strings.Contains(command.Script, "config --quiet") || strings.Index(command.Script, "config --quiet") > strings.Index(command.Script, "write_file") {
		t.Fatalf("Compose preflight is not before persistent writes")
	}
	if !strings.Contains(command.Script, `stopped) "$@" stop; "$@" create --remove-orphans`) {
		t.Fatalf("Compose stopped state does not create a converged stopped project")
	}
	if !observed.Protected || corestate.Digest(observed.Values) != corestate.Digest(node.Desired) {
		t.Fatalf("Compose observation = %#v", observed)
	}
}

func TestDockerComposeProviderClassifiesDriftAndExplicitDelete(t *testing.T) {
	compose := "services: {app: {image: alpine:3.24}}\n"
	env := ""
	node := testDockerProjectNode(compose, env, "running", false)
	node.Desired["has_env"] = false
	node.Desired["env_sha256"] = ""
	node.Desired["env_bytes"] = int64(0)
	node.Desired["env_owner"] = ""
	node.Desired["env_group"] = ""
	node.Desired["env_mode"] = ""
	node.Desired["delete"] = map[string]any{"name": "app", "directory": "/srv/app", "compose_path": "/srv/app/compose.yaml", "env_path": "/srv/app/.env", "has_env": false}
	for _, class := range []string{"running", "partial", "stopped", "absent", "degraded"} {
		t.Run(class, func(t *testing.T) {
			runner := &commandRunner{outputs: map[string][]byte{"inspect.docker_compose_project": dockerProjectInspectOutput(class, compose, env, false)}}
			observed, err := inspectDockerComposeProject(context.Background(), runner, node)
			if err != nil {
				t.Fatal(err)
			}
			matches := corestate.Digest(observed.Values) == corestate.Digest(node.Desired)
			if observed.Values["state"] != class || matches != (class == "running") {
				t.Fatalf("%s observation = %#v, desired match = %t", class, observed, matches)
			}
		})
	}

	absent := node
	absent.Desired = cloneDesired(node.Desired)
	absent.Desired["state"] = "absent"
	absent.Desired["ensure"] = "absent"
	deleteRunner := &commandRunner{outputs: map[string][]byte{}}
	if err := deleteDockerComposeProject(context.Background(), deleteRunner, engine.Step{Action: engine.ActionDelete, Node: absent}); err != nil {
		t.Fatal(err)
	}
	if len(deleteRunner.commands) != 1 || deleteRunner.commands[0].Arguments[len(deleteRunner.commands[0].Arguments)-1] != "absent" || !strings.Contains(deleteRunner.commands[0].Script, "config --quiet") || !strings.Contains(deleteRunner.commands[0].Script, "down --remove-orphans") {
		t.Fatalf("Compose delete command = %#v", deleteRunner.commands)
	}
}

func TestDockerProviderObservesOwnershipAndModeDrift(t *testing.T) {
	content := "{}\n"
	daemon := testDockerDaemonNode(content)
	daemonRunner := &commandRunner{outputs: map[string][]byte{
		"inspect.docker_daemon_config": []byte("file\nroot\nroot\n600\n" + strconv.Itoa(len(content)) + "\n" + sha256String(content) + "\n"),
	}}
	observed, err := inspectDockerDaemonConfig(context.Background(), daemonRunner, daemon)
	if err != nil {
		t.Fatal(err)
	}
	if observed.Values["mode"] != "0600" || corestate.Digest(observed.Values) == corestate.Digest(daemon.Desired) {
		t.Fatalf("daemon metadata drift = %#v", observed)
	}

	compose := "services: {app: {image: alpine:3.24}}\n"
	project := testDockerProjectNode(compose, "", "running", false)
	output := strings.Replace(string(dockerProjectInspectOutput("running", compose, "", true)), "\nroot\nroot\n755\n", "\nroot\nroot\n700\n", 1)
	projectRunner := &commandRunner{outputs: map[string][]byte{"inspect.docker_compose_project": []byte(output)}}
	observed, err = inspectDockerComposeProject(context.Background(), projectRunner, project)
	if err != nil {
		t.Fatal(err)
	}
	if observed.Values["directory_mode"] != "0700" || corestate.Digest(observed.Values) == corestate.Digest(project.Desired) {
		t.Fatalf("Compose metadata drift = %#v", observed)
	}
}

func TestDockerOrphanDestroyUsesOnlyRecordedProjectIdentity(t *testing.T) {
	prior := &corestate.Resource{Kind: "docker_compose_project", DeleteBehavior: engine.ActionDestroy, Delete: map[string]any{
		"name": "app", "directory": "/srv/app", "compose_path": "/srv/app/compose.yaml", "env_path": "/srv/app/.env", "has_env": true,
	}}
	runner := &commandRunner{outputs: map[string][]byte{}}
	if err := deleteDockerComposeProject(context.Background(), runner, engine.Step{Action: engine.ActionDestroy, Prior: prior}); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 || !reflect.DeepEqual(runner.commands[0].Arguments, []string{"app", "/srv/app", "/srv/app/compose.yaml", "/srv/app/.env", "true"}) || !strings.Contains(runner.commands[0].Script, "com.docker.compose.project=$name") {
		t.Fatalf("orphan destroy command = %#v", runner.commands)
	}
	if strings.Contains(runner.commands[0].Script, "volume rm") || strings.Contains(runner.commands[0].Script, "image rm") || strings.Contains(runner.commands[0].Script, "--volumes") {
		t.Fatalf("orphan destroy may remove volumes or images: %s", runner.commands[0].Script)
	}
}

func TestDockerProviderScriptsUseValidShellAndFixedArguments(t *testing.T) {
	scripts := map[string]string{
		"daemon inspect":         dockerDaemonConfigInspectScript,
		"daemon apply":           dockerDaemonConfigApplyScript,
		"daemon delete":          dockerDaemonConfigDeleteScript,
		"service delete":         dockerServiceDeleteScript,
		"compose inspect":        dockerComposeInspectScript,
		"compose apply":          dockerComposeApplyScript,
		"compose orphan destroy": dockerComposeOrphanDestroyScript,
	}
	for name, script := range scripts {
		t.Run(name, func(t *testing.T) {
			command := exec.Command("sh", "-n")
			command.Stdin = strings.NewReader(script)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("shell syntax error: %v: %s", err, output)
			}
		})
	}

	step := engine.Step{Node: graph.Node{Desired: map[string]any{
		"name": "docker", "runlevel": "default", "ensure": "absent", "delete": map[string]any{"name": "docker", "runlevel": "default"},
	}}}
	runner := &commandRunner{outputs: map[string][]byte{}}
	if err := deleteDockerService(context.Background(), runner, step); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 || strings.Contains(runner.commands[0].Script, "default") || strings.Contains(runner.commands[0].Script, "docker\n") {
		t.Fatalf("Docker service deletion command = %#v", runner.commands)
	}
	var _ backend.Runner = runner
}

func dockerProjectInspectOutput(class, compose, env string, hasEnv bool) []byte {
	envDigest := "-"
	envMetadata := "-\n-\n-"
	if hasEnv {
		envDigest = sha256String(env)
		envMetadata = "root\nroot\n600"
	}
	return []byte("project\n" + class + "\n" + sha256String(compose) + "\n" + envDigest +
		"\nroot\nroot\n755\nroot\nroot\n600\n" + envMetadata + "\n")
}

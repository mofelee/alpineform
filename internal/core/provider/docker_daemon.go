package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
)

const dockerDaemonConfigPath = "/etc/docker/daemon.json"

const dockerDaemonConfigInspectScript = `set -eu
path=$1
if [ -L "$path" ]; then
  echo 'refusing symbolic-link Docker daemon configuration' >&2
  exit 1
fi
if [ ! -e "$path" ]; then
  echo missing
  exit 0
fi
if [ ! -f "$path" ]; then
  echo 'Docker daemon configuration is not a regular file' >&2
  exit 1
fi
echo file
stat -c '%U' "$path"
stat -c '%G' "$path"
stat -c '%a' "$path"
stat -c '%s' "$path"
sha256sum "$path" | awk '{print $1}'
`

const dockerDaemonConfigApplyScript = `set -eu
path=$1
parent=${path%/*}
if [ -L "$path" ]; then
  echo 'refusing symbolic-link Docker daemon configuration' >&2
  exit 1
fi
if [ -e "$path" ] && [ ! -f "$path" ]; then
  echo 'Docker daemon configuration is not a regular file' >&2
  exit 1
fi
mkdir -p "$parent"
tmp=$(mktemp "$parent/.alpineform-docker-daemon.XXXXXX")
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
cat >"$tmp"
if ! command -v dockerd >/dev/null 2>&1; then
  echo 'Docker Engine is not installed' >&2
  exit 1
fi
dockerd --validate --config-file "$tmp" >/dev/null
chmod 0644 "$tmp"
chown 0:0 "$tmp"
mv -f "$tmp" "$path"
trap - EXIT HUP INT TERM
`

const dockerDaemonConfigDeleteScript = `set -eu
path=$1
if [ -L "$path" ]; then
  echo 'refusing symbolic-link Docker daemon configuration' >&2
  exit 1
fi
if [ -e "$path" ] && [ ! -f "$path" ]; then
  echo 'Docker daemon configuration is not a regular file' >&2
  exit 1
fi
rm -f "$path"
`

const dockerServiceDeleteScript = `set -eu
name=$1
runlevel=$2
init=/etc/init.d/$name
if [ ! -e "$init" ]; then
  exit 0
fi
if [ -L "$init" ] || [ ! -f "$init" ] || [ ! -x "$init" ]; then
  echo 'Docker OpenRC service is not a regular executable init file' >&2
  exit 1
fi
set +e
status=$(rc-service "$name" status 2>&1)
set -e
case "$status" in
  *"status: started"*|*"status: crashed"*) rc-service "$name" stop >/dev/null ;;
esac
if [ -e "/etc/runlevels/$runlevel/$name" ]; then
  rc-update del "$name" "$runlevel" >/dev/null
fi
`

func inspectDockerDaemonConfig(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	if stringValue(node.Desired, "path") != dockerDaemonConfigPath {
		return engine.ObservedResource{}, fmt.Errorf("Docker daemon configuration has an invalid path")
	}
	output, err := runner.Run(ctx, backend.Command{
		Name: "inspect.docker_daemon_config", Script: dockerDaemonConfigInspectScript,
		Arguments: []string{dockerDaemonConfigPath}, RedactOutput: node.Sensitive || node.Ephemeral,
	})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "missing" || lines[0] == "" {
		return engine.ObservedResource{}, nil
	}
	if len(lines) != 6 || lines[0] != "file" || !providerSHA256Pattern.MatchString(lines[5]) {
		return engine.ObservedResource{}, fmt.Errorf("inspect Docker daemon configuration returned an invalid response")
	}
	size, err := strconv.ParseInt(lines[4], 10, 64)
	if err != nil || size < 0 {
		return engine.ObservedResource{}, fmt.Errorf("inspect Docker daemon configuration returned an invalid size")
	}
	observed := cloneDesired(node.Desired)
	observed["owner"] = lines[1]
	observed["group"] = lines[2]
	observed["mode"] = normalizedObservedMode(lines[3])
	if !boolValue(node.Desired, "content_write_only") {
		observed["content_bytes"] = size
		observed["content_sha256"] = strings.ToLower(lines[5])
	}
	return engine.ObservedResource{Exists: true, Values: observed, Protected: node.Sensitive || node.Ephemeral}, nil
}

func applyDockerDaemonConfig(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	if stringValue(node.Desired, "ensure") != "present" || stringValue(node.Desired, "path") != dockerDaemonConfigPath {
		return engine.ObservedResource{}, fmt.Errorf("Docker daemon configuration apply requires a present canonical path")
	}
	content, ok := node.Payload["content"].(string)
	if !ok {
		return engine.ObservedResource{}, fmt.Errorf("Docker daemon configuration has no content payload")
	}
	if _, err := runner.Run(ctx, backend.Command{
		Name: "apply.docker_daemon_config", Script: dockerDaemonConfigApplyScript,
		Arguments: []string{dockerDaemonConfigPath}, Stdin: []byte(content), RedactStdin: true,
		RedactOutput: node.Sensitive || node.Ephemeral,
	}); err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectDockerDaemonConfig(ctx, runner, node)
}

func deleteDockerDaemonConfig(ctx context.Context, runner backend.Runner, step engine.Step) error {
	path := dockerDaemonConfigPath
	if step.Node.Desired != nil && stringValue(step.Node.Desired, "path") != path {
		return fmt.Errorf("Docker daemon configuration has an invalid deletion path")
	}
	_, err := runner.Run(ctx, backend.Command{
		Name: "delete.docker_daemon_config", Script: dockerDaemonConfigDeleteScript,
		Arguments: []string{path}, RedactOutput: stepIsProtected(step),
	})
	return err
}

func applyDockerService(ctx context.Context, runner backend.Runner, step engine.Step) (engine.ObservedResource, error) {
	if stringValue(step.Node.Desired, "ensure") != "present" {
		return engine.ObservedResource{}, fmt.Errorf("Docker OpenRC service apply requires ensure = \"present\"")
	}
	return applyService(ctx, runner, step)
}

func deleteDockerService(ctx context.Context, runner backend.Runner, step engine.Step) error {
	name, runlevel := deletionIdentity(step, "name", "runlevel")
	if name == "" && step.Node.Desired != nil {
		name = stringValue(step.Node.Desired, "name")
		runlevel = stringValue(step.Node.Desired, "runlevel")
	}
	if name != "docker" || !providerOpenRCNamePattern.MatchString(runlevel) {
		return fmt.Errorf("invalid Docker OpenRC service deletion identity")
	}
	_, err := runner.Run(ctx, backend.Command{Name: "delete.docker_service", Script: dockerServiceDeleteScript, Arguments: []string{name, runlevel}})
	return err
}

package provider

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
)

var providerDockerProjectNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

const dockerComposeInspectScript = `set -eu
name=$1
directory=$2
compose_path=$3
env_path=$4
has_env=$5

config_exists=false
if [ -L "$directory" ]; then
  echo 'refusing symbolic-link Docker project directory' >&2
  exit 1
fi
if [ -e "$directory" ] && [ ! -d "$directory" ]; then
  echo 'Docker project directory is not a directory' >&2
  exit 1
fi
if [ -L "$compose_path" ] || { [ -e "$compose_path" ] && [ ! -f "$compose_path" ]; }; then
  echo 'Docker Compose path is not a regular file' >&2
  exit 1
fi
if [ -f "$compose_path" ]; then config_exists=true; fi
if [ "$has_env" = true ] && { [ -L "$env_path" ] || { [ -e "$env_path" ] && [ ! -f "$env_path" ]; }; }; then
  echo 'Docker Compose env path is not a regular file' >&2
  exit 1
fi

containers=$(docker ps -a --filter "label=com.docker.compose.project=$name" --format '{{.ID}}' 2>/dev/null || true)
container_count=$(printf '%s\n' "$containers" | awk 'NF { count++ } END { print count+0 }')
if [ "$config_exists" = false ] && [ "$container_count" -eq 0 ]; then
  echo missing
  exit 0
fi

class=degraded
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 && [ "$config_exists" = true ]; then
  set -- docker compose --project-name "$name" --project-directory "$directory" --file "$compose_path"
  if [ "$has_env" = true ] && [ -f "$env_path" ]; then set -- "$@" --env-file "$env_path"; fi
  if "$@" config --quiet >/dev/null 2>&1; then
    services=$("$@" config --services 2>/dev/null || true)
    expected=$(printf '%s\n' "$services" | awk 'NF { count++ } END { print count+0 }')
    if [ "$expected" -gt 0 ]; then
      running=0
      stopped=0
      missing=0
      bad=0
      for service in $services; do
        states=$(docker ps -a \
          --filter "label=com.docker.compose.project=$name" \
          --filter "label=com.docker.compose.service=$service" \
          --format '{{.State}}')
        if [ -z "$states" ]; then
          missing=$((missing + 1))
          continue
        fi
        service_running=false
        service_stopped=false
        for state in $states; do
          case "$state" in
            running) service_running=true ;;
            exited|created) service_stopped=true ;;
            *) bad=$((bad + 1)) ;;
          esac
        done
        if [ "$service_running" = true ]; then running=$((running + 1)); fi
        if [ "$service_stopped" = true ]; then stopped=$((stopped + 1)); fi
      done
      unexpected=0
      for service in $(docker ps -a --filter "label=com.docker.compose.project=$name" --format '{{.Label "com.docker.compose.service"}}'); do
        if ! printf '%s\n' "$services" | grep -Fqx "$service"; then unexpected=$((unexpected + 1)); fi
      done
      if [ "$bad" -gt 0 ] || [ "$unexpected" -gt 0 ]; then
        class=degraded
      elif [ "$container_count" -eq 0 ]; then
        class=absent
      elif [ "$running" -eq "$expected" ] && [ "$stopped" -eq 0 ] && [ "$missing" -eq 0 ]; then
        class=running
      elif [ "$running" -eq 0 ] && [ "$stopped" -eq "$expected" ] && [ "$missing" -eq 0 ]; then
        class=stopped
      else
        class=partial
      fi
    fi
  fi
fi

echo project
echo "$class"
if [ -f "$compose_path" ]; then sha256sum "$compose_path" | awk '{print $1}'; else echo -; fi
if [ "$has_env" = true ] && [ -f "$env_path" ]; then sha256sum "$env_path" | awk '{print $1}'; else echo -; fi
metadata() {
  path=$1
  kind=$2
  if { [ "$kind" = directory ] && [ -d "$path" ] && [ ! -L "$path" ]; } ||
     { [ "$kind" = file ] && [ -f "$path" ] && [ ! -L "$path" ]; }; then
    stat -c '%U' "$path"
    stat -c '%G' "$path"
    stat -c '%a' "$path"
  else
    echo -
    echo -
    echo -
  fi
}
metadata "$directory" directory
metadata "$compose_path" file
if [ "$has_env" = true ]; then metadata "$env_path" file; else echo -; echo -; echo -; fi
`

const dockerComposeApplyScript = `set -eu
name=$1
directory=$2
compose_path=$3
env_path=$4
has_env=$5
compose_bytes=$6
env_bytes=$7
action=$8

if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
  echo 'Docker Engine and Compose plugin are required' >&2
  exit 1
fi
if [ -L "$directory" ]; then
  echo 'refusing symbolic-link Docker project directory' >&2
  exit 1
fi
if [ -e "$directory" ] && [ ! -d "$directory" ]; then
  echo 'Docker project directory is not a directory' >&2
  exit 1
fi
mkdir -p "$directory" /var/lib/alpineform/tmp
chmod 0755 "$directory"
chown 0:0 "$directory"
chmod 0700 /var/lib/alpineform/tmp
tmp=$(mktemp -d /var/lib/alpineform/tmp/docker-compose.XXXXXX)
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT HUP INT TERM
dd bs=1 count="$compose_bytes" of="$tmp/compose.yaml" 2>/dev/null
if [ "$(wc -c <"$tmp/compose.yaml" | tr -d ' ')" != "$compose_bytes" ]; then
  echo 'Docker Compose payload is truncated' >&2
  exit 1
fi
if [ "$has_env" = true ]; then
  dd bs=1 count="$env_bytes" of="$tmp/.env" 2>/dev/null
  if [ "$(wc -c <"$tmp/.env" | tr -d ' ')" != "$env_bytes" ]; then
    echo 'Docker Compose env payload is truncated' >&2
    exit 1
  fi
fi
set -- docker compose --project-name "$name" --project-directory "$directory" --file "$tmp/compose.yaml"
if [ "$has_env" = true ]; then set -- "$@" --env-file "$tmp/.env"; fi
"$@" config --quiet >/dev/null

if [ "$action" = absent ]; then
  "$@" down --remove-orphans
  rm -f "$compose_path" "$env_path"
  exit 0
fi

write_file() {
  source=$1
  target=$2
  if [ -L "$target" ] || { [ -e "$target" ] && [ ! -f "$target" ]; }; then
    echo 'refusing non-regular Docker Compose target' >&2
    exit 1
  fi
  staged=$(mktemp "$directory/.alpineform-docker-compose.XXXXXX")
  cp "$source" "$staged"
  chmod 0600 "$staged"
  chown 0:0 "$staged"
  mv -f "$staged" "$target"
}
write_file "$tmp/compose.yaml" "$compose_path"
if [ "$has_env" = true ]; then write_file "$tmp/.env" "$env_path"; else rm -f "$env_path"; fi

set -- docker compose --project-name "$name" --project-directory "$directory" --file "$compose_path"
if [ "$has_env" = true ]; then set -- "$@" --env-file "$env_path"; fi
case "$action" in
  running) "$@" up --detach --remove-orphans ;;
  stopped) "$@" stop; "$@" create --remove-orphans ;;
  *) echo 'unsupported Docker Compose desired state' >&2; exit 1 ;;
esac
`

const dockerComposeOrphanDestroyScript = `set -eu
name=$1
directory=$2
compose_path=$3
env_path=$4
has_env=$5
if ! command -v docker >/dev/null 2>&1; then
  echo 'Docker CLI is required to destroy a Compose project' >&2
  exit 1
fi
used_compose=false
if [ -f "$compose_path" ] && [ ! -L "$compose_path" ] && docker compose version >/dev/null 2>&1; then
  set -- docker compose --project-name "$name" --project-directory "$directory" --file "$compose_path"
  if [ "$has_env" = true ] && [ -f "$env_path" ] && [ ! -L "$env_path" ]; then set -- "$@" --env-file "$env_path"; fi
  if "$@" config --quiet >/dev/null 2>&1; then
    "$@" down --remove-orphans
    used_compose=true
  fi
fi
if [ "$used_compose" = false ]; then
  ids=$(docker ps -aq --filter "label=com.docker.compose.project=$name")
  if [ -n "$ids" ]; then docker rm -f $ids >/dev/null; fi
  networks=$(docker network ls -q --filter "label=com.docker.compose.project=$name")
  if [ -n "$networks" ]; then docker network rm $networks >/dev/null; fi
fi
rm -f "$compose_path" "$env_path"
`

func inspectDockerComposeProject(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	name, directory, composePath, envPath, hasEnv, err := dockerProjectIdentity(node.Desired)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{
		Name: "inspect.docker_compose_project", Script: dockerComposeInspectScript,
		Arguments:    []string{name, directory, composePath, envPath, strconv.FormatBool(hasEnv)},
		RedactOutput: node.Sensitive || node.Ephemeral,
	})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "missing" || lines[0] == "" {
		return engine.ObservedResource{}, nil
	}
	if len(lines) != 13 || lines[0] != "project" || !validDockerProjectClass(lines[1]) {
		return engine.ObservedResource{}, fmt.Errorf("inspect Docker Compose project %q returned an invalid response", name)
	}
	observed := cloneDesired(node.Desired)
	observed["state"] = lines[1]
	if !boolValue(node.Desired, "compose_write_only") {
		observed["compose_sha256"] = dockerObservedDigest(lines[2])
	}
	if hasEnv && !boolValue(node.Desired, "env_write_only") {
		observed["env_sha256"] = dockerObservedDigest(lines[3])
	}
	observed["directory_owner"] = dockerMetadataValue(lines[4])
	observed["directory_group"] = dockerMetadataValue(lines[5])
	observed["directory_mode"] = normalizedObservedMode(dockerMetadataValue(lines[6]))
	observed["compose_owner"] = dockerMetadataValue(lines[7])
	observed["compose_group"] = dockerMetadataValue(lines[8])
	observed["compose_mode"] = normalizedObservedMode(dockerMetadataValue(lines[9]))
	observed["env_owner"] = dockerMetadataValue(lines[10])
	observed["env_group"] = dockerMetadataValue(lines[11])
	observed["env_mode"] = normalizedObservedMode(dockerMetadataValue(lines[12]))
	return engine.ObservedResource{Exists: true, Values: observed, Protected: node.Sensitive || node.Ephemeral}, nil
}

func applyDockerComposeProject(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	if stringValue(node.Desired, "ensure") != "present" {
		return engine.ObservedResource{}, fmt.Errorf("Docker Compose apply requires a present project")
	}
	name, directory, composePath, envPath, hasEnv, err := dockerProjectIdentity(node.Desired)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	compose, composeOK := node.Payload["compose"].(string)
	env, envOK := node.Payload["env"].(string)
	if !composeOK || !envOK {
		return engine.ObservedResource{}, fmt.Errorf("Docker Compose project %q has invalid content payloads", name)
	}
	action := stringValue(node.Desired, "state")
	if action != "running" && action != "stopped" {
		return engine.ObservedResource{}, fmt.Errorf("Docker Compose project %q has unsupported desired state", name)
	}
	stdin := append([]byte(compose), []byte(env)...)
	_, err = runner.Run(ctx, backend.Command{
		Name: "apply.docker_compose_project", Script: dockerComposeApplyScript,
		Arguments: []string{name, directory, composePath, envPath, strconv.FormatBool(hasEnv), strconv.Itoa(len(compose)), strconv.Itoa(len(env)), action},
		Stdin:     stdin, RedactStdin: true, RedactOutput: node.Sensitive || node.Ephemeral,
	})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectDockerComposeProject(ctx, runner, node)
}

func deleteDockerComposeProject(ctx context.Context, runner backend.Runner, step engine.Step) error {
	values := step.Node.Desired
	if values == nil && step.Prior != nil {
		values = step.Prior.Delete
	}
	name, directory, composePath, envPath, hasEnv, err := dockerProjectIdentity(values)
	if err != nil {
		return err
	}
	if step.Node.Desired != nil && stringValue(step.Node.Desired, "ensure") == "absent" {
		compose, composeOK := step.Node.Payload["compose"].(string)
		env, envOK := step.Node.Payload["env"].(string)
		if !composeOK || !envOK {
			return fmt.Errorf("Docker Compose project %q has invalid deletion payloads", name)
		}
		stdin := append([]byte(compose), []byte(env)...)
		_, err = runner.Run(ctx, backend.Command{
			Name: "delete.docker_compose_project", Script: dockerComposeApplyScript,
			Arguments: []string{name, directory, composePath, envPath, strconv.FormatBool(hasEnv), strconv.Itoa(len(compose)), strconv.Itoa(len(env)), "absent"},
			Stdin:     stdin, RedactStdin: true, RedactOutput: stepIsProtected(step),
		})
		return err
	}
	_, err = runner.Run(ctx, backend.Command{
		Name: "destroy.docker_compose_project", Script: dockerComposeOrphanDestroyScript,
		Arguments: []string{name, directory, composePath, envPath, strconv.FormatBool(hasEnv)}, RedactOutput: stepIsProtected(step),
	})
	return err
}

func dockerProjectIdentity(values map[string]any) (string, string, string, string, bool, error) {
	name := stringValue(values, "name")
	directory := stringValue(values, "directory")
	composePath := stringValue(values, "compose_path")
	envPath := stringValue(values, "env_path")
	hasEnv := boolValue(values, "has_env")
	if !providerDockerProjectNamePattern.MatchString(name) {
		return "", "", "", "", false, fmt.Errorf("invalid Docker Compose project identity %q", name)
	}
	if err := validateRemoteFilePath(directory); err != nil {
		return "", "", "", "", false, fmt.Errorf("Docker Compose project directory: %w", err)
	}
	if composePath != filepath.Join(directory, "compose.yaml") || envPath != filepath.Join(directory, ".env") {
		return "", "", "", "", false, fmt.Errorf("Docker Compose project %q has invalid managed paths", name)
	}
	return name, directory, composePath, envPath, hasEnv, nil
}

func validDockerProjectClass(value string) bool {
	return value == "running" || value == "partial" || value == "stopped" || value == "absent" || value == "degraded"
}

func dockerObservedDigest(value string) string {
	if providerSHA256Pattern.MatchString(value) {
		return strings.ToLower(value)
	}
	return ""
}

func dockerMetadataValue(value string) string {
	if value == "-" {
		return ""
	}
	return value
}

func normalizedObservedMode(value string) string {
	if len(value) == 3 {
		return "0" + value
	}
	return value
}

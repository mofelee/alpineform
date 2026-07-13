package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
)

const apkRepositoriesPath = "/etc/apk/repositories"

var providerAPKRepositoryNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

const apkManagedRepositoryInspectScript = `set -eu
path=$1
begin=$2
end=$3
if [ -L "$path" ]; then
  echo 'refusing symbolic-link APK repositories file' >&2
  exit 1
fi
if [ ! -e "$path" ]; then
  echo missing
  exit 0
fi
if [ ! -f "$path" ]; then
  echo 'APK repositories path is not a regular file' >&2
  exit 1
fi
awk -v begin="$begin" -v end="$end" '
BEGIN { found=0; inside=0; count=0; bad=0 }
$0 == begin { if (inside || found) bad=1; inside=1; found=1; next }
$0 == end { if (!inside) bad=1; inside=0; next }
inside { count++; value=$0; next }
END {
  if (inside || bad || (found && count != 1)) exit 42
  if (!found) print "missing"
  else { print "repository"; print value }
}' "$path"
`

const apkAuthoritativeRepositoryInspectScript = `set -eu
path=$1
if [ -L "$path" ]; then
  echo 'refusing symbolic-link APK repositories file' >&2
  exit 1
fi
if [ ! -e "$path" ]; then
  echo missing
  exit 0
fi
if [ ! -f "$path" ]; then
  echo 'APK repositories path is not a regular file' >&2
  exit 1
fi
printf 'file\n'
cat "$path"
`

const apkManagedRepositoryWriteScript = `set -eu
path=$1
begin=$2
end=$3
line=$4
parent=${path%/*}
if [ -L "$path" ]; then
  echo 'refusing symbolic-link APK repositories file' >&2
  exit 1
fi
if [ -e "$path" ] && [ ! -f "$path" ]; then
  echo 'APK repositories path is not a regular file' >&2
  exit 1
fi
mkdir -p "$parent"
tmp=$(mktemp "$parent/.alpineform-repositories.XXXXXX")
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
if [ -f "$path" ]; then
  awk -v begin="$begin" -v end="$end" '
  BEGIN { found=0; inside=0; bad=0 }
  $0 == begin { if (inside || found) bad=1; inside=1; found=1; next }
  $0 == end { if (!inside) bad=1; inside=0; next }
  !inside { print }
  END { if (inside || bad) exit 42 }
  ' "$path" >"$tmp"
fi
printf '%s\n%s\n%s\n' "$begin" "$line" "$end" >>"$tmp"
chmod 0644 "$tmp"
if [ "$(id -u)" -eq 0 ]; then chown 0:0 "$tmp"; fi
mv -f "$tmp" "$path"
trap - EXIT HUP INT TERM
`

const apkAuthoritativeRepositoryWriteScript = `set -eu
path=$1
parent=${path%/*}
if [ -L "$path" ]; then
  echo 'refusing symbolic-link APK repositories file' >&2
  exit 1
fi
if [ -e "$path" ] && [ ! -f "$path" ]; then
  echo 'APK repositories path is not a regular file' >&2
  exit 1
fi
mkdir -p "$parent"
tmp=$(mktemp "$parent/.alpineform-repositories.XXXXXX")
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
cat >"$tmp"
chmod 0644 "$tmp"
if [ "$(id -u)" -eq 0 ]; then chown 0:0 "$tmp"; fi
mv -f "$tmp" "$path"
trap - EXIT HUP INT TERM
`

const apkManagedRepositoryDeleteScript = `set -eu
path=$1
begin=$2
end=$3
if [ -L "$path" ]; then
  echo 'refusing symbolic-link APK repositories file' >&2
  exit 1
fi
if [ ! -e "$path" ]; then
  exit 0
fi
if [ ! -f "$path" ]; then
  echo 'APK repositories path is not a regular file' >&2
  exit 1
fi
parent=${path%/*}
tmp=$(mktemp "$parent/.alpineform-repositories.XXXXXX")
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
awk -v begin="$begin" -v end="$end" '
BEGIN { found=0; inside=0; bad=0 }
$0 == begin { if (inside || found) bad=1; inside=1; found=1; next }
$0 == end { if (!inside) bad=1; inside=0; next }
!inside { print }
END { if (inside || bad) exit 42 }
' "$path" >"$tmp"
chmod 0644 "$tmp"
if [ "$(id -u)" -eq 0 ]; then chown 0:0 "$tmp"; fi
mv -f "$tmp" "$path"
trap - EXIT HUP INT TERM
`

func inspectAPKRepository(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	ownership := stringValue(node.Desired, "ownership")
	if ownership == "authoritative" {
		output, err := runner.Run(ctx, backend.Command{Name: "inspect.apk_repositories", Script: apkAuthoritativeRepositoryInspectScript, Arguments: []string{apkRepositoriesPath}})
		if err != nil {
			return engine.ObservedResource{}, err
		}
		if string(output) == "missing\n" || len(output) == 0 {
			return engine.ObservedResource{}, nil
		}
		const header = "file\n"
		if !strings.HasPrefix(string(output), header) {
			return engine.ObservedResource{}, fmt.Errorf("inspect authoritative APK repositories returned an invalid response")
		}
		content := output[len(header):]
		observed := cloneDesired(node.Desired)
		observed["lines"] = splitRepositoryLines(content)
		observed["final_newline"] = len(content) > 0 && content[len(content)-1] == '\n'
		return engine.ObservedResource{Exists: true, Values: observed}, nil
	}
	name, err := desiredAPKRepositoryName(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	begin, end := apkRepositoryMarkers(name)
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.apk_repository", Script: apkManagedRepositoryInspectScript, Arguments: []string{apkRepositoriesPath, begin, end}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "missing" || lines[0] == "" {
		return engine.ObservedResource{}, nil
	}
	if len(lines) != 2 || lines[0] != "repository" {
		return engine.ObservedResource{}, fmt.Errorf("inspect managed APK repository %q returned an invalid response", name)
	}
	observed := cloneDesired(node.Desired)
	observed["line"] = lines[1]
	return engine.ObservedResource{Exists: true, Values: observed}, nil
}

func applyAPKRepository(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	ownership := stringValue(node.Desired, "ownership")
	if ownership == "authoritative" {
		lines, ok := node.Desired["lines"].([]string)
		if !ok {
			return engine.ObservedResource{}, fmt.Errorf("authoritative APK repositories have invalid line metadata")
		}
		content := strings.Join(lines, "\n")
		if boolValue(node.Desired, "final_newline") {
			content += "\n"
		}
		if _, err := runner.Run(ctx, backend.Command{Name: "apply.apk_repositories", Script: apkAuthoritativeRepositoryWriteScript, Arguments: []string{apkRepositoriesPath}, Stdin: []byte(content)}); err != nil {
			return engine.ObservedResource{}, err
		}
		return inspectAPKRepository(ctx, runner, node)
	}
	name, err := desiredAPKRepositoryName(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	line := stringValue(node.Desired, "line")
	if strings.TrimSpace(line) != line || line == "" || strings.ContainsAny(line, "\x00\r\n") {
		return engine.ObservedResource{}, fmt.Errorf("managed APK repository %q has an invalid line", name)
	}
	begin, end := apkRepositoryMarkers(name)
	if _, err := runner.Run(ctx, backend.Command{Name: "apply.apk_repository", Script: apkManagedRepositoryWriteScript, Arguments: []string{apkRepositoriesPath, begin, end, line}}); err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectAPKRepository(ctx, runner, node)
}

func deleteAPKRepository(ctx context.Context, runner backend.Runner, step engine.Step) error {
	name := ""
	if step.Node.Desired != nil {
		name = stringValue(step.Node.Desired, "name")
	}
	if name == "" && step.Prior != nil {
		name, _ = step.Prior.Delete["name"].(string)
	}
	if !providerAPKRepositoryNamePattern.MatchString(name) {
		return fmt.Errorf("invalid managed APK repository identity %q", name)
	}
	begin, end := apkRepositoryMarkers(name)
	_, err := runner.Run(ctx, backend.Command{Name: "delete.apk_repository", Script: apkManagedRepositoryDeleteScript, Arguments: []string{apkRepositoriesPath, begin, end}})
	return err
}

func desiredAPKRepositoryName(node graph.Node) (string, error) {
	name := stringValue(node.Desired, "name")
	if !providerAPKRepositoryNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid managed APK repository identity %q", name)
	}
	return name, nil
}

func apkRepositoryMarkers(name string) (string, string) {
	return "# BEGIN ALPINEFORM REPOSITORY " + name, "# END ALPINEFORM REPOSITORY " + name
}

func splitRepositoryLines(content []byte) []string {
	if len(content) == 0 {
		return []string{}
	}
	text := string(content)
	if strings.HasSuffix(text, "\n") {
		text = strings.TrimSuffix(text, "\n")
	}
	if text == "" {
		return []string{""}
	}
	return strings.Split(text, "\n")
}

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

var (
	providerAccountPattern = regexp.MustCompile(`^(?:[a-z_][a-z0-9_-]{0,31}|[0-9]{1,10})$`)
	numericIDPattern       = regexp.MustCompile(`^[0-9]+$`)
)

const fileInspectScript = `set -eu
path=$1
if [ ! -e "$path" ]; then
  echo missing
  exit 0
fi
if [ ! -f "$path" ]; then
  echo other
  exit 0
fi
echo file
stat -c '%U' "$path"
stat -c '%u' "$path"
stat -c '%G' "$path"
stat -c '%g' "$path"
stat -c '%a' "$path"
stat -c '%s' "$path"
sha256sum "$path" | awk '{print $1}'
`

const fileWriteScript = `set -eu
path=$1
owner=$2
group=$3
mode=$4
parent=${path%/*}
[ -n "$parent" ] || parent=/
mkdir -p "$parent"
if [ -d "$path" ]; then
  echo 'refusing to replace a directory with a file' >&2
  exit 1
fi
tmp=$(mktemp "$parent/.alpineform-file.XXXXXX")
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
cat >"$tmp"
if [ "$(stat -c '%U' "$tmp")" != "$owner" ] && [ "$(stat -c '%u' "$tmp")" != "$owner" ]; then
  chown "$owner" "$tmp"
fi
if [ "$(stat -c '%G' "$tmp")" != "$group" ] && [ "$(stat -c '%g' "$tmp")" != "$group" ]; then
  chgrp "$group" "$tmp"
fi
chmod "$mode" "$tmp"
mv -f "$tmp" "$path"
trap - EXIT HUP INT TERM
`

const fileDeleteScript = `set -eu
path=$1
if [ -d "$path" ]; then
  echo 'refusing to delete a directory as a file' >&2
  exit 1
fi
rm -f "$path"
`

func inspectFile(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	path, err := desiredFilePath(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{
		Name:         "inspect.file",
		Script:       fileInspectScript,
		Arguments:    []string{path},
		RedactOutput: node.Sensitive || node.Ephemeral,
	})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "missing" || lines[0] == "" {
		return engine.ObservedResource{}, nil
	}
	observed := cloneDesired(node.Desired)
	if lines[0] != "file" {
		observed["type"] = lines[0]
		return engine.ObservedResource{Exists: true, Values: observed, Protected: node.Sensitive || node.Ephemeral}, nil
	}
	if len(lines) != 8 {
		return engine.ObservedResource{}, fmt.Errorf("inspect file %q returned %d fields, want 8", path, len(lines))
	}
	owner := lines[1]
	if numericIDPattern.MatchString(stringValue(node.Desired, "owner")) {
		owner = lines[2]
	}
	group := lines[3]
	if numericIDPattern.MatchString(stringValue(node.Desired, "group")) {
		group = lines[4]
	}
	mode := lines[5]
	if len(mode) == 3 {
		mode = "0" + mode
	}
	observed["owner"] = owner
	observed["group"] = group
	observed["mode"] = mode
	if !boolValue(node.Desired, "content_write_only") {
		size, err := strconv.ParseInt(lines[6], 10, 64)
		if err != nil {
			return engine.ObservedResource{}, fmt.Errorf("inspect file %q returned invalid size", path)
		}
		observed["content_bytes"] = size
		observed["content_sha256"] = strings.ToLower(lines[7])
	}
	return engine.ObservedResource{Exists: true, Values: observed, Protected: node.Sensitive || node.Ephemeral}, nil
}

func applyFile(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	path, err := desiredFilePath(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	owner := stringValue(node.Desired, "owner")
	group := stringValue(node.Desired, "group")
	mode := stringValue(node.Desired, "mode")
	if !providerAccountPattern.MatchString(owner) || !providerAccountPattern.MatchString(group) {
		return engine.ObservedResource{}, fmt.Errorf("file %q has an invalid owner or group", path)
	}
	if !validMode(mode) {
		return engine.ObservedResource{}, fmt.Errorf("file %q has invalid mode metadata", path)
	}
	content, ok := node.Payload["content"].(string)
	if !ok {
		return engine.ObservedResource{}, fmt.Errorf("file %q has no provider content payload", path)
	}
	_, err = runner.Run(ctx, backend.Command{
		Name:         "apply.file",
		Script:       fileWriteScript,
		Arguments:    []string{path, owner, group, mode},
		Stdin:        []byte(content),
		RedactStdin:  true,
		RedactOutput: node.Sensitive || node.Ephemeral,
	})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectFile(ctx, runner, node)
}

func deleteFile(ctx context.Context, runner backend.Runner, step engine.Step) error {
	path := ""
	if step.Node.Desired != nil {
		path = stringValue(step.Node.Desired, "path")
	}
	if path == "" && step.Prior != nil {
		path, _ = step.Prior.Delete["path"].(string)
	}
	if err := validateRemoteFilePath(path); err != nil {
		return err
	}
	_, err := runner.Run(ctx, backend.Command{Name: "delete.file", Script: fileDeleteScript, Arguments: []string{path}, RedactOutput: stepIsProtected(step)})
	return err
}

func desiredFilePath(node graph.Node) (string, error) {
	path := stringValue(node.Desired, "path")
	if err := validateRemoteFilePath(path); err != nil {
		return "", err
	}
	return path, nil
}

func validateRemoteFilePath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || strings.ContainsAny(path, "\x00\r\n") {
		return fmt.Errorf("file path %q must be a clean absolute non-root path", path)
	}
	return nil
}

func cloneDesired(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func stringValue(input map[string]any, name string) string {
	value, _ := input[name].(string)
	return value
}

func boolValue(input map[string]any, name string) bool {
	value, _ := input[name].(bool)
	return value
}

func validMode(mode string) bool {
	if len(mode) != 4 {
		return false
	}
	for _, character := range mode {
		if character < '0' || character > '7' {
			return false
		}
	}
	return true
}

func stepIsProtected(step engine.Step) bool {
	if step.Node.Sensitive || step.Node.Ephemeral {
		return true
	}
	return step.Prior != nil && (step.Prior.Protected || step.Prior.Sensitive || step.Prior.Ephemeral)
}

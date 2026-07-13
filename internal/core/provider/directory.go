package provider

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
)

const directoryInspectScript = `set -eu
path=$1
if [ ! -e "$path" ] && [ ! -L "$path" ]; then
  echo missing
  exit 0
fi
if [ -L "$path" ]; then
  echo other
  exit 0
fi
if [ ! -d "$path" ]; then
  echo other
  exit 0
fi
echo directory
stat -c '%U' "$path"
stat -c '%u' "$path"
stat -c '%G' "$path"
stat -c '%g' "$path"
stat -c '%a' "$path"
`

const directoryApplyScript = `set -eu
path=$1
owner=$2
group=$3
mode=$4
if [ -L "$path" ]; then
  echo 'refusing to manage a symbolic link as a directory' >&2
  exit 1
fi
if [ -e "$path" ] && [ ! -d "$path" ]; then
  echo 'refusing to replace a non-directory path with a directory' >&2
  exit 1
fi
mkdir -p "$path"
if [ "$(stat -c '%U' "$path")" != "$owner" ] && [ "$(stat -c '%u' "$path")" != "$owner" ]; then
  chown "$owner" "$path"
fi
if [ "$(stat -c '%G' "$path")" != "$group" ] && [ "$(stat -c '%g' "$path")" != "$group" ]; then
  chgrp "$group" "$path"
fi
chmod "$mode" "$path"
`

const directoryDeleteScript = `set -eu
path=$1
recursive=$2
if [ ! -e "$path" ] && [ ! -L "$path" ]; then
  exit 0
fi
if [ -L "$path" ] || [ ! -d "$path" ]; then
  echo 'refusing to delete a non-directory path as a directory' >&2
  exit 1
fi
if [ "$recursive" = true ]; then
  rm -rf "$path"
else
  rmdir "$path"
fi
`

func inspectDirectory(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	path, err := desiredDirectoryPath(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.directory", Script: directoryInspectScript, Arguments: []string{path}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "missing" || lines[0] == "" {
		return engine.ObservedResource{}, nil
	}
	observed := cloneDesired(node.Desired)
	if lines[0] != "directory" {
		observed["type"] = lines[0]
		return engine.ObservedResource{Exists: true, Values: observed}, nil
	}
	if len(lines) != 6 {
		return engine.ObservedResource{}, fmt.Errorf("inspect directory %q returned %d fields, want 6", path, len(lines))
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
	return engine.ObservedResource{Exists: true, Values: observed}, nil
}

func applyDirectory(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	path, err := desiredDirectoryPath(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	owner := stringValue(node.Desired, "owner")
	group := stringValue(node.Desired, "group")
	mode := stringValue(node.Desired, "mode")
	if !providerAccountPattern.MatchString(owner) || !providerAccountPattern.MatchString(group) || !validMode(mode) {
		return engine.ObservedResource{}, fmt.Errorf("directory %q has invalid ownership or mode metadata", path)
	}
	_, err = runner.Run(ctx, backend.Command{Name: "apply.directory", Script: directoryApplyScript, Arguments: []string{path, owner, group, mode}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectDirectory(ctx, runner, node)
}

func deleteDirectory(ctx context.Context, runner backend.Runner, step engine.Step) error {
	path := ""
	recursive := false
	if step.Node.Desired != nil {
		path = stringValue(step.Node.Desired, "path")
		recursive = boolValue(step.Node.Desired, "recursive_delete")
	}
	if path == "" && step.Prior != nil {
		path, _ = step.Prior.Delete["path"].(string)
		recursive, _ = step.Prior.Delete["recursive"].(bool)
	}
	if err := validateRemoteDirectoryPath(path); err != nil {
		return err
	}
	recursiveArgument := "false"
	if recursive {
		recursiveArgument = "true"
	}
	_, err := runner.Run(ctx, backend.Command{Name: "delete.directory", Script: directoryDeleteScript, Arguments: []string{path, recursiveArgument}})
	return err
}

func desiredDirectoryPath(node graph.Node) (string, error) {
	path := stringValue(node.Desired, "path")
	if err := validateRemoteDirectoryPath(path); err != nil {
		return "", err
	}
	return path, nil
}

func validateRemoteDirectoryPath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || strings.ContainsAny(path, "\x00\r\n") {
		return fmt.Errorf("directory path %q must be a clean absolute non-root path", path)
	}
	return nil
}

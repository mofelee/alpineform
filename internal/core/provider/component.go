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

var componentProviderSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

const componentSourceInspectScript = `set -eu
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
sha256sum "$path" | awk '{print $1}'
`

const componentSourceApplyScript = `set -eu
url=$1
want=$2
path=$3
parent=${path%/*}
[ -n "$parent" ] || parent=/
mkdir -p "$parent"
if [ -d "$path" ]; then
  echo 'refusing to replace a directory with an artifact cache file' >&2
  exit 1
fi
tmp=$(mktemp "$parent/.alpineform-download.XXXXXX")
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
if ! wget -q -O "$tmp" "$url"; then
  echo 'artifact download failed' >&2
  exit 1
fi
actual=$(sha256sum "$tmp" | awk '{print $1}')
if [ "$actual" != "$want" ]; then
  echo "artifact checksum mismatch: expected $want, got $actual" >&2
  exit 1
fi
chmod 0600 "$tmp"
mv -f "$tmp" "$path"
trap - EXIT HUP INT TERM
`

const componentInstallInspectScript = `set -eu
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
sha256sum "$path" | awk '{print $1}'
`

const componentInstallApplyScript = `set -eu
cache=$1
want=$2
path=$3
owner=$4
group=$5
mode=$6
if [ ! -f "$cache" ]; then
  echo 'verified artifact cache file is missing' >&2
  exit 1
fi
parent=${path%/*}
[ -n "$parent" ] || parent=/
mkdir -p "$parent"
if [ -d "$path" ]; then
  echo 'refusing to replace a directory with a component file' >&2
  exit 1
fi
tmp=$(mktemp "$parent/.alpineform-component.XXXXXX")
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
cp "$cache" "$tmp"
actual=$(sha256sum "$tmp" | awk '{print $1}')
if [ "$actual" != "$want" ]; then
  echo "artifact checksum mismatch before install: expected $want, got $actual" >&2
  exit 1
fi
chown "$owner:$group" "$tmp"
chmod "$mode" "$tmp"
mv -f "$tmp" "$path"
trap - EXIT HUP INT TERM
`

func inspectComponentSource(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	path, _, err := componentSourceIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.component_artifact_source", Script: componentSourceInspectScript, Arguments: []string{path}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "" || lines[0] == "missing" {
		return engine.ObservedResource{}, nil
	}
	observed := cloneDesired(node.Desired)
	if lines[0] != "file" {
		observed["type"] = lines[0]
		return engine.ObservedResource{Exists: true, Values: observed}, nil
	}
	if len(lines) != 2 {
		return engine.ObservedResource{}, fmt.Errorf("inspect component artifact cache %q returned %d fields, want 2", path, len(lines))
	}
	observed["sha256"] = strings.ToLower(lines[1])
	return engine.ObservedResource{Exists: true, Values: observed}, nil
}

func applyComponentSource(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	path, digest, err := componentSourceIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	url := stringValue(node.Desired, "url")
	if url == "" {
		return engine.ObservedResource{}, fmt.Errorf("component artifact source has an empty URL")
	}
	_, err = runner.Run(ctx, backend.Command{
		Name: "apply.component_artifact_source", Script: componentSourceApplyScript,
		Arguments: []string{url, digest, path}, RedactOutput: true,
	})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectComponentSource(ctx, runner, node)
}

func deleteComponentSource(ctx context.Context, runner backend.Runner, step engine.Step) error {
	path := componentDeletePath(step)
	if err := validateRemoteFilePath(path); err != nil {
		return err
	}
	_, err := runner.Run(ctx, backend.Command{Name: "delete.component_artifact_source", Script: fileDeleteScript, Arguments: []string{path}})
	return err
}

func inspectComponentInstall(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	path, _, err := componentInstallIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect." + node.Kind, Script: componentInstallInspectScript, Arguments: []string{path}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "" || lines[0] == "missing" {
		return engine.ObservedResource{}, nil
	}
	observed := cloneDesired(node.Desired)
	if lines[0] != "file" {
		observed["type"] = lines[0]
		return engine.ObservedResource{Exists: true, Values: observed}, nil
	}
	if len(lines) != 7 {
		return engine.ObservedResource{}, fmt.Errorf("inspect component install %q returned %d fields, want 7", path, len(lines))
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
	observed["content_sha256"] = strings.ToLower(lines[6])
	return engine.ObservedResource{Exists: true, Values: observed}, nil
}

func applyComponentInstall(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	path, digest, err := componentInstallIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	cachePath := stringValue(node.Desired, "cache_path")
	if err := validateRemoteFilePath(cachePath); err != nil {
		return engine.ObservedResource{}, fmt.Errorf("component artifact cache: %w", err)
	}
	owner := stringValue(node.Desired, "owner")
	group := stringValue(node.Desired, "group")
	mode := stringValue(node.Desired, "mode")
	if !providerAccountPattern.MatchString(owner) || !providerAccountPattern.MatchString(group) || !validMode(mode) {
		return engine.ObservedResource{}, fmt.Errorf("component install %q has invalid owner, group, or mode metadata", path)
	}
	_, err = runner.Run(ctx, backend.Command{
		Name: "apply." + node.Kind, Script: componentInstallApplyScript,
		Arguments: []string{cachePath, digest, path, owner, group, mode}, RedactOutput: true,
	})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectComponentInstall(ctx, runner, node)
}

func deleteComponentInstall(ctx context.Context, runner backend.Runner, step engine.Step) error {
	path := componentDeletePath(step)
	if err := validateRemoteFilePath(path); err != nil {
		return err
	}
	_, err := runner.Run(ctx, backend.Command{Name: "delete.component_install", Script: fileDeleteScript, Arguments: []string{path}, RedactOutput: stepIsProtected(step)})
	return err
}

func componentSourceIdentity(node graph.Node) (string, string, error) {
	path := stringValue(node.Desired, "path")
	if err := validateRemoteFilePath(path); err != nil {
		return "", "", err
	}
	digest := strings.ToLower(stringValue(node.Desired, "sha256"))
	if !componentProviderSHA256Pattern.MatchString(digest) {
		return "", "", fmt.Errorf("component artifact source has invalid SHA-256 metadata")
	}
	return path, digest, nil
}

func componentInstallIdentity(node graph.Node) (string, string, error) {
	path := stringValue(node.Desired, "path")
	if err := validateRemoteFilePath(path); err != nil {
		return "", "", err
	}
	digest := strings.ToLower(stringValue(node.Desired, "content_sha256"))
	if !componentProviderSHA256Pattern.MatchString(digest) {
		return "", "", fmt.Errorf("component install has invalid SHA-256 metadata")
	}
	return path, digest, nil
}

func componentDeletePath(step engine.Step) string {
	if step.Node.Desired != nil {
		if path := stringValue(step.Node.Desired, "path"); path != "" {
			return path
		}
	}
	if step.Prior != nil {
		path, _ := step.Prior.Delete["path"].(string)
		return path
	}
	return ""
}

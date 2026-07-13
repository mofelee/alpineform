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

const apkKeysDirectory = "/etc/apk/keys"

var (
	providerAPKKeyFilenamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._+-]{0,127}$`)
	providerSHA256Pattern         = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

const apkKeyInspectScript = `set -eu
path=$1
if [ -L "$path" ]; then
  echo 'refusing symbolic-link APK key' >&2
  exit 1
fi
if [ ! -e "$path" ]; then
  echo missing
  exit 0
fi
if [ ! -f "$path" ]; then
  echo 'APK key path is not a regular file' >&2
  exit 1
fi
echo key
sha256sum "$path" | awk '{print $1}'
`

const apkKeyWriteScript = `set -eu
path=$1
expected=$2
parent=${path%/*}
mkdir -p "$parent"
if [ -L "$path" ]; then
  echo 'refusing symbolic-link APK key' >&2
  exit 1
fi
if [ -e "$path" ] && [ ! -f "$path" ]; then
  echo 'APK key path is not a regular file' >&2
  exit 1
fi
tmp=$(mktemp "$parent/.alpineform-key.XXXXXX")
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
cat >"$tmp"
actual=$(sha256sum "$tmp" | awk '{print $1}')
if [ "$actual" != "$expected" ]; then
  echo 'APK key payload digest mismatch' >&2
  exit 1
fi
chmod 0644 "$tmp"
chown 0:0 "$tmp"
mv -f "$tmp" "$path"
trap - EXIT HUP INT TERM
`

const apkKeyDeleteScript = `set -eu
path=$1
if [ -L "$path" ]; then
  echo 'refusing symbolic-link APK key' >&2
  exit 1
fi
if [ -e "$path" ] && [ ! -f "$path" ]; then
  echo 'APK key path is not a regular file' >&2
  exit 1
fi
rm -f "$path"
`

func inspectAPKKey(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	filename, err := desiredAPKKeyFilename(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.apk_key", Script: apkKeyInspectScript, Arguments: []string{apkKeysDirectory + "/" + filename}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "missing" || lines[0] == "" {
		return engine.ObservedResource{}, nil
	}
	if len(lines) != 2 || lines[0] != "key" || !providerSHA256Pattern.MatchString(lines[1]) {
		return engine.ObservedResource{}, fmt.Errorf("inspect APK key %q returned an invalid response", filename)
	}
	observed := cloneDesired(node.Desired)
	observed["sha256"] = lines[1]
	return engine.ObservedResource{Exists: true, Values: observed}, nil
}

func applyAPKKey(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	filename, err := desiredAPKKeyFilename(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	digest := stringValue(node.Desired, "sha256")
	if !providerSHA256Pattern.MatchString(digest) {
		return engine.ObservedResource{}, fmt.Errorf("APK key %q has invalid SHA-256 metadata", filename)
	}
	content, ok := node.Payload["content"].([]byte)
	if !ok {
		return engine.ObservedResource{}, fmt.Errorf("APK key %q has no provider content payload", filename)
	}
	if _, err := runner.Run(ctx, backend.Command{
		Name: "apply.apk_key", Script: apkKeyWriteScript, Arguments: []string{apkKeysDirectory + "/" + filename, digest},
		Stdin: content, RedactStdin: true,
	}); err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectAPKKey(ctx, runner, node)
}

func deleteAPKKey(ctx context.Context, runner backend.Runner, step engine.Step) error {
	filename := ""
	if step.Node.Desired != nil {
		filename = stringValue(step.Node.Desired, "filename")
	}
	if filename == "" && step.Prior != nil {
		filename, _ = step.Prior.Delete["filename"].(string)
	}
	if !providerAPKKeyFilenamePattern.MatchString(filename) {
		return fmt.Errorf("invalid APK key filename %q", filename)
	}
	_, err := runner.Run(ctx, backend.Command{Name: "delete.apk_key", Script: apkKeyDeleteScript, Arguments: []string{apkKeysDirectory + "/" + filename}})
	return err
}

func desiredAPKKeyFilename(node graph.Node) (string, error) {
	filename := stringValue(node.Desired, "filename")
	if !providerAPKKeyFilenamePattern.MatchString(filename) {
		return "", fmt.Errorf("invalid APK key filename %q", filename)
	}
	return filename, nil
}

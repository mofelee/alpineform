package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

var (
	providerAPKPackageNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+_.-]{0,127}$`)
	providerAPKWorldIntentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+_.-]{0,127}(?:@[A-Za-z0-9][A-Za-z0-9._-]{0,63})?$`)
)

const packageInspectScript = `set -eu
name=$1
intent=$2
installed=false
metadata=
if apk info --exists "$name" >/dev/null 2>&1; then
  installed=true
  metadata=$(apk info --verbose "$name" 2>/dev/null | head -n 1 || true)
fi
world=false
if [ -L /etc/apk/world ]; then
  echo 'refusing symbolic-link APK world file' >&2
  exit 1
fi
if [ -e /etc/apk/world ] && [ ! -f /etc/apk/world ]; then
  echo 'APK world path is not a regular file' >&2
  exit 1
fi
if [ -f /etc/apk/world ] && awk -v intent="$intent" '$0 == intent { found=1 } END { exit !found }' /etc/apk/world; then
  world=true
fi
if [ "$installed" = false ] && [ "$world" = false ]; then
  echo missing
  exit 0
fi
echo package
echo "$installed"
echo "$world"
echo "$metadata"
`

const packageAddScript = `set -eu
intent=$1
apk --quiet add "$intent"
`

const packageDeleteScript = `set -eu
name=$1
apk --quiet del "$name"
`

func inspectPackage(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	name, intent, err := desiredPackageIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.package", Script: packageInspectScript, Arguments: []string{name, intent}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "missing" || lines[0] == "" {
		return engine.ObservedResource{}, nil
	}
	if len(lines) != 4 || lines[0] != "package" || (lines[1] != "true" && lines[1] != "false") || (lines[2] != "true" && lines[2] != "false") {
		return engine.ObservedResource{}, fmt.Errorf("inspect APK package %q returned an invalid response", name)
	}
	observed := cloneDesired(node.Desired)
	observed["installed"] = lines[1] == "true"
	observed["world"] = lines[2] == "true"
	if lines[3] != "" {
		observed["installed_package"] = lines[3]
	}
	digest := ""
	if lines[1] == "true" && lines[2] == "true" {
		digest = corestate.Digest(node.Desired)
	}
	return engine.ObservedResource{Exists: true, Values: observed, Digest: digest}, nil
}

func applyPackage(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	_, intent, err := desiredPackageIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if stringValue(node.Desired, "ensure") != "present" {
		return engine.ObservedResource{}, fmt.Errorf("APK package apply requires ensure = \"present\"")
	}
	if _, err := runner.Run(ctx, backend.Command{Name: "apply.package", Script: packageAddScript, Arguments: []string{intent}}); err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectPackage(ctx, runner, node)
}

func deletePackage(ctx context.Context, runner backend.Runner, step engine.Step) error {
	if step.Action != engine.ActionDelete || stringValue(step.Node.Desired, "ensure") != "absent" {
		return fmt.Errorf("APK package deletion requires an explicit ensure = \"absent\" resource")
	}
	name := stringValue(step.Node.Desired, "name")
	if !providerAPKPackageNamePattern.MatchString(name) {
		return fmt.Errorf("invalid APK package name %q", name)
	}
	_, err := runner.Run(ctx, backend.Command{Name: "delete.package", Script: packageDeleteScript, Arguments: []string{name}})
	return err
}

func desiredPackageIdentity(node graph.Node) (string, string, error) {
	name := stringValue(node.Desired, "name")
	intent := stringValue(node.Desired, "world_intent")
	if !providerAPKPackageNamePattern.MatchString(name) || !providerAPKWorldIntentPattern.MatchString(intent) {
		return "", "", fmt.Errorf("invalid APK package identity %q", name)
	}
	return name, intent, nil
}

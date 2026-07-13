package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

const apkUpdateMarkerPath = "/var/lib/alpineform/apk-update.sha256"

const apkUpdateInspectScript = `set -eu
path=$1
if [ -L "$path" ]; then
  echo 'refusing symbolic-link APK update marker' >&2
  exit 1
fi
if [ ! -e "$path" ]; then
  echo missing
  exit 0
fi
if [ ! -f "$path" ]; then
  echo 'APK update marker is not a regular file' >&2
  exit 1
fi
echo marker
cat "$path"
`

const apkUpdateApplyScript = `set -eu
path=$1
fingerprint=$2
apk --quiet update
parent=${path%/*}
mkdir -p "$parent"
if [ -L "$path" ]; then
  echo 'refusing symbolic-link APK update marker' >&2
  exit 1
fi
if [ -e "$path" ] && [ ! -f "$path" ]; then
  echo 'APK update marker is not a regular file' >&2
  exit 1
fi
tmp=$(mktemp "$parent/.alpineform-apk-update.XXXXXX")
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
printf '%s\n' "$fingerprint" >"$tmp"
chmod 0600 "$tmp"
chown 0:0 "$tmp"
mv -f "$tmp" "$path"
trap - EXIT HUP INT TERM
`

func inspectAPKUpdate(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	fingerprint := stringValue(node.Desired, "fingerprint")
	if !providerSHA256Pattern.MatchString(fingerprint) {
		return engine.ObservedResource{}, fmt.Errorf("APK update has invalid fingerprint metadata")
	}
	ready, err := apkInputsReady(ctx, runner, node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.apk_update", Script: apkUpdateInspectScript, Arguments: []string{apkUpdateMarkerPath}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "missing" || lines[0] == "" {
		return engine.ObservedResource{}, nil
	}
	if len(lines) != 2 || lines[0] != "marker" || !providerSHA256Pattern.MatchString(lines[1]) {
		return engine.ObservedResource{}, fmt.Errorf("inspect APK update marker returned an invalid response")
	}
	observed := cloneDesired(node.Desired)
	observed["fingerprint"] = lines[1]
	if !ready {
		observed["inputs_ready"] = false
	}
	return engine.ObservedResource{Exists: true, Values: observed}, nil
}

func applyAPKUpdate(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	fingerprint := stringValue(node.Desired, "fingerprint")
	if !providerSHA256Pattern.MatchString(fingerprint) {
		return engine.ObservedResource{}, fmt.Errorf("APK update has invalid fingerprint metadata")
	}
	if _, err := runner.Run(ctx, backend.Command{Name: "apply.apk_update", Script: apkUpdateApplyScript, Arguments: []string{apkUpdateMarkerPath, fingerprint}}); err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectAPKUpdate(ctx, runner, node)
}

func apkInputsReady(ctx context.Context, runner backend.Runner, node graph.Node) (bool, error) {
	resources, ok := node.Payload["readiness"].([]graph.Node)
	if !ok {
		return false, fmt.Errorf("APK update has no readiness payload")
	}
	for _, resource := range resources {
		var observed engine.ObservedResource
		var err error
		switch resource.Kind {
		case "apk_key":
			observed, err = inspectAPKKey(ctx, runner, resource)
		case "apk_repository", "apk_repositories":
			observed, err = inspectAPKRepository(ctx, runner, resource)
		default:
			return false, fmt.Errorf("APK update has unsupported readiness kind %q", resource.Kind)
		}
		if err != nil {
			return false, err
		}
		ensure := stringValue(resource.Desired, "ensure")
		if ensure == "absent" {
			if observed.Exists {
				return false, nil
			}
			continue
		}
		if !observed.Exists || corestate.Digest(observed.Values) != corestate.Digest(resource.Desired) {
			return false, nil
		}
	}
	return true, nil
}

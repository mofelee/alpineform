package provider

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

var providerOpenRCNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

const serviceInspectScript = `set -eu
name=$1
runlevel=$2
init=/etc/init.d/$name
if [ -L "$init" ]; then
  echo 'refusing symbolic-link OpenRC init service' >&2
  exit 1
fi
if [ ! -e "$init" ]; then
  echo missing
  exit 0
fi
if [ ! -f "$init" ] || [ ! -x "$init" ]; then
  echo 'OpenRC init service is not a regular executable file' >&2
  exit 1
fi
enabled=false
if [ -e "/etc/runlevels/$runlevel/$name" ]; then enabled=true; fi
set +e
status_output=$(rc-service "$name" status 2>&1)
status_code=$?
set -e
case "$status_output" in
  *"status: started"*) runtime=started ;;
  *"status: stopped"*) runtime=stopped ;;
  *"status: crashed"*) runtime=crashed ;;
  *) runtime=inactive ;;
esac
echo service
echo "$enabled"
echo "$runtime"
echo "$status_code"
`

const serviceApplyScript = `set -eu
name=$1
runlevel=$2
enabled=$3
state=$4
operation=$5
previous_runlevel=$6
init=/etc/init.d/$name
if [ -L "$init" ] || [ ! -f "$init" ] || [ ! -x "$init" ]; then
  echo 'OpenRC service requires a regular executable init file' >&2
  exit 1
fi
if [ -n "$previous_runlevel" ] && [ "$previous_runlevel" != "$runlevel" ] && [ -e "/etc/runlevels/$previous_runlevel/$name" ]; then
  rc-update del "$name" "$previous_runlevel" >/dev/null
fi
if [ "$enabled" = true ]; then
  if [ ! -e "/etc/runlevels/$runlevel/$name" ]; then rc-update add "$name" "$runlevel" >/dev/null; fi
else
  if [ -e "/etc/runlevels/$runlevel/$name" ]; then rc-update del "$name" "$runlevel" >/dev/null; fi
fi
case "$state" in
  running)
    command=start
    case "$operation" in
      restarted) command=restart ;;
      reloaded) command=reload ;;
      '') ;;
      *) echo 'unsupported OpenRC service operation' >&2; exit 1 ;;
    esac
    if ! rc-service "$name" "$command" >/dev/null; then
      echo "OpenRC service $name does not support or failed operation $command" >&2
      exit 1
    fi
    ;;
  stopped)
    if [ -n "$operation" ]; then echo 'OpenRC service operation requires running state' >&2; exit 1; fi
    rc-service "$name" stop >/dev/null
    ;;
  *) echo 'unsupported OpenRC runtime state' >&2; exit 1 ;;
esac
`

func inspectService(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	name, runlevel, err := desiredServiceIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.service", Script: serviceInspectScript, Arguments: []string{name, runlevel}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "missing" || lines[0] == "" {
		return engine.ObservedResource{}, nil
	}
	if len(lines) != 4 || lines[0] != "service" || (lines[1] != "true" && lines[1] != "false") {
		return engine.ObservedResource{}, fmt.Errorf("inspect OpenRC service %q returned an invalid response", name)
	}
	if _, err := strconv.Atoi(lines[3]); err != nil {
		return engine.ObservedResource{}, fmt.Errorf("inspect OpenRC service %q returned an invalid status code", name)
	}
	actualState := "stopped"
	if lines[2] == "started" {
		actualState = "running"
	} else if lines[2] == "crashed" {
		actualState = "crashed"
	} else if lines[2] != "stopped" && lines[2] != "inactive" {
		return engine.ObservedResource{}, fmt.Errorf("inspect OpenRC service %q returned unknown runtime class %q", name, lines[2])
	}
	observed := cloneDesired(node.Desired)
	observed["enabled"] = lines[1] == "true"
	observed["state"] = actualState
	observed["runtime_status"] = lines[2]
	digest := ""
	if observed["enabled"] == node.Desired["enabled"] && actualState == stringValue(node.Desired, "state") {
		digest = corestate.Digest(node.Desired)
	}
	return engine.ObservedResource{Exists: true, Values: observed, Digest: digest}, nil
}

func applyService(ctx context.Context, runner backend.Runner, step engine.Step) (engine.ObservedResource, error) {
	name, runlevel, err := desiredServiceIdentity(step.Node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	state := stringValue(step.Node.Desired, "state")
	if state != "running" && state != "stopped" {
		return engine.ObservedResource{}, fmt.Errorf("OpenRC service %q has unsupported runtime state %q", name, state)
	}
	enabled := boolValue(step.Node.Desired, "enabled")
	previousRunlevel := ""
	if step.Prior != nil {
		previousRunlevel, _ = step.Prior.Observed["runlevel"].(string)
		if previousRunlevel != "" && !providerOpenRCNamePattern.MatchString(previousRunlevel) {
			return engine.ObservedResource{}, fmt.Errorf("OpenRC service %q has invalid prior runlevel identity", name)
		}
	}
	operation := ""
	if step.Action == engine.ActionUpdate && len(step.TriggeredBy) > 0 {
		operation = stringValue(step.Node.Desired, "operation")
		if operation != "restarted" && operation != "reloaded" {
			return engine.ObservedResource{}, fmt.Errorf("OpenRC service %q has unsupported operation %q", name, operation)
		}
	}
	if _, err := runner.Run(ctx, backend.Command{
		Name: "apply.service", Script: serviceApplyScript,
		Arguments: []string{name, runlevel, strconv.FormatBool(enabled), state, operation, previousRunlevel},
	}); err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectService(ctx, runner, step.Node)
}

func desiredServiceIdentity(node graph.Node) (string, string, error) {
	name := stringValue(node.Desired, "name")
	runlevel := stringValue(node.Desired, "runlevel")
	if !providerOpenRCNamePattern.MatchString(name) || !providerOpenRCNamePattern.MatchString(runlevel) {
		return "", "", fmt.Errorf("invalid OpenRC service or runlevel identity")
	}
	return name, runlevel, nil
}

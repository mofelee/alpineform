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
)

var providerManagedAccountPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

const groupInspectScript = `set -eu
name=$1
awk -F: -v name="$name" '
$1 == name {
  print "group"
  print $3
  found=1
  exit
}
END { if (!found) print "missing" }
' /etc/group
`

const groupApplyScript = `set -eu
name=$1
gid=$2
system=$3
current_gid=$(awk -F: -v name="$name" '$1 == name { print $3; exit }' /etc/group)
if [ -z "$current_gid" ]; then
  if [ "$system" = true ]; then
    if [ -n "$gid" ]; then
      addgroup -S -g "$gid" "$name"
    else
      addgroup -S "$name"
    fi
  elif [ -n "$gid" ]; then
    addgroup -g "$gid" "$name"
  else
    addgroup "$name"
  fi
  exit 0
fi
if [ -z "$gid" ] || [ "$current_gid" = "$gid" ]; then
  exit 0
fi
if awk -F: -v name="$name" -v gid="$gid" '$1 != name && $3 == gid { found=1 } END { exit found ? 0 : 1 }' /etc/group; then
  echo 'refusing to reuse a gid owned by another group' >&2
  exit 1
fi
if awk -F: -v gid="$current_gid" '$4 == gid { found=1 } END { exit found ? 0 : 1 }' /etc/passwd; then
  echo 'refusing to change the gid of a primary group' >&2
  exit 1
fi
tmp=$(mktemp /etc/.alpineform-group.XXXXXX)
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
file_uid=$(stat -c '%u' /etc/group)
file_gid=$(stat -c '%g' /etc/group)
file_mode=$(stat -c '%a' /etc/group)
awk -F: -v OFS=: -v name="$name" -v gid="$gid" '$1 == name { $3=gid } { print }' /etc/group >"$tmp"
chown "$file_uid:$file_gid" "$tmp"
chmod "$file_mode" "$tmp"
mv -f "$tmp" /etc/group
trap - EXIT HUP INT TERM
`

const groupDeleteScript = `set -eu
name=$1
entry=$(awk -F: -v name="$name" '$1 == name { print; exit }' /etc/group)
if [ -z "$entry" ]; then
  exit 0
fi
gid=$(printf '%s\n' "$entry" | awk -F: '{ print $3 }')
members=$(printf '%s\n' "$entry" | awk -F: '{ print $4 }')
if [ "$gid" = 0 ]; then
  echo 'refusing to delete a gid 0 group' >&2
  exit 1
fi
if [ -n "$members" ]; then
  echo 'refusing to delete a group with supplementary members' >&2
  exit 1
fi
if awk -F: -v gid="$gid" '$4 == gid { found=1 } END { exit found ? 0 : 1 }' /etc/passwd; then
  echo 'refusing to delete a primary group' >&2
  exit 1
fi
delgroup "$name"
`

func inspectGroup(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	name, err := desiredGroupName(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.group", Script: groupInspectScript, Arguments: []string{name}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "missing" || lines[0] == "" {
		return engine.ObservedResource{}, nil
	}
	if len(lines) != 2 || lines[0] != "group" {
		return engine.ObservedResource{}, fmt.Errorf("inspect group %q returned an invalid record", name)
	}
	observed := cloneDesired(node.Desired)
	if stringValue(node.Desired, "gid") != "" {
		observed["gid"] = lines[1]
	}
	return engine.ObservedResource{Exists: true, Values: observed}, nil
}

func applyGroup(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	name, err := desiredGroupName(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	gid := stringValue(node.Desired, "gid")
	if err := validateNumericID(gid); err != nil {
		return engine.ObservedResource{}, fmt.Errorf("group %q gid: %w", name, err)
	}
	system := "false"
	if boolValue(node.Desired, "system") {
		system = "true"
	}
	_, err = runner.Run(ctx, backend.Command{Name: "apply.group", Script: groupApplyScript, Arguments: []string{name, gid, system}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectGroup(ctx, runner, node)
}

func deleteGroup(ctx context.Context, runner backend.Runner, step engine.Step) error {
	name := ""
	if step.Node.Desired != nil {
		name = stringValue(step.Node.Desired, "name")
	}
	if name == "" && step.Prior != nil {
		name, _ = step.Prior.Delete["name"].(string)
	}
	if err := validateManagedAccountName(name); err != nil {
		return err
	}
	_, err := runner.Run(ctx, backend.Command{Name: "delete.group", Script: groupDeleteScript, Arguments: []string{name}})
	return err
}

func desiredGroupName(node graph.Node) (string, error) {
	name := stringValue(node.Desired, "name")
	if err := validateManagedAccountName(name); err != nil {
		return "", err
	}
	return name, nil
}

func validateManagedAccountName(name string) error {
	if !providerManagedAccountPattern.MatchString(name) {
		return fmt.Errorf("account name %q must be a valid Alpine account name", name)
	}
	return nil
}

func validateNumericID(value string) error {
	if value == "" {
		return nil
	}
	if _, err := strconv.ParseUint(value, 10, 31); err != nil {
		return fmt.Errorf("must be an integer between 0 and 2147483647")
	}
	return nil
}

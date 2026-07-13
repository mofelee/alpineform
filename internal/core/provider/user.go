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

const userInspectScript = `set -eu
name=$1
entry=$(awk -F: -v name="$name" '$1 == name { print; exit }' /etc/passwd)
if [ -z "$entry" ]; then
  echo missing
  exit 0
fi
uid=$(printf '%s\n' "$entry" | awk -F: '{ print $3 }')
gid=$(printf '%s\n' "$entry" | awk -F: '{ print $4 }')
home=$(printf '%s\n' "$entry" | awk -F: '{ print $6 }')
shell=$(printf '%s\n' "$entry" | awk -F: '{ print $7 }')
group=$(awk -F: -v gid="$gid" '$3 == gid { print $1; exit }' /etc/group)
[ -n "$group" ] || group=$gid
printf 'user\n%s\n%s\n%s\n%s\n%s\n' "$uid" "$gid" "$group" "$home" "$shell"
`

const userApplyScript = `set -eu
name=$1
uid=$2
group=$3
home=$4
shell=$5
system=$6
group_name=
group_gid=
if [ -n "$group" ]; then
  group_entry=$(awk -F: -v group="$group" '$1 == group || $3 == group { print; exit }' /etc/group)
  if [ -z "$group_entry" ]; then
    echo 'primary group does not exist' >&2
    exit 1
  fi
  group_name=$(printf '%s\n' "$group_entry" | awk -F: '{ print $1 }')
  group_gid=$(printf '%s\n' "$group_entry" | awk -F: '{ print $3 }')
fi
if [ -n "$home" ]; then
  if [ -L "$home" ] || { [ -e "$home" ] && [ ! -d "$home" ]; }; then
    echo 'refusing to use a non-directory or symbolic-link home path' >&2
    exit 1
  fi
fi
entry=$(awk -F: -v name="$name" '$1 == name { print; exit }' /etc/passwd)
if [ -z "$entry" ]; then
  set -- -D
  if [ "$system" = true ]; then
    set -- "$@" -S
  fi
  if [ -n "$uid" ]; then
    set -- "$@" -u "$uid"
  fi
  if [ -n "$group_name" ]; then
    set -- "$@" -G "$group_name"
  fi
  if [ -n "$home" ]; then
    set -- "$@" -h "$home"
  fi
  if [ -n "$shell" ]; then
    set -- "$@" -s "$shell"
  fi
  adduser "$@" "$name"
else
  current_uid=$(printf '%s\n' "$entry" | awk -F: '{ print $3 }')
  if [ "$current_uid" = 0 ]; then
    echo 'refusing to modify a uid 0 user' >&2
    exit 1
  fi
  if [ -n "$uid" ] && [ "$current_uid" != "$uid" ] && awk -F: -v name="$name" -v uid="$uid" '$1 != name && $3 == uid { found=1 } END { exit found ? 0 : 1 }' /etc/passwd; then
    echo 'refusing to reuse a uid owned by another user' >&2
    exit 1
  fi
  tmp=$(mktemp /etc/.alpineform-passwd.XXXXXX)
  cleanup() { rm -f "$tmp"; }
  trap cleanup EXIT HUP INT TERM
  file_uid=$(stat -c '%u' /etc/passwd)
  file_gid=$(stat -c '%g' /etc/passwd)
  file_mode=$(stat -c '%a' /etc/passwd)
  awk -F: -v OFS=: -v name="$name" -v uid="$uid" -v gid="$group_gid" -v home="$home" -v shell="$shell" '
  $1 == name {
    if (uid != "") $3=uid
    if (gid != "") $4=gid
    if (home != "") $6=home
    if (shell != "") $7=shell
  }
  { print }
  ' /etc/passwd >"$tmp"
  chown "$file_uid:$file_gid" "$tmp"
  chmod "$file_mode" "$tmp"
  mv -f "$tmp" /etc/passwd
  trap - EXIT HUP INT TERM
fi
if [ -n "$home" ]; then
  mkdir -p "$home"
  home_group=$(awk -F: -v name="$name" '$1 == name { print $4; exit }' /etc/passwd)
  chown "$name:$home_group" "$home"
fi
`

const userDeleteScript = `set -eu
name=$1
entry=$(awk -F: -v name="$name" '$1 == name { print; exit }' /etc/passwd)
if [ -z "$entry" ]; then
  exit 0
fi
uid=$(printf '%s\n' "$entry" | awk -F: '{ print $3 }')
if [ "$uid" = 0 ]; then
  echo 'refusing to delete a uid 0 user' >&2
  exit 1
fi
if awk -F: -v name="$name" '
{
  count=split($4, members, ",")
  for (index=1; index<=count; index++) {
    if (members[index] == name) found=1
  }
}
END { exit found ? 0 : 1 }
' /etc/group; then
  echo 'refusing to delete a user with supplementary group memberships' >&2
  exit 1
fi
deluser "$name"
`

func inspectUser(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	name, err := desiredUserName(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.user", Script: userInspectScript, Arguments: []string{name}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "missing" || lines[0] == "" {
		return engine.ObservedResource{}, nil
	}
	if len(lines) != 6 || lines[0] != "user" {
		return engine.ObservedResource{}, fmt.Errorf("inspect user %q returned an invalid record", name)
	}
	observed := cloneDesired(node.Desired)
	if stringValue(node.Desired, "uid") != "" {
		observed["uid"] = lines[1]
	}
	if group := stringValue(node.Desired, "group"); group != "" {
		if numericIDPattern.MatchString(group) {
			observed["group"] = lines[2]
		} else {
			observed["group"] = lines[3]
		}
	}
	if stringValue(node.Desired, "home") != "" {
		observed["home"] = lines[4]
	}
	if stringValue(node.Desired, "shell") != "" {
		observed["shell"] = lines[5]
	}
	return engine.ObservedResource{Exists: true, Values: observed}, nil
}

func applyUser(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	name, err := desiredUserName(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	uid := stringValue(node.Desired, "uid")
	if err := validateNumericID(uid); err != nil {
		return engine.ObservedResource{}, fmt.Errorf("user %q uid: %w", name, err)
	}
	if uid == "0" {
		return engine.ObservedResource{}, fmt.Errorf("user %q uid 0 is reserved", name)
	}
	group := stringValue(node.Desired, "group")
	if group != "" && !providerManagedAccountPattern.MatchString(group) && validateNumericID(group) != nil {
		return engine.ObservedResource{}, fmt.Errorf("user %q has an invalid primary group", name)
	}
	home := stringValue(node.Desired, "home")
	if home != "" && (!filepath.IsAbs(home) || filepath.Clean(home) != home || home == "/" || strings.ContainsAny(home, "\x00\r\n")) {
		return engine.ObservedResource{}, fmt.Errorf("user %q home must be a clean absolute non-root path", name)
	}
	shell := stringValue(node.Desired, "shell")
	if shell != "" && (!filepath.IsAbs(shell) || filepath.Clean(shell) != shell || strings.ContainsAny(shell, "\x00\r\n")) {
		return engine.ObservedResource{}, fmt.Errorf("user %q shell must be a clean absolute path", name)
	}
	system := "false"
	if boolValue(node.Desired, "system") {
		system = "true"
	}
	_, err = runner.Run(ctx, backend.Command{Name: "apply.user", Script: userApplyScript, Arguments: []string{name, uid, group, home, shell, system}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectUser(ctx, runner, node)
}

func deleteUser(ctx context.Context, runner backend.Runner, step engine.Step) error {
	name := ""
	if step.Node.Desired != nil {
		name = stringValue(step.Node.Desired, "name")
	}
	if name == "" && step.Prior != nil {
		name, _ = step.Prior.Delete["name"].(string)
	}
	if err := validateManagedUserName(name); err != nil {
		return err
	}
	_, err := runner.Run(ctx, backend.Command{Name: "delete.user", Script: userDeleteScript, Arguments: []string{name}})
	return err
}

func desiredUserName(node graph.Node) (string, error) {
	name := stringValue(node.Desired, "name")
	if err := validateManagedUserName(name); err != nil {
		return "", err
	}
	return name, nil
}

func validateManagedUserName(name string) error {
	if !providerManagedAccountPattern.MatchString(name) || name == "root" {
		return fmt.Errorf("user name %q must be a valid non-root Alpine account name", name)
	}
	return nil
}

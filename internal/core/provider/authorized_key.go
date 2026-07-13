package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
)

const authorizedKeyInspectScript = `set -eu
user=$1
key_type=$2
key_blob=$3
entry=$(awk -F: -v user="$user" '$1 == user { print; exit }' /etc/passwd)
if [ -z "$entry" ]; then
  echo missing
  exit 0
fi
uid=$(printf '%s\n' "$entry" | awk -F: '{ print $3 }')
gid=$(printf '%s\n' "$entry" | awk -F: '{ print $4 }')
home=$(printf '%s\n' "$entry" | awk -F: '{ print $6 }')
case "$home" in
  /*) ;;
  *) echo 'user home is not absolute' >&2; exit 1 ;;
esac
case "$home" in
  *'/../'*|*'/..'|*'/./'*|*'/.'|*'//'*) echo 'user home is not clean' >&2; exit 1 ;;
esac
if [ "$home" = / ]; then
  echo 'refusing root as an authorized-key home' >&2
  exit 1
fi
dir=$home/.ssh
file=$dir/authorized_keys
if [ -L "$home" ] || [ -L "$dir" ] || [ -L "$file" ]; then
  echo 'refusing to inspect authorized keys through a symbolic link' >&2
  exit 1
fi
if [ ! -f "$file" ] || ! awk -v type="$key_type" -v blob="$key_blob" '
{
  for (i=1; i<NF; i++) {
    if ($i == type && $(i+1) == blob) found=1
  }
}
END { exit found ? 0 : 1 }
' "$file"; then
  echo missing
  exit 0
fi
metadata_ok=false
if [ "$(stat -c '%u' "$dir")" = "$uid" ] && [ "$(stat -c '%g' "$dir")" = "$gid" ] && [ "$(stat -c '%a' "$dir")" = 700 ] && [ "$(stat -c '%u' "$file")" = "$uid" ] && [ "$(stat -c '%g' "$file")" = "$gid" ] && [ "$(stat -c '%a' "$file")" = 600 ]; then
  metadata_ok=true
fi
printf 'key\n%s\n' "$metadata_ok"
`

const authorizedKeyApplyScript = `set -eu
user=$1
line=$2
key_type=$3
key_blob=$4
entry=$(awk -F: -v user="$user" '$1 == user { print; exit }' /etc/passwd)
if [ -z "$entry" ]; then
  echo 'user does not exist' >&2
  exit 1
fi
uid=$(printf '%s\n' "$entry" | awk -F: '{ print $3 }')
gid=$(printf '%s\n' "$entry" | awk -F: '{ print $4 }')
home=$(printf '%s\n' "$entry" | awk -F: '{ print $6 }')
case "$home" in
  /*) ;;
  *) echo 'user home is not absolute' >&2; exit 1 ;;
esac
case "$home" in
  *'/../'*|*'/..'|*'/./'*|*'/.'|*'//'*) echo 'user home is not clean' >&2; exit 1 ;;
esac
if [ "$home" = / ] || [ -L "$home" ] || { [ -e "$home" ] && [ ! -d "$home" ]; }; then
  echo 'refusing unsafe user home for authorized keys' >&2
  exit 1
fi
mkdir -p "$home"
chown "$uid:$gid" "$home"
dir=$home/.ssh
file=$dir/authorized_keys
if [ -L "$dir" ] || { [ -e "$dir" ] && [ ! -d "$dir" ]; }; then
  echo 'refusing unsafe .ssh path' >&2
  exit 1
fi
mkdir -p "$dir"
if [ -L "$file" ] || [ -d "$file" ]; then
  echo 'refusing unsafe authorized_keys path' >&2
  exit 1
fi
tmp=$(mktemp "$dir/.alpineform-authorized-keys.XXXXXX")
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
if [ -f "$file" ]; then
  cat "$file" >"$tmp"
fi
if ! awk -v type="$key_type" -v blob="$key_blob" '
{
  for (i=1; i<NF; i++) {
    if ($i == type && $(i+1) == blob) found=1
  }
}
END { exit found ? 0 : 1 }
' "$tmp"; then
  printf '%s\n' "$line" >>"$tmp"
fi
chown "$uid:$gid" "$dir" "$tmp"
chmod 0700 "$dir"
chmod 0600 "$tmp"
mv -f "$tmp" "$file"
trap - EXIT HUP INT TERM
`

const authorizedKeyDeleteScript = `set -eu
user=$1
key_type=$2
key_blob=$3
entry=$(awk -F: -v user="$user" '$1 == user { print; exit }' /etc/passwd)
if [ -z "$entry" ]; then
  exit 0
fi
uid=$(printf '%s\n' "$entry" | awk -F: '{ print $3 }')
gid=$(printf '%s\n' "$entry" | awk -F: '{ print $4 }')
home=$(printf '%s\n' "$entry" | awk -F: '{ print $6 }')
case "$home" in
  /*) ;;
  *) echo 'user home is not absolute' >&2; exit 1 ;;
esac
case "$home" in
  *'/../'*|*'/..'|*'/./'*|*'/.'|*'//'*) echo 'user home is not clean' >&2; exit 1 ;;
esac
if [ "$home" = / ]; then
  echo 'refusing root as an authorized-key home' >&2
  exit 1
fi
dir=$home/.ssh
file=$dir/authorized_keys
if [ -L "$home" ] || [ -L "$dir" ] || [ -L "$file" ]; then
  echo 'refusing to delete authorized keys through a symbolic link' >&2
  exit 1
fi
if [ ! -f "$file" ]; then
  exit 0
fi
tmp=$(mktemp "$dir/.alpineform-authorized-keys.XXXXXX")
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
awk -v type="$key_type" -v blob="$key_blob" '
{
  matched=0
  for (i=1; i<NF; i++) {
    if ($i == type && $(i+1) == blob) matched=1
  }
  if (!matched) print
}
' "$file" >"$tmp"
chown "$uid:$gid" "$dir" "$tmp"
chmod 0700 "$dir"
chmod 0600 "$tmp"
mv -f "$tmp" "$file"
trap - EXIT HUP INT TERM
`

func inspectAuthorizedKey(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	user, keyType, keyBlob, err := desiredAuthorizedKeyIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.authorized_key", Script: authorizedKeyInspectScript, Arguments: []string{user, keyType, keyBlob}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "missing" || lines[0] == "" {
		return engine.ObservedResource{}, nil
	}
	if len(lines) != 2 || lines[0] != "key" || (lines[1] != "true" && lines[1] != "false") {
		return engine.ObservedResource{}, fmt.Errorf("inspect authorized key for %q returned an invalid record", user)
	}
	observed := cloneDesired(node.Desired)
	observed["metadata_ok"] = lines[1] == "true"
	return engine.ObservedResource{Exists: true, Values: observed}, nil
}

func applyAuthorizedKey(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	user, keyType, keyBlob, err := desiredAuthorizedKeyIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	line := stringValue(node.Payload, "line")
	if line == "" || strings.ContainsAny(line, "\x00\r\n") {
		return engine.ObservedResource{}, fmt.Errorf("authorized key for %q has an invalid provider payload", user)
	}
	if _, err := runner.Run(ctx, backend.Command{Name: "apply.authorized_key", Script: authorizedKeyApplyScript, Arguments: []string{user, line, keyType, keyBlob}}); err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectAuthorizedKey(ctx, runner, node)
}

func deleteAuthorizedKey(ctx context.Context, runner backend.Runner, step engine.Step) error {
	user, keyType, keyBlob := authorizedKeyDeletionIdentity(step)
	if err := validateManagedUserName(user); err != nil {
		return err
	}
	if err := validateAuthorizedKeyParts(keyType, keyBlob); err != nil {
		return err
	}
	_, err := runner.Run(ctx, backend.Command{Name: "delete.authorized_key", Script: authorizedKeyDeleteScript, Arguments: []string{user, keyType, keyBlob}})
	return err
}

func desiredAuthorizedKeyIdentity(node graph.Node) (string, string, string, error) {
	user := stringValue(node.Desired, "user")
	if err := validateManagedUserName(user); err != nil {
		return "", "", "", err
	}
	keyType := stringValue(node.Payload, "key_type")
	keyBlob := stringValue(node.Payload, "key_blob")
	if err := validateAuthorizedKeyParts(keyType, keyBlob); err != nil {
		return "", "", "", err
	}
	return user, keyType, keyBlob, nil
}

func authorizedKeyDeletionIdentity(step engine.Step) (string, string, string) {
	values := map[string]any(nil)
	user := stringValue(step.Node.Desired, "user")
	if step.Node.Desired != nil {
		values, _ = step.Node.Desired["delete"].(map[string]any)
	}
	if values == nil && step.Prior != nil {
		values = step.Prior.Delete
	}
	if user == "" {
		user = stringValue(values, "user")
	}
	return user, stringValue(values, "key_type"), stringValue(values, "key_blob")
}

func validateAuthorizedKeyParts(keyType, keyBlob string) error {
	if keyType == "" || keyBlob == "" || strings.ContainsAny(keyType+keyBlob, "\x00\r\n \t") {
		return fmt.Errorf("authorized key identity is invalid")
	}
	return nil
}

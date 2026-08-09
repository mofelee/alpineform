package provider

import (
	"context"
	"fmt"
	"path/filepath"
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
parent_scope=$4
case "$parent_scope" in identity|shared) ;; *) echo 'invalid artifact parent scope' >&2; exit 1;; esac
parent=${path%/*}
[ -n "$parent" ] || parent=/
parent_created=0
tmp=
pending_signal=0
arm_signals() {
  trap 'exit 129' HUP
  trap 'exit 130' INT
  trap 'exit 143' TERM
}
defer_signals() {
  trap 'pending_signal=129' HUP
  trap 'pending_signal=130' INT
  trap 'pending_signal=143' TERM
}
resume_signals() {
  code=$pending_signal
  pending_signal=0
  arm_signals
  if [ "$code" != 0 ]; then exit "$code"; fi
}
cleanup() {
  status=$?
  trap - EXIT
  trap '' HUP INT TERM
  [ -z "$tmp" ] || rm -f "$tmp"
  if [ "$parent_scope" = identity ] && [ "$parent_created" = 1 ]; then
    rm -f "$parent/.alpineform-owned"
    rmdir "$parent" 2>/dev/null || true
  fi
  exit "$status"
}
trap cleanup EXIT
arm_signals
if [ ! -e "$parent" ]; then
  parent_created=1
  defer_signals
  mkdir -p "$parent"
  resume_signals
fi
if [ ! -d "$parent" ] || [ -L "$parent" ]; then
  echo 'artifact cache parent is not a directory' >&2
  exit 1
fi
if [ -d "$path" ]; then
  echo 'refusing to replace a directory with an artifact cache file' >&2
  exit 1
fi
if [ "$parent_scope" = identity ] && [ "$parent_created" = 1 ]; then
  printf '%s' 'alpineform-component-source-v1' >"$parent/.alpineform-owned"
  chmod 0600 "$parent/.alpineform-owned"
fi
defer_signals
tmp=$(mktemp "$parent/.alpineform-download.XXXXXX")
resume_signals
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
defer_signals
mv -f "$tmp" "$path"
tmp=
trap '' HUP INT TERM
trap - EXIT
exit 0
`

const componentProtectedSourceInspectScript = `set -eu
path=$1
if ! IFS= read -r want; then
  echo 'protected artifact verification input is missing' >&2
  exit 1
fi
if [ ! -e "$path" ]; then
  echo missing
  exit 0
fi
if [ ! -f "$path" ]; then
  echo other
  exit 0
fi
actual=$(sha256sum "$path" | awk '{print $1}')
if [ "$actual" = "$want" ]; then
  echo verified
else
  echo unverified
fi
`

const componentProtectedSourceApplyScript = `set -eu
path=$1
parent_scope=$2
case "$parent_scope" in identity|shared) ;; *) echo 'invalid artifact parent scope' >&2; exit 1;; esac
if ! IFS= read -r url || ! IFS= read -r want; then
  echo 'protected artifact input is missing' >&2
  exit 1
fi
parent=${path%/*}
[ -n "$parent" ] || parent=/
parent_created=0
tmp=
pending_signal=0
arm_signals() {
  trap 'exit 129' HUP
  trap 'exit 130' INT
  trap 'exit 143' TERM
}
defer_signals() {
  trap 'pending_signal=129' HUP
  trap 'pending_signal=130' INT
  trap 'pending_signal=143' TERM
}
resume_signals() {
  code=$pending_signal
  pending_signal=0
  arm_signals
  if [ "$code" != 0 ]; then exit "$code"; fi
}
cleanup() {
  status=$?
  trap - EXIT
  trap '' HUP INT TERM
  [ -z "$tmp" ] || rm -f "$tmp"
  if [ "$parent_scope" = identity ] && [ "$parent_created" = 1 ]; then
    rm -f "$parent/.alpineform-owned"
    rmdir "$parent" 2>/dev/null || true
  fi
  exit "$status"
}
trap cleanup EXIT
arm_signals
if [ ! -e "$parent" ]; then
  parent_created=1
  defer_signals
  mkdir -p "$parent"
  resume_signals
fi
if [ ! -d "$parent" ] || [ -L "$parent" ]; then
  echo 'artifact cache parent is not a directory' >&2
  exit 1
fi
if [ -d "$path" ]; then
  echo 'refusing to replace a directory with an artifact cache file' >&2
  exit 1
fi
if [ "$parent_scope" = identity ] && [ "$parent_created" = 1 ]; then
  printf '%s' 'alpineform-component-source-v1' >"$parent/.alpineform-owned"
  chmod 0600 "$parent/.alpineform-owned"
fi
defer_signals
tmp=$(mktemp "$parent/.alpineform-download.XXXXXX")
resume_signals
if ! printf '%s\n' "$url" | /usr/bin/wget -q -O "$tmp" --input-file=-; then
  echo 'protected artifact download failed' >&2
  exit 1
fi
actual=$(sha256sum "$tmp" | awk '{print $1}')
if [ "$actual" != "$want" ]; then
  echo 'protected artifact checksum mismatch' >&2
  exit 1
fi
chmod 0600 "$tmp"
defer_signals
mv -f "$tmp" "$path"
tmp=
trap '' HUP INT TERM
trap - EXIT
exit 0
`

const componentProtectedSourceDeleteScript = `set -eu
path=$1
parent=${path%/*}
[ -n "$parent" ] || parent=/
owned=0
marker="$parent/.alpineform-owned"
if [ ! -L "$marker" ] && [ -f "$marker" ]; then
  case "$(cat "$marker")" in alpineform-component-source-v1|alpineform-component-ca-marker-v1) owned=1;; esac
fi
rm -f "$path"
if [ "$owned" = 1 ]; then
  rm -f "$marker"
  rmdir "$parent" 2>/dev/null || true
fi
`

const componentPriorFileCleanupScript = `set -eu
path=$1
if [ -d "$path" ]; then
  echo 'refusing to clean a directory as a prior component file' >&2
  exit 1
fi
parent=${path%/*}
[ -n "$parent" ] || parent=/
owned=0
marker="$parent/.alpineform-owned"
if [ ! -L "$marker" ] && [ -f "$marker" ]; then
  case "$(cat "$marker")" in alpineform-component-source-v1|alpineform-component-ca-marker-v1) owned=1;; esac
fi
rm -f "$path"
if [ "$owned" = 1 ]; then
  rm -f "$marker"
  rmdir "$parent" 2>/dev/null || true
fi
`

const componentProtectedPriorFileCleanupScript = `set -eu
if ! IFS= read -r path; then
  echo 'protected prior component path is missing' >&2
  exit 1
fi
if [ -d "$path" ]; then
  echo 'refusing to clean a protected prior component directory as a file' >&2
  exit 1
fi
parent=${path%/*}
[ -n "$parent" ] || parent=/
owned=0
marker="$parent/.alpineform-owned"
if [ ! -L "$marker" ] && [ -f "$marker" ]; then
  case "$(cat "$marker")" in alpineform-component-source-v1|alpineform-component-ca-marker-v1) owned=1;; esac
fi
rm -f "$path"
if [ "$owned" = 1 ]; then
  rm -f "$marker"
  rmdir "$parent" 2>/dev/null || true
fi
`

const componentProtectedSourcePriorMigrateScript = `set -eu
current=$1
if ! IFS= read -r prior; then
  echo 'protected prior source path is missing' >&2
  exit 1
fi
if [ "$prior" = "$current" ]; then exit 0; fi
current_parent=${current%/*}
[ -n "$current_parent" ] || current_parent=/
prior_parent=${prior%/*}
[ -n "$prior_parent" ] || prior_parent=/
parent_created=0
moved=0
pending_signal=0
arm_signals() {
  trap 'exit 129' HUP
  trap 'exit 130' INT
  trap 'exit 143' TERM
}
defer_signals() {
  trap 'pending_signal=129' HUP
  trap 'pending_signal=130' INT
  trap 'pending_signal=143' TERM
}
resume_signals() {
  code=$pending_signal
  pending_signal=0
  arm_signals
  if [ "$code" != 0 ]; then exit "$code"; fi
}
cleanup() {
  status=$?
  trap - EXIT
  trap '' HUP INT TERM
  restore_failed=0
  if [ "$moved" = 1 ] && { [ -e "$current" ] || [ -L "$current" ]; }; then
    mkdir -p "$prior_parent" || restore_failed=1
    if [ "$restore_failed" = 0 ]; then
      if mv "$current" "$prior"; then moved=0; else restore_failed=1; fi
    fi
  fi
  if [ "$parent_created" = 1 ]; then
    rm -f "$current_parent/.alpineform-owned" || true
    rmdir "$current_parent" 2>/dev/null || true
  fi
  if [ "$restore_failed" = 1 ]; then
    echo 'protected source identity migration rollback failed' >&2
    exit 1
  fi
  exit "$status"
}
trap cleanup EXIT
arm_signals
if { [ -e "$prior" ] || [ -L "$prior" ]; } && { [ -e "$current" ] || [ -L "$current" ]; }; then
  echo 'protected source identity migration found both prior and current paths' >&2
  exit 1
fi
if [ ! -e "$prior" ] && [ ! -L "$prior" ]; then
  if [ -d "$current" ] || [ -L "$current" ]; then
    echo 'protected source identity migration current path is invalid' >&2
    exit 1
  fi
  trap '' HUP INT TERM
  trap - EXIT
  exit 0
fi
if [ ! -f "$prior" ] || [ -L "$prior" ]; then
  echo 'protected source identity migration prior path is invalid' >&2
  exit 1
fi
if [ ! -e "$current_parent" ]; then
  parent_created=1
  mkdir -p "$current_parent"
fi
if [ ! -d "$current_parent" ] || [ -L "$current_parent" ]; then
  echo 'protected source identity parent is invalid' >&2
  exit 1
fi
if [ "$parent_created" = 1 ]; then
  printf '%s' 'alpineform-component-source-v1' >"$current_parent/.alpineform-owned"
  chmod 0600 "$current_parent/.alpineform-owned"
fi
defer_signals
mv "$prior" "$current"
moved=1
resume_signals
prior_marker="$prior_parent/.alpineform-owned"
trap '' HUP INT TERM
if [ ! -L "$prior_marker" ] && [ -f "$prior_marker" ] && [ "$(cat "$prior_marker")" = 'alpineform-component-source-v1' ]; then
  if ! rm -f "$prior_marker"; then
    if [ -e "$prior_marker" ] || [ -L "$prior_marker" ]; then exit 1; fi
  fi
fi
rmdir "$prior_parent" 2>/dev/null || true
trap - EXIT
exit 0
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

const componentProtectedInstallInspectScript = `set -eu
path=$1
if ! IFS= read -r want; then
  echo 'protected component verification input is missing' >&2
  exit 1
fi
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
actual=$(sha256sum "$path" | awk '{print $1}')
if [ "$actual" = "$want" ]; then
  echo verified
else
  echo unverified
fi
`

const componentProtectedInstallApplyScript = `set -eu
cache=$1
path=$2
owner=$3
group=$4
mode=$5
if ! IFS= read -r want; then
  echo 'protected component verification input is missing' >&2
  exit 1
fi
if [ ! -f "$cache" ]; then
  echo 'verified artifact cache file is missing' >&2
  exit 1
fi
parent=${path%/*}
[ -n "$parent" ] || parent=/
tmp=
pending_signal=0
arm_signals() {
  trap 'exit 129' HUP
  trap 'exit 130' INT
  trap 'exit 143' TERM
}
defer_signals() {
  trap 'pending_signal=129' HUP
  trap 'pending_signal=130' INT
  trap 'pending_signal=143' TERM
}
resume_signals() {
  code=$pending_signal
  pending_signal=0
  arm_signals
  if [ "$code" != 0 ]; then exit "$code"; fi
}
cleanup() {
  status=$?
  trap - EXIT
  trap '' HUP INT TERM
  [ -z "$tmp" ] || rm -f "$tmp"
  exit "$status"
}
trap cleanup EXIT
arm_signals
mkdir -p "$parent"
if [ -d "$path" ]; then
  echo 'refusing to replace a directory with a component file' >&2
  exit 1
fi
defer_signals
tmp=$(mktemp "$parent/.alpineform-component.XXXXXX")
resume_signals
cp "$cache" "$tmp"
actual=$(sha256sum "$tmp" | awk '{print $1}')
if [ "$actual" != "$want" ]; then
  echo 'protected artifact checksum mismatch before install' >&2
  exit 1
fi
chown "$owner:$group" "$tmp"
chmod "$mode" "$tmp"
defer_signals
mv -f "$tmp" "$path"
tmp=
trap '' HUP INT TERM
trap - EXIT
exit 0
`

func inspectComponentSource(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	path, _, digest, protected, err := componentSourceValues(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	command := backend.Command{Name: "inspect.component_artifact_source", Script: componentSourceInspectScript, Arguments: []string{path}}
	if protected {
		command.Script = componentProtectedSourceInspectScript
		command.Stdin = []byte(digest + "\n")
		command.RedactStdin = true
		command.RedactOutput = true
	}
	output, err := runner.Run(ctx, command)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "" || lines[0] == "missing" {
		return engine.ObservedResource{}, nil
	}
	observed := cloneDesired(node.Desired)
	if protected {
		if len(lines) != 1 {
			return engine.ObservedResource{}, fmt.Errorf("inspect protected component artifact cache returned an invalid response")
		}
		switch lines[0] {
		case "other":
			observed["type"] = "other"
		case "verified":
			observed["verified"] = true
		case "unverified":
			observed["verified"] = false
		default:
			return engine.ObservedResource{}, fmt.Errorf("inspect protected component artifact cache returned an invalid response")
		}
		return engine.ObservedResource{Exists: true, Values: observed, Protected: true}, nil
	}
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

func applyComponentSource(ctx context.Context, runner backend.Runner, step engine.Step) (engine.ObservedResource, error) {
	node := step.Node
	path, url, digest, protected, err := componentSourceValues(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if url == "" {
		return engine.ObservedResource{}, fmt.Errorf("component artifact source has an empty URL")
	}
	command := backend.Command{
		Name: "apply.component_artifact_source", Script: componentSourceApplyScript,
		Arguments: []string{url, digest, path, "identity"}, RedactOutput: true,
	}
	if protected {
		command.Script = componentProtectedSourceApplyScript
		command.Arguments = []string{path, "identity"}
		command.Stdin = []byte(url + "\n" + digest + "\n")
		command.RedactStdin = true
	}
	_, err = runner.Run(ctx, command)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	observed, err := inspectComponentSource(ctx, runner, node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if !componentSourceObservationVerified(observed, digest, protected) {
		return engine.ObservedResource{}, fmt.Errorf("component artifact cache verification failed after apply")
	}
	if err := cleanupPriorComponentFile(ctx, runner, step, "prior_delete_path", "path", path, "cleanup.component_artifact_source_previous"); err != nil {
		return engine.ObservedResource{}, err
	}
	return observed, nil
}

func componentSourceObservationVerified(observed engine.ObservedResource, digest string, protected bool) bool {
	if !observed.Exists || stringValue(observed.Values, "type") != "" {
		return false
	}
	if protected {
		verified, _ := observed.Values["verified"].(bool)
		return verified
	}
	return strings.EqualFold(stringValue(observed.Values, "sha256"), digest)
}

func deleteComponentSource(ctx context.Context, runner backend.Runner, step engine.Step) error {
	path := componentDeletePath(step)
	if err := validateRemoteFilePath(path); err != nil {
		return err
	}
	command := backend.Command{Name: "delete.component_artifact_source", Script: componentProtectedSourceDeleteScript, Arguments: []string{path}}
	if stepIsProtected(step) {
		command.RedactOutput = true
	}
	_, err := runner.Run(ctx, command)
	return err
}

func inspectComponentInstall(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	path, digest, protected, err := componentInstallIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	command := backend.Command{Name: "inspect." + node.Kind, Script: componentInstallInspectScript, Arguments: []string{path}}
	if protected {
		command.Script = componentProtectedInstallInspectScript
		command.Stdin = []byte(digest + "\n")
		command.RedactStdin = true
		command.RedactOutput = true
	}
	output, err := runner.Run(ctx, command)
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
		return engine.ObservedResource{Exists: true, Values: observed, Protected: protected}, nil
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
	if protected {
		switch lines[6] {
		case "verified":
			observed["content_verified"] = true
		case "unverified":
			observed["content_verified"] = false
		default:
			return engine.ObservedResource{}, fmt.Errorf("inspect protected component install returned an invalid response")
		}
	} else {
		observed["content_sha256"] = strings.ToLower(lines[6])
	}
	return engine.ObservedResource{Exists: true, Values: observed, Protected: protected}, nil
}

func applyComponentInstall(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	path, digest, protected, err := componentInstallIdentity(node)
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
	command := backend.Command{
		Name: "apply." + node.Kind, Script: componentInstallApplyScript,
		Arguments: []string{cachePath, digest, path, owner, group, mode}, RedactOutput: true,
	}
	if protected {
		command.Script = componentProtectedInstallApplyScript
		command.Arguments = []string{cachePath, path, owner, group, mode}
		command.Stdin = []byte(digest + "\n")
		command.RedactStdin = true
	}
	_, err = runner.Run(ctx, command)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	observed, err := inspectComponentInstall(ctx, runner, node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if !componentInstallObservationVerified(observed, node) {
		return engine.ObservedResource{}, fmt.Errorf("component install verification failed after apply")
	}
	return observed, nil
}

func componentInstallObservationVerified(observed engine.ObservedResource, node graph.Node) bool {
	if !observed.Exists || stringValue(observed.Values, "type") != "" {
		return false
	}
	for _, name := range []string{"owner", "group", "mode"} {
		if stringValue(observed.Values, name) != stringValue(node.Desired, name) {
			return false
		}
	}
	_, digest, protected, err := componentInstallIdentity(node)
	if err != nil {
		return false
	}
	if protected {
		verified, _ := observed.Values["content_verified"].(bool)
		return verified
	}
	return strings.EqualFold(stringValue(observed.Values, "content_sha256"), digest)
}

func deleteComponentInstall(ctx context.Context, runner backend.Runner, step engine.Step) error {
	path := componentDeletePath(step)
	if err := validateRemoteFilePath(path); err != nil {
		return err
	}
	_, err := runner.Run(ctx, backend.Command{Name: "delete.component_install", Script: fileDeleteScript, Arguments: []string{path}, RedactOutput: stepIsProtected(step)})
	return err
}

const componentCAInspectMarkerScript = `set -eu
marker=$1
want=$2
if [ ! -L "$marker" ] && [ -f "$marker" ] && [ "$(cat "$marker")" = "$want" ]; then
  echo updated
else
  echo stale
fi
`

const componentCAUpdateScript = `set -eu
marker=$1
want=$2
rm -f "$marker"
if ! command -v update-ca-certificates >/dev/null 2>&1; then
  echo 'update-ca-certificates is required for CA certificate artifacts' >&2
  exit 1
fi
update-ca-certificates
parent=${marker%/*}
mkdir -p "$parent"
tmp=$(mktemp "$parent/.alpineform-ca.XXXXXX")
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
printf '%s' "$want" >"$tmp"
chmod 0600 "$tmp"
mv -f "$tmp" "$marker"
trap - EXIT HUP INT TERM
`

const componentCADeleteRefreshScript = `set -eu
marker=$1
parent=${marker%/*}
[ -n "$parent" ] || parent=/
owned=0
ownership_marker="$parent/.alpineform-owned"
if [ ! -L "$ownership_marker" ] && [ -f "$ownership_marker" ] && [ "$(cat "$ownership_marker")" = 'alpineform-component-ca-marker-v1' ]; then owned=1; fi
rm -f "$marker"
if ! command -v update-ca-certificates >/dev/null 2>&1; then
  echo 'update-ca-certificates is required for CA certificate artifacts' >&2
  exit 1
fi
update-ca-certificates
if [ "$owned" = 1 ]; then
  rm -f "$ownership_marker"
  rmdir "$parent" 2>/dev/null || true
fi
`

const componentProtectedCAApplyScript = `set -eu
cache=$1
path=$2
owner=$3
group=$4
mode=$5
marker=$6
marker_value=$7
if ! IFS= read -r want; then
  echo 'protected CA verification input is missing' >&2
  exit 1
fi
if [ ! -f "$cache" ]; then
  echo 'verified CA artifact cache file is missing' >&2
  exit 1
fi
parent=${path%/*}
[ -n "$parent" ] || parent=/
marker_parent=${marker%/*}
[ -n "$marker_parent" ] || marker_parent=/
candidate=
prior=
marker_tmp=
had_prior=0
installed=0
refresh_started=0
marker_cleared=0
marker_parent_created=0
success=0
pending_signal=0
arm_signals() {
  trap 'exit 129' HUP
  trap 'exit 130' INT
  trap 'exit 143' TERM
}
defer_signals() {
  trap 'pending_signal=129' HUP
  trap 'pending_signal=130' INT
  trap 'pending_signal=143' TERM
}
resume_signals() {
  code=$pending_signal
  pending_signal=0
  arm_signals
  if [ "$code" != 0 ]; then exit "$code"; fi
}
cleanup() {
  status=$?
  trap - EXIT
  trap '' HUP INT TERM
  cleanup_failed=0
  if [ -n "$candidate" ] && ! rm -f "$candidate"; then cleanup_failed=1; fi
  if [ -n "$marker_tmp" ] && ! rm -f "$marker_tmp"; then cleanup_failed=1; fi
  restore_failed=0
  if [ "$success" != 1 ]; then
    if [ "$marker_cleared" = 1 ] && ! rm -f "$marker"; then cleanup_failed=1; fi
    if [ "$had_prior" = 1 ] && [ -n "$prior" ] && { [ -e "$prior" ] || [ -L "$prior" ]; }; then
      if ! rm -f "$path"; then
        restore_failed=1
      elif mv "$prior" "$path"; then
        prior=
        had_prior=0
      else
        restore_failed=1
      fi
    elif [ "$installed" = 1 ]; then
      rm -f "$path" || restore_failed=1
    fi
    if [ "$installed" = 1 ] && [ "$refresh_started" = 1 ] && command -v update-ca-certificates >/dev/null 2>&1; then update-ca-certificates >/dev/null 2>&1 || true; fi
    if [ "$marker_parent_created" = 1 ]; then
      rm -f "$marker_parent/.alpineform-owned" || true
      rmdir "$marker_parent" 2>/dev/null || true
    fi
  fi
  if [ "$had_prior" != 1 ] && [ -n "$prior" ] && ! rm -f "$prior"; then cleanup_failed=1; fi
  if [ "$restore_failed" = 1 ] || [ "$cleanup_failed" = 1 ]; then
    echo 'protected CA transaction cleanup or rollback failed' >&2
    exit 1
  fi
  exit "$status"
}
trap cleanup EXIT
arm_signals
mkdir -p "$parent"
if [ -d "$path" ]; then
  echo 'refusing to replace a directory with a CA certificate' >&2
  exit 1
fi
defer_signals
candidate=$(mktemp "$parent/.alpineform-ca-candidate.XXXXXX")
resume_signals
cp "$cache" "$candidate"
actual=$(sha256sum "$candidate" | awk '{print $1}')
if [ "$actual" != "$want" ]; then
  echo 'protected CA artifact checksum mismatch before install' >&2
  exit 1
fi
chown "$owner:$group" "$candidate"
chmod "$mode" "$candidate"
defer_signals
prior=$(mktemp -d "$parent/.alpineform-ca-prior.XXXXXX")
rmdir "$prior"
resume_signals
if [ -e "$path" ] || [ -L "$path" ]; then
  defer_signals
  mv "$path" "$prior"
  had_prior=1
  resume_signals
fi
defer_signals
mv "$candidate" "$path"
candidate=
installed=1
resume_signals
marker_cleared=1
rm -f "$marker"
if ! command -v update-ca-certificates >/dev/null 2>&1; then
  echo 'update-ca-certificates is required for CA certificate artifacts' >&2
  exit 1
fi
refresh_started=1
if ! update-ca-certificates; then
  echo 'CA trust refresh failed' >&2
  exit 1
fi
if [ ! -e "$marker_parent" ]; then
  marker_parent_created=1
  mkdir -p "$marker_parent"
fi
if [ ! -d "$marker_parent" ] || [ -L "$marker_parent" ]; then
  echo 'CA trust marker parent is not a directory' >&2
  exit 1
fi
if [ "$marker_parent_created" = 1 ]; then
  printf '%s' 'alpineform-component-ca-marker-v1' >"$marker_parent/.alpineform-owned"
  chmod 0600 "$marker_parent/.alpineform-owned"
fi
defer_signals
marker_tmp=$(mktemp "$marker_parent/.alpineform-ca.XXXXXX")
resume_signals
printf '%s' "$marker_value" >"$marker_tmp"
chmod 0600 "$marker_tmp"
defer_signals
mv -f "$marker_tmp" "$marker"
marker_tmp=
if [ "$had_prior" = 1 ]; then
  if ! rm -f "$prior"; then
    if [ -e "$prior" ] || [ -L "$prior" ]; then exit 1; fi
  fi
  prior=
  had_prior=0
fi
success=1
trap '' HUP INT TERM
trap - EXIT
exit 0
`

const componentProtectedCAMarkerValue = "alpineform-protected-ca-v1"

const componentProtectedCAPriorMigrateScript = `set -eu
current=$1
if ! IFS= read -r prior; then
  echo 'protected prior CA marker path is missing' >&2
  exit 1
fi
if [ "$prior" = "$current" ]; then exit 0; fi
parent=${current%/*}
[ -n "$parent" ] || parent=/
tmp=
parent_created=0
current_created=0
prior_removed=0
pending_signal=0
arm_signals() {
  trap 'exit 129' HUP
  trap 'exit 130' INT
  trap 'exit 143' TERM
}
defer_signals() {
  trap 'pending_signal=129' HUP
  trap 'pending_signal=130' INT
  trap 'pending_signal=143' TERM
}
resume_signals() {
  code=$pending_signal
  pending_signal=0
  arm_signals
  if [ "$code" != 0 ]; then exit "$code"; fi
}
cleanup() {
  status=$?
  trap - EXIT
  trap '' HUP INT TERM
  [ -z "$tmp" ] || rm -f "$tmp" || true
  if [ "$current_created" = 1 ] && [ "$prior_removed" != 1 ]; then rm -f "$current" || true; fi
  if [ "$parent_created" = 1 ]; then
    rm -f "$parent/.alpineform-owned" || true
    rmdir "$parent" 2>/dev/null || true
  fi
  exit "$status"
}
trap cleanup EXIT
arm_signals
if { [ -e "$prior" ] || [ -L "$prior" ]; } && { [ -e "$current" ] || [ -L "$current" ]; }; then
  echo 'protected CA marker migration found both prior and current paths' >&2
  exit 1
fi
if [ ! -e "$prior" ] && [ ! -L "$prior" ]; then
  if [ -e "$current" ] || [ -L "$current" ]; then
    if [ -L "$current" ] || [ ! -f "$current" ] || [ "$(cat "$current")" != 'alpineform-protected-ca-stale-v1' ]; then
      echo 'protected CA marker migration current path is ambiguous' >&2
      exit 1
    fi
  fi
  trap '' HUP INT TERM
  trap - EXIT
  exit 0
fi
if [ ! -f "$prior" ] || [ -L "$prior" ]; then
  echo 'protected CA marker migration prior path is invalid' >&2
  exit 1
fi
if [ ! -e "$parent" ]; then
  parent_created=1
  mkdir -p "$parent"
fi
if [ ! -d "$parent" ] || [ -L "$parent" ]; then
  echo 'protected CA marker parent is invalid' >&2
  exit 1
fi
if [ "$parent_created" = 1 ]; then
  printf '%s' 'alpineform-component-ca-marker-v1' >"$parent/.alpineform-owned"
  chmod 0600 "$parent/.alpineform-owned"
fi
defer_signals
tmp=$(mktemp "$parent/.alpineform-ca.XXXXXX")
resume_signals
printf '%s' 'alpineform-protected-ca-stale-v1' >"$tmp"
chmod 0600 "$tmp"
defer_signals
mv -f "$tmp" "$current"
tmp=
current_created=1
resume_signals
defer_signals
if ! rm -f "$prior"; then
  if [ -e "$prior" ] || [ -L "$prior" ]; then exit 1; fi
fi
prior_removed=1
trap '' HUP INT TERM
trap - EXIT
exit 0
`

func inspectComponentCACertificate(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	observed, err := inspectComponentInstall(ctx, runner, node)
	if err != nil || !observed.Exists {
		return observed, err
	}
	marker := stringValue(node.Desired, "trust_marker")
	if err := validateRemoteFilePath(marker); err != nil {
		return engine.ObservedResource{}, fmt.Errorf("CA trust marker: %w", err)
	}
	markerValue, checksumProtected, err := componentCAMarkerValue(node, marker)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{
		Name: "inspect.component_ca_certificate_trust", Script: componentCAInspectMarkerScript,
		Arguments: []string{marker, markerValue}, RedactOutput: checksumProtected,
	})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if strings.TrimSpace(string(output)) != "updated" {
		observed.Values["trust_updated"] = false
	}
	return observed, nil
}

func applyComponentCACertificate(ctx context.Context, runner backend.Runner, step engine.Step) (engine.ObservedResource, error) {
	node := step.Node
	marker := stringValue(node.Desired, "trust_marker")
	if err := validateRemoteFilePath(marker); err != nil {
		return engine.ObservedResource{}, fmt.Errorf("CA trust marker: %w", err)
	}
	if strings.Contains(marker, "/protected/") {
		return applyProtectedCACertificate(ctx, runner, step, marker)
	}
	if _, err := applyComponentInstall(ctx, runner, node); err != nil {
		return engine.ObservedResource{}, err
	}
	markerValue, checksumProtected, err := componentCAMarkerValue(node, marker)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if _, err := runner.Run(ctx, backend.Command{
		Name: "apply.component_ca_certificate_trust", Script: componentCAUpdateScript,
		Arguments: []string{marker, markerValue}, RedactOutput: checksumProtected,
	}); err != nil {
		return engine.ObservedResource{}, err
	}
	observed, err := inspectComponentCACertificate(ctx, runner, node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if !componentCAObservationVerified(observed, node) {
		return engine.ObservedResource{}, fmt.Errorf("component CA certificate verification failed after trust refresh")
	}
	if err := cleanupPriorComponentFile(ctx, runner, step, "prior_trust_marker", "trust_marker", marker, "cleanup.component_ca_certificate_trust_previous"); err != nil {
		return engine.ObservedResource{}, err
	}
	return observed, nil
}

func applyProtectedCACertificate(ctx context.Context, runner backend.Runner, step engine.Step, marker string) (engine.ObservedResource, error) {
	node := step.Node
	path, digest, _, err := componentInstallIdentity(node)
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
		return engine.ObservedResource{}, fmt.Errorf("component CA install %q has invalid owner, group, or mode metadata", path)
	}
	_, err = runner.Run(ctx, backend.Command{
		Name: "apply.component_ca_certificate", Script: componentProtectedCAApplyScript,
		Arguments: []string{cachePath, path, owner, group, mode, marker, componentProtectedCAMarkerValue},
		Stdin:     []byte(digest + "\n"), RedactStdin: true, RedactOutput: true,
	})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	observed, err := inspectComponentCACertificate(ctx, runner, node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if !componentCAObservationVerified(observed, node) {
		return engine.ObservedResource{}, fmt.Errorf("protected component CA certificate verification failed after trust refresh")
	}
	if err := cleanupPriorComponentFile(ctx, runner, step, "prior_trust_marker", "trust_marker", marker, "cleanup.component_ca_certificate_trust_previous"); err != nil {
		return engine.ObservedResource{}, err
	}
	return observed, nil
}

func componentCAObservationVerified(observed engine.ObservedResource, node graph.Node) bool {
	if !observed.Exists || stringValue(observed.Values, "type") != "" {
		return false
	}
	trustUpdated, _ := observed.Values["trust_updated"].(bool)
	if !trustUpdated {
		return false
	}
	_, digest, protected, err := componentInstallIdentity(node)
	if err != nil {
		return false
	}
	if protected {
		verified, _ := observed.Values["content_verified"].(bool)
		return verified
	}
	return strings.EqualFold(stringValue(observed.Values, "content_sha256"), digest)
}

func cleanupPriorComponentFile(ctx context.Context, runner backend.Runner, step engine.Step, payloadName, deleteName, currentPath, operation string) error {
	priorPath, protected, err := componentPriorCleanupPath(step, payloadName, deleteName)
	if err != nil {
		return err
	}
	if priorPath == "" || priorPath == currentPath {
		return nil
	}
	command := backend.Command{
		Name: operation, Script: componentPriorFileCleanupScript, Arguments: []string{priorPath}, RedactOutput: protected,
	}
	if protected {
		command.Script = componentProtectedPriorFileCleanupScript
		command.Arguments = nil
		command.Stdin = []byte(priorPath + "\n")
		command.RedactStdin = true
		command.RedactOutput = true
	}
	_, err = runner.Run(ctx, command)
	return err
}

func componentPriorCleanupPath(step engine.Step, payloadName, deleteName string) (string, bool, error) {
	protected := stepIsProtected(step)
	path := ""
	if raw, exists := step.Node.Payload[payloadName]; exists {
		protected = true
		value, ok := raw.(string)
		if !ok || value == "" {
			return "", true, fmt.Errorf("protected prior component cleanup path is invalid")
		}
		path = value
	} else if step.Prior != nil {
		if raw, exists := step.Prior.Delete[deleteName]; exists {
			value, ok := raw.(string)
			if !ok || value == "" {
				return "", protected, fmt.Errorf("prior component cleanup path is invalid")
			}
			path = value
		}
	}
	if path == "" {
		return "", protected, nil
	}
	if err := validateRemoteFilePath(path); err != nil {
		if protected {
			return "", true, fmt.Errorf("protected prior component cleanup path is invalid")
		}
		return "", false, fmt.Errorf("prior component cleanup path is invalid")
	}
	return path, protected, nil
}

func migrateProtectedComponentPrior(ctx context.Context, runner backend.Runner, step engine.Step) error {
	var currentPath, priorPath, script, operation string
	switch step.Node.Kind {
	case "component_artifact_source":
		currentPath = stringValue(step.Node.Desired, "path")
		var err error
		priorPath, _, err = componentPriorCleanupPath(step, "prior_delete_path", "path")
		if err != nil {
			return err
		}
		if priorPath == "" || priorPath == currentPath {
			return nil
		}
		if !validProtectedSourceMigrationPaths(currentPath, priorPath) {
			return fmt.Errorf("protected prior component identity is invalid")
		}
		script = componentProtectedSourcePriorMigrateScript
		operation = "migrate.component_artifact_source_previous"
	case "component_ca_certificate":
		currentPath = stringValue(step.Node.Desired, "trust_marker")
		var err error
		priorPath, _, err = componentPriorCleanupPath(step, "prior_trust_marker", "trust_marker")
		if err != nil {
			return err
		}
		if priorPath == "" || priorPath == currentPath {
			return nil
		}
		if !validProtectedCAMigrationPaths(currentPath, priorPath) {
			return fmt.Errorf("protected prior component identity is invalid")
		}
		script = componentProtectedCAPriorMigrateScript
		operation = "migrate.component_ca_certificate_trust_previous"
	default:
		return nil
	}
	_, err := runner.Run(ctx, backend.Command{
		Name: operation, Script: script, Arguments: []string{currentPath},
		Stdin: []byte(priorPath + "\n"), RedactStdin: true, RedactOutput: true,
	})
	return err
}

func validProtectedSourceMigrationPaths(currentPath, priorPath string) bool {
	if validateRemoteFilePath(currentPath) != nil || validateRemoteFilePath(priorPath) != nil || filepath.Base(currentPath) != "artifact" || filepath.Base(priorPath) != "artifact" {
		return false
	}
	currentLabel := filepath.Dir(currentPath)
	protectedRoot := filepath.Dir(currentLabel)
	componentRoot := filepath.Dir(protectedRoot)
	cacheRoot := filepath.Dir(componentRoot)
	priorIdentity := filepath.Dir(priorPath)
	return filepath.Base(protectedRoot) == "protected" &&
		cacheRoot == "/var/cache/alpineform/components" &&
		filepath.Dir(priorIdentity) == componentRoot &&
		componentProviderSHA256Pattern.MatchString(filepath.Base(priorIdentity))
}

func validProtectedCAMigrationPaths(currentPath, priorPath string) bool {
	if validateRemoteFilePath(currentPath) != nil || validateRemoteFilePath(priorPath) != nil {
		return false
	}
	protectedRoot := filepath.Dir(currentPath)
	componentRoot := filepath.Dir(protectedRoot)
	markerRoot := filepath.Dir(componentRoot)
	currentName := filepath.Base(currentPath)
	priorName := filepath.Base(priorPath)
	if !strings.HasSuffix(currentName, ".updated") || strings.TrimSuffix(currentName, ".updated") == "" || !strings.HasSuffix(priorName, ".updated") {
		return false
	}
	digest := strings.TrimSuffix(priorName, ".updated")
	return filepath.Base(protectedRoot) == "protected" &&
		markerRoot == "/var/lib/alpineform/ca-certificates" &&
		filepath.Dir(priorPath) == markerRoot &&
		componentProviderSHA256Pattern.MatchString(digest)
}

func deleteComponentCACertificate(ctx context.Context, runner backend.Runner, step engine.Step) error {
	if err := deleteComponentInstall(ctx, runner, step); err != nil {
		return err
	}
	marker := componentDeleteValue(step, "trust_marker")
	if err := validateRemoteFilePath(marker); err != nil {
		return fmt.Errorf("CA trust marker: %w", err)
	}
	_, err := runner.Run(ctx, backend.Command{
		Name: "delete.component_ca_certificate_trust", Script: componentCADeleteRefreshScript,
		Arguments: []string{marker}, RedactOutput: stepIsProtected(step),
	})
	return err
}

func componentCAMarkerValue(node graph.Node, marker string) (string, bool, error) {
	_, digest, checksumProtected, err := componentInstallIdentity(node)
	if err != nil {
		return "", false, err
	}
	if checksumProtected || strings.Contains(marker, "/protected/") {
		return componentProtectedCAMarkerValue, checksumProtected, nil
	}
	return digest, false, nil
}

func componentSourceValues(node graph.Node) (string, string, string, bool, error) {
	path := stringValue(node.Desired, "path")
	if err := validateRemoteFilePath(path); err != nil {
		return "", "", "", false, err
	}
	url, urlProtected, err := componentArtifactField(node, "url")
	if err != nil {
		return "", "", "", false, err
	}
	digest, digestProtected, err := componentArtifactField(node, "sha256")
	if err != nil {
		return "", "", "", false, err
	}
	digest = strings.ToLower(digest)
	if !componentProviderSHA256Pattern.MatchString(digest) {
		return "", "", "", false, fmt.Errorf("component artifact source has invalid SHA-256 metadata")
	}
	return path, url, digest, urlProtected || digestProtected, nil
}

func componentInstallIdentity(node graph.Node) (string, string, bool, error) {
	path := stringValue(node.Desired, "path")
	if err := validateRemoteFilePath(path); err != nil {
		return "", "", false, err
	}
	digest, protected, err := componentArtifactField(node, "content_sha256")
	if err != nil {
		return "", "", false, err
	}
	digest = strings.ToLower(digest)
	if !componentProviderSHA256Pattern.MatchString(digest) {
		return "", "", false, fmt.Errorf("component install has invalid SHA-256 metadata")
	}
	return path, digest, protected, nil
}

func componentArtifactField(node graph.Node, name string) (string, bool, error) {
	protected := false
	for _, suffix := range []string{"_sensitive", "_ephemeral"} {
		key := name + suffix
		value, exists := node.Desired[key]
		if !exists {
			continue
		}
		marked, ok := value.(bool)
		if !ok || !marked {
			return "", false, fmt.Errorf("component artifact field %q has invalid protection metadata", name)
		}
		protected = true
	}
	desiredValue, desiredExists := node.Desired[name]
	payloadValue, payloadExists := node.Payload[name]
	if protected {
		value, ok := payloadValue.(string)
		if desiredExists || !payloadExists || !ok || value == "" {
			return "", true, fmt.Errorf("component artifact field %q has invalid protected provider payload", name)
		}
		return value, true, nil
	}
	value, ok := desiredValue.(string)
	if payloadExists || !desiredExists || !ok || value == "" {
		return "", false, fmt.Errorf("component artifact field %q has invalid public provider metadata", name)
	}
	return value, false, nil
}

func componentDeletePath(step engine.Step) string {
	return componentDeleteValue(step, "path")
}

func componentDeleteValue(step engine.Step, name string) string {
	if step.Node.Desired != nil {
		if value := stringValue(step.Node.Desired, name); value != "" {
			return value
		}
	}
	if step.Prior != nil {
		value, _ := step.Prior.Delete[name].(string)
		return value
	}
	return ""
}

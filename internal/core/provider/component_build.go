package provider

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
	"github.com/mofelee/alpineform/internal/product"
)

var (
	componentBuildVirtualPackagePattern = regexp.MustCompile(`^\.alpineform-build-[a-f0-9]{24}$`)
	componentBuildOwnerPattern          = regexp.MustCompile(`^[a-f0-9]{32}$`)
	buildEnvironmentNamePattern         = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

const (
	componentBuildWorkspaceRootPayload = "workspace_root"
	componentBuildOutputCachePayload   = "output_cache"
)

const componentBuildInputWriteScript = `set -eu
path=$1
want=$2
parent=${path%/*}
mkdir -p "$parent"
if [ -d "$path" ] || [ -L "$path" ]; then
  echo 'refusing unsafe source-build input cache path' >&2
  exit 1
fi
tmp=$(mktemp "$parent/.alpineform-build-input.XXXXXX")
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
cat >"$tmp"
actual=$(sha256sum "$tmp" | awk '{print $1}')
if [ "$actual" != "$want" ]; then
  echo 'source-build input checksum mismatch' >&2
  exit 1
fi
chmod 0600 "$tmp"
mv -f "$tmp" "$path"
trap - EXIT HUP INT TERM
`

const componentBuildDependenciesInspectScript = `set -eu
` + componentBuildWorkspaceSafetyScript + `
virtual=$1
marker=$2
owner=$3
identity=$4
root=$5
workspace=$6
output_marker=$7
output_cache=$8
default_root=$9
shift 9
if ! valid_workspace_tuple "$root" "$workspace" "$identity" || ! valid_build_owner "$owner"; then
  echo 'invalid source-build dependency workspace metadata' >&2
  exit 1
fi
output_valid=false
if verified_build_output "$output_cache" "$output_marker" "$identity"; then output_valid=true; fi
if [ -L /etc/apk/world ] || { [ -e /etc/apk/world ] && [ ! -f /etc/apk/world ]; }; then
  echo 'refusing unsafe APK world path during source-build dependency inspection' >&2
  exit 1
fi
installed=false
if apk info -e "$virtual" >/dev/null 2>&1; then installed=true; fi
owned=false
marker_identity=
marker_matches=false
if [ -e "$marker" ] || [ -L "$marker" ]; then
  if ! load_dependency_workspace "$marker" "$virtual" "$owner" "$default_root"; then
    echo 'source-build dependency marker collides with another owner or unsafe workspace' >&2
    exit 1
  fi
  owned=true
  marker_identity=$dependency_identity
  if [ "$dependency_identity" = "$identity" ] && [ "$dependency_root" = "$root" ] && [ "$dependency_workspace" = "$workspace" ]; then marker_matches=true; fi
fi
if [ "$installed" = true ] && [ "$owned" != true ]; then
  echo 'source-build virtual package collides with unowned APK state' >&2
  exit 1
fi
if { [ -e "$marker" ] || [ -L "$marker" ]; } && [ "$owned" != true ]; then
  echo 'source-build dependency marker collides with another owner' >&2
  exit 1
fi
if [ "$output_valid" = true ]; then
  echo satisfied
  exit 0
fi
if [ "$installed" != true ]; then
  if [ "$#" -eq 0 ] && [ "$owned" = true ] && [ "$marker_matches" = true ]; then echo active; exit 0; fi
  echo missing
  exit 0
fi
world=false
if [ -f /etc/apk/world ] && awk -v virtual="$virtual" '$0 == virtual || index($0, virtual "=") == 1 { found=1 } END { exit !found }' /etc/apk/world; then world=true; fi
packages_ok=true
for package in "$@"; do
  if ! apk info -e "$package" >/dev/null 2>&1; then packages_ok=false; fi
done
if [ "$marker_matches" = true ] && [ "$world" = true ] && [ "$packages_ok" = true ]; then
  echo active
else
  printf 'stale\n%s\n' "$marker_identity"
fi
`

const componentBuildDependenciesApplyScript = `set -eu
` + componentBuildWorkspaceSafetyScript + `
virtual=$1
marker=$2
owner=$3
identity=$4
root=$5
workspace=$6
default_root=$7
shift 7
if ! valid_workspace_tuple "$root" "$workspace" "$identity" || ! valid_build_owner "$owner"; then
  echo 'invalid source-build dependency workspace metadata' >&2
  exit 1
fi
begin_owned_build_runtime_transaction "$owner"
stop_owned_build_runtime_locked "$owner"
if [ -L /etc/apk/world ] || { [ -e /etc/apk/world ] && [ ! -f /etc/apk/world ]; }; then
  echo 'refusing unsafe APK world path during source-build dependency apply' >&2
  exit 1
fi
if [ -e "$marker" ] || [ -L "$marker" ]; then
  if ! load_dependency_workspace "$marker" "$virtual" "$owner" "$default_root"; then
    echo 'refusing source-build dependency marker owned by another resource' >&2
    exit 1
  fi
  remove_dependency_workspace "$owner"
fi
if [ -f /etc/apk/world ] && awk -v virtual="$virtual" '$0 == virtual || index($0, virtual "=") == 1 { found=1 } END { exit !found }' /etc/apk/world && [ ! -f "$marker" ]; then
  echo 'refusing to adopt unowned source-build virtual package world intent' >&2
  exit 1
fi
if apk info -e "$virtual" >/dev/null 2>&1; then
  if [ ! -f "$marker" ]; then
    echo 'refusing to adopt an unowned source-build virtual package' >&2
    exit 1
  fi
  apk --quiet del "$virtual"
fi
finish_owned_build_runtime_locked "$owner" "$runtime_transaction_generation"
release_owned_build_runtime_lock "$owner"
parent=${marker%/*}
if ! no_symlink_boundaries "$parent"; then echo 'source-build dependency marker parent contains a symbolic link' >&2; exit 1; fi
mkdir -p "$parent"
if ! no_symlink_boundaries "$parent" || [ ! -d "$parent" ]; then echo 'source-build dependency marker parent is unsafe' >&2; exit 1; fi
tmp=$(mktemp "$parent/.alpineform-build-dependencies.XXXXXX")
success=0
cleanup() {
  operation_status=$?
  trap - EXIT HUP INT TERM
  cleanup_status=0
  rm -f "$tmp" || cleanup_status=1
  if [ "$success" != 1 ]; then
    if apk info -e "$virtual" >/dev/null 2>&1; then
      if ! apk --quiet del "$virtual" >/dev/null 2>&1; then cleanup_status=1; fi
    fi
    if [ "$cleanup_status" = 0 ]; then rm -f "$marker" || cleanup_status=1; fi
  fi
  if [ "$operation_status" -ne 0 ]; then exit "$operation_status"; fi
  if [ "$cleanup_status" -ne 0 ]; then exit 1; fi
}
trap cleanup EXIT
trap 'exit 130' HUP INT TERM
printf '%s\n%s\n%s\n%s\n%s\n' "$virtual" "$owner" "$identity" "$root" "$workspace" >"$tmp"
chmod 0600 "$tmp"
mv -f "$tmp" "$marker"
if [ "$#" -gt 0 ]; then apk --quiet add --virtual "$virtual" "$@"; fi
if [ "$#" -gt 0 ] && { [ ! -f /etc/apk/world ] || ! awk -v virtual="$virtual" '$0 == virtual || index($0, virtual "=") == 1 { found=1 } END { exit !found }' /etc/apk/world; }; then
  echo 'source-build virtual package was not recorded in APK world' >&2
  exit 1
fi
for package in "$@"; do
  if ! apk info -e "$package" >/dev/null 2>&1; then echo 'source-build dependency is not installed after apk add' >&2; exit 1; fi
done
success=1
trap - EXIT HUP INT TERM
`

const componentBuildWorkspaceSafetyScript = `
workspace_uid=0
build_runtime_root=` + product.ComponentBuildRuntimeRoot + `
build_runtime_lock_root=` + product.ComponentBuildRuntimeLockRoot + `
runtime_lock_owner=
valid_build_identity() {
  [ "${#1}" -eq 64 ] || return 1
  case "$1" in *[!a-f0-9]*) return 1;; esac
}
valid_build_owner() {
  [ "${#1}" -eq 32 ] || return 1
  case "$1" in *[!a-f0-9]*) return 1;; esac
}
valid_workspace_root() {
  case "$1" in /*) ;; *) return 1;; esac
  case "$1" in /|*/|*//*|*/./*|*/../*|*/.|*/..) return 1;; esac
}
valid_workspace_tuple() {
  valid_workspace_root "$1" && valid_build_identity "$3" && [ "$2" = "$1/$3" ]
}
verified_build_output() {
  apf_output_cache=$1
  apf_output_marker=$2
  apf_output_identity=$3
  [ -f "$apf_output_cache" ] && [ ! -L "$apf_output_cache" ] || return 1
  [ -f "$apf_output_marker" ] && [ ! -L "$apf_output_marker" ] || return 1
  [ "$(sed -n '1p' "$apf_output_marker")" = "$apf_output_identity" ] || return 1
  apf_output_want=$(sed -n '2p' "$apf_output_marker")
  [ -n "$apf_output_want" ] || return 1
  apf_output_actual=$(sha256sum "$apf_output_cache" | awk '{print $1}')
  [ "$apf_output_actual" = "$apf_output_want" ]
}
no_symlink_boundaries() {
  apf_probe=$1
  while [ "$apf_probe" != / ]; do
    [ ! -L "$apf_probe" ] || return 1
    apf_probe=${apf_probe%/*}
    [ -n "$apf_probe" ] || apf_probe=/
  done
  [ "$(stat -c '%u' /)" = 0 ] || return 1
  apf_permissions=$(stat -c '%A' /)
  if [ "$(printf '%s' "$apf_permissions" | cut -c6)" = w ] || [ "$(printf '%s' "$apf_permissions" | cut -c9)" = w ]; then
    case "$apf_permissions" in *t|*T) ;; *) return 1;; esac
  fi
}
safe_workspace_ancestors() {
  apf_selected_root=$1
  apf_probe=$apf_selected_root
  while [ "$apf_probe" != / ]; do
    if [ -L "$apf_probe" ]; then return 1; fi
    if [ -e "$apf_probe" ]; then
      apf_uid=$(stat -c '%u' "$apf_probe")
      apf_permissions=$(stat -c '%A' "$apf_probe")
      if [ "$apf_probe" = "$apf_selected_root" ]; then
        [ "$apf_uid" = "$workspace_uid" ] || return 1
        [ "$(printf '%s' "$apf_permissions" | cut -c6)" != w ] || return 1
        [ "$(printf '%s' "$apf_permissions" | cut -c9)" != w ] || return 1
      else
        [ "$apf_uid" = 0 ] || [ "$apf_uid" = "$workspace_uid" ] || return 1
        if [ "$(printf '%s' "$apf_permissions" | cut -c6)" = w ] || [ "$(printf '%s' "$apf_permissions" | cut -c9)" = w ]; then
          case "$apf_permissions" in *t|*T) ;; *) return 1;; esac
        fi
      fi
    fi
    apf_probe=${apf_probe%/*}
    [ -n "$apf_probe" ] || apf_probe=/
  done
}
private_workspace_root() {
  valid_workspace_root "$1" || return 1
  safe_workspace_ancestors "$1" || return 1
  [ -d "$1" ] && [ ! -L "$1" ] || return 1
  [ "$(stat -c '%u' "$1")" = "$workspace_uid" ] || return 1
  apf_root_permissions=$(stat -c '%A' "$1")
  [ "$(printf '%s' "$apf_root_permissions" | cut -c6)" != w ] || return 1
  [ "$(printf '%s' "$apf_root_permissions" | cut -c9)" != w ]
}
owned_workspace_boundary() {
  apf_root=$1
  apf_workspace=$2
  apf_identity=$3
  apf_owner=$4
  valid_workspace_tuple "$apf_root" "$apf_workspace" "$apf_identity" || return 1
  valid_build_owner "$apf_owner" || return 1
  private_workspace_root "$apf_root" || return 1
  [ -d "$apf_workspace" ] && [ ! -L "$apf_workspace" ] || return 1
  [ "$(stat -c '%u' "$apf_workspace")" = "$workspace_uid" ] || return 1
  [ "$(stat -c '%a' "$apf_workspace")" = 700 ] || return 1
  apf_workspace_marker=$apf_workspace/.alpineform-build-owner
  [ -f "$apf_workspace_marker" ] && [ ! -L "$apf_workspace_marker" ] || return 1
  [ "$(stat -c '%u' "$apf_workspace_marker")" = "$workspace_uid" ] || return 1
  [ "$(stat -c '%a' "$apf_workspace_marker")" = 600 ] || return 1
  [ "$(wc -l <"$apf_workspace_marker" | tr -d ' ')" = 5 ] || return 1
  [ "$(sed -n '1p' "$apf_workspace_marker")" = APFWORKSPACE1 ] || return 1
  [ "$(sed -n '2p' "$apf_workspace_marker")" = "$apf_owner" ] || return 1
  [ "$(sed -n '3p' "$apf_workspace_marker")" = "$apf_identity" ] || return 1
  [ "$(sed -n '4p' "$apf_workspace_marker")" = "$apf_root" ] || return 1
  [ "$(sed -n '5p' "$apf_workspace_marker")" = "$apf_workspace" ] || return 1
}
owned_workspace() {
  owned_workspace_boundary "$1" "$2" "$3" "$4" || return 1
  apf_build=$2/build
  [ -d "$apf_build" ] && [ ! -L "$apf_build" ] || return 1
  [ "$(stat -c '%u' "$apf_build")" = "$workspace_uid" ] || return 1
  [ "$(stat -c '%a' "$apf_build")" = 700 ] || return 1
}
remove_owned_workspace() {
  apf_root=$1
  apf_workspace=$2
  apf_identity=$3
  apf_owner=$4
  if [ ! -e "$apf_workspace" ] && [ ! -L "$apf_workspace" ]; then return 0; fi
  if ! owned_workspace_boundary "$apf_root" "$apf_workspace" "$apf_identity" "$apf_owner"; then
    echo 'refusing to remove an unowned or unsafe source-build workspace' >&2
    return 1
  fi
  rm -rf -- "$apf_workspace"
  if [ -e "$apf_workspace" ] || [ -L "$apf_workspace" ]; then
    echo 'source-build workspace cleanup did not remove the owned path' >&2
    return 1
  fi
}
runtime_paths_for_owner() {
  apf_runtime_owner=$1
  valid_build_owner "$apf_runtime_owner" || return 1
  build_runtime_dir=$build_runtime_root/$apf_runtime_owner
  build_runtime_intent=$build_runtime_dir/intent
  build_runtime_marker=$build_runtime_dir/process
  build_runtime_lock=$build_runtime_lock_root/$apf_runtime_owner.lock
}
valid_build_runtime_generation() {
  case "$1" in ''|*[!0-9:]*|:*|*::*|*:) return 1;; esac
  apf_generation_pid=${1%%:*}
  apf_generation_start=${1#*:}
  [ "$apf_generation_pid" -gt 1 ] && [ "$apf_generation_start" -gt 0 ]
}
private_build_runtime_lock_root() {
  safe_workspace_ancestors "$build_runtime_lock_root" || return 1
  [ -d "$build_runtime_lock_root" ] && [ ! -L "$build_runtime_lock_root" ] || return 1
  [ "$(stat -c '%u' "$build_runtime_lock_root")" = "$workspace_uid" ] || return 1
  [ "$(stat -c '%a' "$build_runtime_lock_root")" = 700 ]
}
prepare_build_runtime_lock_root() {
  if [ -e "$build_runtime_lock_root" ] || [ -L "$build_runtime_lock_root" ]; then
    private_build_runtime_lock_root
    return
  fi
  safe_workspace_ancestors "$build_runtime_lock_root" || return 1
  (umask 077; mkdir -p "$build_runtime_lock_root")
  chmod 0700 "$build_runtime_lock_root"
  private_build_runtime_lock_root
}
acquire_owned_build_runtime_lock() {
  apf_lock_owner=$1
  runtime_paths_for_owner "$apf_lock_owner" || return 1
  if [ -n "$runtime_lock_owner" ]; then
    [ "$runtime_lock_owner" = "$apf_lock_owner" ] || return 1
    return 0
  fi
  if ! prepare_build_runtime_lock_root; then
    echo 'source-build runtime lock root is unowned or unsafe' >&2
    return 1
  fi
  if [ -e "$build_runtime_lock" ] || [ -L "$build_runtime_lock" ]; then
    if [ ! -f "$build_runtime_lock" ] || [ -L "$build_runtime_lock" ] ||
       [ "$(stat -c '%u' "$build_runtime_lock")" != "$workspace_uid" ] ||
       [ "$(stat -c '%a' "$build_runtime_lock")" != 600 ]; then
      echo 'source-build runtime lock is unowned or unsafe' >&2
      return 1
    fi
  else
    (umask 077; : >"$build_runtime_lock")
    chmod 0600 "$build_runtime_lock"
  fi
  exec 9>"$build_runtime_lock"
  for apf_lock_wait in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
    if flock -n 9; then
      apf_lock_fd_identity=$(stat -Lc '%d:%i:%u:%a' "/proc/$$/fd/9") || {
        exec 9>&-
        return 1
      }
      apf_lock_path_identity=$(stat -c '%d:%i:%u:%a' "$build_runtime_lock") || {
        exec 9>&-
        return 1
      }
      if [ "$apf_lock_fd_identity" != "$apf_lock_path_identity" ] ||
         [ ! -f "$build_runtime_lock" ] || [ -L "$build_runtime_lock" ]; then
        echo 'source-build runtime lock changed during acquisition' >&2
        exec 9>&-
        return 1
      fi
      runtime_lock_owner=$apf_lock_owner
      return 0
    fi
    sleep 1
  done
  echo 'timed out acquiring source-build runtime lock' >&2
  exec 9>&-
  return 1
}
release_owned_build_runtime_lock() {
  apf_unlock_owner=$1
  [ "$runtime_lock_owner" = "$apf_unlock_owner" ] || return 1
  runtime_lock_owner=
  exec 9>&-
}
capture_owned_build_runtime() {
  apf_capture_owner=$1
  runtime_paths_for_owner "$apf_capture_owner" || return 1
  captured_runtime_state=absent
  captured_runtime_identity=
  captured_runtime_generation=
  if [ ! -e "$build_runtime_dir" ] && [ ! -L "$build_runtime_dir" ]; then return 0; fi
  if ! owned_build_runtime_dir "$apf_capture_owner"; then
    if [ ! -e "$build_runtime_dir" ] && [ ! -L "$build_runtime_dir" ]; then return 0; fi
    echo 'source-build runtime changed through an unsafe boundary' >&2
    return 1
  fi
  captured_runtime_state=present
  if ! captured_runtime_identity=$(stat -c '%d:%i' "$build_runtime_dir"); then
    if [ ! -e "$build_runtime_dir" ] && [ ! -L "$build_runtime_dir" ]; then
      captured_runtime_state=absent
      return 0
    fi
    return 1
  fi
  if [ -e "$build_runtime_intent" ] || [ -L "$build_runtime_intent" ]; then
    if load_owned_build_runtime_intent "$apf_capture_owner"; then
      captured_runtime_generation=$build_runtime_generation
    fi
  fi
}
captured_owned_build_runtime_matches() {
  apf_captured_owner=$1
  runtime_paths_for_owner "$apf_captured_owner" || return 1
  captured_runtime_gone=false
  if [ "$captured_runtime_state" = absent ]; then
    [ ! -e "$build_runtime_dir" ] && [ ! -L "$build_runtime_dir" ]
    return
  fi
  if [ ! -e "$build_runtime_dir" ] && [ ! -L "$build_runtime_dir" ]; then
    captured_runtime_gone=true
    return 0
  fi
  owned_build_runtime_dir "$apf_captured_owner" || return 1
  [ "$(stat -c '%d:%i' "$build_runtime_dir")" = "$captured_runtime_identity" ] || return 1
  if [ -n "$captured_runtime_generation" ]; then
    load_owned_build_runtime_intent "$apf_captured_owner" || return 1
    [ "$build_runtime_generation" = "$captured_runtime_generation" ] || return 1
  fi
}
begin_owned_build_runtime_transaction() {
  apf_transaction_owner=$1
  capture_owned_build_runtime "$apf_transaction_owner" || return 1
  acquire_owned_build_runtime_lock "$apf_transaction_owner" || return 1
  if ! captured_owned_build_runtime_matches "$apf_transaction_owner"; then
    release_owned_build_runtime_lock "$apf_transaction_owner" || true
    echo 'source-build runtime generation changed while waiting for its lock' >&2
    return 1
  fi
  runtime_transaction_generation=$captured_runtime_generation
}
private_build_runtime_root() {
  safe_workspace_ancestors "$build_runtime_root" || return 1
  [ -d "$build_runtime_root" ] && [ ! -L "$build_runtime_root" ] || return 1
  [ "$(stat -c '%u' "$build_runtime_root")" = "$workspace_uid" ] || return 1
  [ "$(stat -c '%a' "$build_runtime_root")" = 700 ]
}
owned_build_runtime_dir() {
  runtime_paths_for_owner "$1" || return 1
  private_build_runtime_root || return 1
  [ -d "$build_runtime_dir" ] && [ ! -L "$build_runtime_dir" ] || return 1
  [ "$(stat -c '%u' "$build_runtime_dir")" = "$workspace_uid" ] || return 1
  [ "$(stat -c '%a' "$build_runtime_dir")" = 700 ]
}
prepare_owned_build_runtime() {
  apf_prepare_owner=$1
  apf_prepare_identity=$2
  apf_prepare_root=$3
  apf_prepare_workspace=$4
  runtime_paths_for_owner "$apf_prepare_owner" || return 1
  valid_workspace_tuple "$apf_prepare_root" "$apf_prepare_workspace" "$apf_prepare_identity" || return 1
  if [ -e "$build_runtime_dir" ] || [ -L "$build_runtime_dir" ]; then
    echo 'source-build runtime directory already exists' >&2
    return 1
  fi
  if [ -e "$build_runtime_root" ] || [ -L "$build_runtime_root" ]; then
    if ! private_build_runtime_root; then
      echo 'source-build runtime root is unowned or unsafe' >&2
      return 1
    fi
  else
    if ! safe_workspace_ancestors "$build_runtime_root"; then
      echo 'source-build runtime root has an unsafe symbolic-link or ownership boundary' >&2
      return 1
    fi
    mkdir -p "$build_runtime_root"
    chmod 0700 "$build_runtime_root"
    if ! private_build_runtime_root; then
      echo 'source-build runtime root is unowned or unsafe after creation' >&2
      return 1
    fi
  fi
  mkdir "$build_runtime_dir"
  chmod 0700 "$build_runtime_dir"
  if ! owned_build_runtime_dir "$apf_prepare_owner"; then
    echo 'source-build runtime directory is unowned or unsafe after creation' >&2
    return 1
  fi
  read_build_runtime_process_stat "$$" || return 1
  build_runtime_generation=$apf_stat_actual_pid:$apf_stat_start_time
  valid_build_runtime_generation "$build_runtime_generation" || return 1
  apf_runtime_intent_pending=$build_runtime_dir/intent.pending
  printf 'APFRUNTIME1\n%s\n%s\n%s\n%s\n%s\n' \
    "$build_runtime_generation" "$apf_prepare_owner" "$apf_prepare_identity" "$apf_prepare_root" "$apf_prepare_workspace" >"$apf_runtime_intent_pending"
  chmod 0600 "$apf_runtime_intent_pending"
  mv -f "$apf_runtime_intent_pending" "$build_runtime_intent"
  load_owned_build_runtime_intent "$apf_prepare_owner"
}
load_owned_build_runtime_intent() {
  apf_expected_runtime_owner=$1
  owned_build_runtime_dir "$apf_expected_runtime_owner" || return 1
  [ -f "$build_runtime_intent" ] && [ ! -L "$build_runtime_intent" ] || return 1
  [ "$(stat -c '%u' "$build_runtime_intent")" = "$workspace_uid" ] || return 1
  [ "$(stat -c '%a' "$build_runtime_intent")" = 600 ] || return 1
  [ "$(wc -l <"$build_runtime_intent" | tr -d ' ')" = 6 ] || return 1
  [ "$(sed -n '1p' "$build_runtime_intent")" = APFRUNTIME1 ] || return 1
  build_runtime_generation=$(sed -n '2p' "$build_runtime_intent")
  build_runtime_intent_owner=$(sed -n '3p' "$build_runtime_intent")
  build_runtime_identity=$(sed -n '4p' "$build_runtime_intent")
  build_runtime_workspace_root=$(sed -n '5p' "$build_runtime_intent")
  build_runtime_workspace=$(sed -n '6p' "$build_runtime_intent")
  valid_build_runtime_generation "$build_runtime_generation" || return 1
  [ "$build_runtime_intent_owner" = "$apf_expected_runtime_owner" ] || return 1
  valid_workspace_tuple "$build_runtime_workspace_root" "$build_runtime_workspace" "$build_runtime_identity"
}
load_owned_build_runtime() {
  apf_expected_runtime_owner=$1
  load_owned_build_runtime_intent "$apf_expected_runtime_owner" || return 1
  [ -f "$build_runtime_marker" ] && [ ! -L "$build_runtime_marker" ] || return 1
  [ "$(stat -c '%u' "$build_runtime_marker")" = "$workspace_uid" ] || return 1
  [ "$(stat -c '%a' "$build_runtime_marker")" = 600 ] || return 1
  [ "$(wc -l <"$build_runtime_marker" | tr -d ' ')" = 9 ] || return 1
  [ "$(sed -n '1p' "$build_runtime_marker")" = APFPROCESS1 ] || return 1
  [ "$(sed -n '2p' "$build_runtime_marker")" = "$build_runtime_generation" ] || return 1
  [ "$(sed -n '3p' "$build_runtime_marker")" = "$apf_expected_runtime_owner" ] || return 1
  [ "$(sed -n '4p' "$build_runtime_marker")" = "$build_runtime_identity" ] || return 1
  [ "$(sed -n '5p' "$build_runtime_marker")" = "$build_runtime_workspace_root" ] || return 1
  [ "$(sed -n '6p' "$build_runtime_marker")" = "$build_runtime_workspace" ] || return 1
  build_runtime_pid=$(sed -n '7p' "$build_runtime_marker")
  build_runtime_pgid=$(sed -n '8p' "$build_runtime_marker")
  build_runtime_start_time=$(sed -n '9p' "$build_runtime_marker")
  valid_workspace_tuple "$build_runtime_workspace_root" "$build_runtime_workspace" "$build_runtime_identity" || return 1
  for apf_runtime_number in "$build_runtime_pid" "$build_runtime_pgid" "$build_runtime_start_time"; do
    case "$apf_runtime_number" in ''|*[!0-9]*) return 1;; esac
  done
  [ "$build_runtime_pid" -gt 1 ] && [ "$build_runtime_pid" = "$build_runtime_pgid" ] && [ "$build_runtime_start_time" -gt 0 ]
}
read_build_runtime_process_stat() {
  apf_stat_pid=$1
  [ -r "/proc/$apf_stat_pid/stat" ] || return 1
  apf_stat_contents=$(cat "/proc/$apf_stat_pid/stat") || return 1
  [ -n "$apf_stat_contents" ] || return 1
  apf_stat_actual_pid=${apf_stat_contents%% *}
  apf_stat_tail=${apf_stat_contents##*) }
  [ "$apf_stat_tail" != "$apf_stat_contents" ] || return 1
  set -- $apf_stat_tail
  [ "$#" -ge 20 ] || return 1
  apf_stat_state=$1
  apf_stat_pgid=$3
  apf_stat_session=$4
  shift 19
  apf_stat_start_time=$1
}
build_runtime_group_exists() {
  for apf_group_stat in /proc/[0-9]*/stat; do
    [ -r "$apf_group_stat" ] || continue
    apf_group_pid=${apf_group_stat#/proc/}
    apf_group_pid=${apf_group_pid%/stat}
    if read_build_runtime_process_stat "$apf_group_pid" && [ "$apf_stat_pgid" = "$build_runtime_pgid" ]; then
      case "$apf_stat_state" in Z|X) ;; *) return 0;; esac
    fi
  done
  return 1
}
owned_build_runtime_process() {
  read_build_runtime_process_stat "$build_runtime_pid" || return 1
  [ "$(stat -c '%u' "/proc/$build_runtime_pid")" = "$workspace_uid" ] || return 1
  [ "$apf_stat_actual_pid" = "$build_runtime_pid" ] &&
    [ "$apf_stat_pgid" = "$build_runtime_pgid" ] &&
    [ "$apf_stat_session" = "$build_runtime_pid" ] &&
    [ "$apf_stat_start_time" = "$build_runtime_start_time" ]
}
build_runtime_cmdline_matches_intent() {
  apf_cmdline_pid=$1
  [ -r "/proc/$apf_cmdline_pid/cmdline" ] || return 1
  [ "$(stat -c '%u' "/proc/$apf_cmdline_pid")" = "$workspace_uid" ] || return 1
  apf_runtime_arguments=$(tr '\000' '\n' <"/proc/$apf_cmdline_pid/cmdline") || return 1
  apf_runtime_arg1=$(printf '%s\n' "$apf_runtime_arguments" | sed -n '1p')
  apf_runtime_arg2=$(printf '%s\n' "$apf_runtime_arguments" | sed -n '2p')
  apf_runtime_arg3=$(printf '%s\n' "$apf_runtime_arguments" | sed -n '3p')
  if [ "$apf_runtime_arg2" = "$build_runtime_dir/supervisor" ]; then
    apf_runtime_offset=2
  elif [ "$apf_runtime_arg3" = "$build_runtime_dir/supervisor" ]; then
    case "$apf_runtime_arg1" in setsid|*/setsid) ;; *) return 1;; esac
    apf_runtime_offset=3
  else
    return 1
  fi
  [ "$(printf '%s\n' "$apf_runtime_arguments" | sed -n "$((apf_runtime_offset + 1))p")" = "$build_runtime_marker" ] &&
    [ "$(printf '%s\n' "$apf_runtime_arguments" | sed -n "$((apf_runtime_offset + 2))p")" = "$build_runtime_generation" ] &&
    [ "$(printf '%s\n' "$apf_runtime_arguments" | sed -n "$((apf_runtime_offset + 3))p")" = "$build_runtime_intent_owner" ] &&
    [ "$(printf '%s\n' "$apf_runtime_arguments" | sed -n "$((apf_runtime_offset + 4))p")" = "$build_runtime_identity" ] &&
    [ "$(printf '%s\n' "$apf_runtime_arguments" | sed -n "$((apf_runtime_offset + 5))p")" = "$build_runtime_workspace_root" ] &&
    [ "$(printf '%s\n' "$apf_runtime_arguments" | sed -n "$((apf_runtime_offset + 6))p")" = "$build_runtime_workspace" ]
}
find_unrecorded_build_runtime_process() {
  apf_unrecorded_owner=$1
  load_owned_build_runtime_intent "$apf_unrecorded_owner" || return 2
  unrecorded_runtime_pid=
  for apf_runtime_cmdline in /proc/[0-9]*/cmdline; do
    [ -r "$apf_runtime_cmdline" ] || continue
    apf_runtime_candidate=${apf_runtime_cmdline#/proc/}
    apf_runtime_candidate=${apf_runtime_candidate%/cmdline}
    if build_runtime_cmdline_matches_intent "$apf_runtime_candidate"; then
      if [ -n "$unrecorded_runtime_pid" ]; then
        echo 'multiple unpublished source-build supervisors match one runtime' >&2
        return 2
      fi
      read_build_runtime_process_stat "$apf_runtime_candidate" || continue
      case "$apf_stat_state" in Z|X) continue;; esac
      unrecorded_runtime_pid=$apf_runtime_candidate
      unrecorded_runtime_pgid=$apf_stat_pgid
      unrecorded_runtime_session=$apf_stat_session
      unrecorded_runtime_start_time=$apf_stat_start_time
    fi
  done
  [ -n "$unrecorded_runtime_pid" ]
}
owned_unrecorded_build_runtime_process() {
  apf_unrecorded_check_pid=$unrecorded_runtime_pid
  read_build_runtime_process_stat "$apf_unrecorded_check_pid" || return 1
  [ "$apf_stat_actual_pid" = "$apf_unrecorded_check_pid" ] &&
    [ "$apf_stat_pgid" = "$unrecorded_runtime_pgid" ] &&
    [ "$apf_stat_session" = "$unrecorded_runtime_session" ] &&
    [ "$apf_stat_start_time" = "$unrecorded_runtime_start_time" ] &&
    build_runtime_cmdline_matches_intent "$apf_unrecorded_check_pid"
}
recover_stalled_build_runtime_publication() {
  apf_recover_owner=$1
  runtime_paths_for_owner "$apf_recover_owner" || return 1
  [ "$runtime_lock_owner" = "$apf_recover_owner" ] || return 1
  if [ -e "$build_runtime_marker" ] || [ -L "$build_runtime_marker" ]; then return 1; fi
  find_unrecorded_build_runtime_process "$apf_recover_owner" || return 1
  if [ "$unrecorded_runtime_pid" != "$unrecorded_runtime_pgid" ] ||
     [ "$unrecorded_runtime_pid" != "$unrecorded_runtime_session" ] ||
     ! owned_unrecorded_build_runtime_process; then
    echo 'refusing to stop unpublished source-build supervisor without session ownership' >&2
    return 1
  fi
  if ! kill -TERM "-$unrecorded_runtime_pgid" >/dev/null 2>&1 && owned_unrecorded_build_runtime_process; then
    echo 'failed to terminate unpublished source-build supervisor' >&2
    return 1
  fi
  for apf_runtime_wait in 1 2; do
    if ! owned_unrecorded_build_runtime_process; then return 0; fi
    sleep 1
  done
  if ! owned_unrecorded_build_runtime_process; then return 0; fi
  if ! kill -KILL "-$unrecorded_runtime_pgid" >/dev/null 2>&1 && owned_unrecorded_build_runtime_process; then
    echo 'failed to kill unpublished source-build supervisor' >&2
    return 1
  fi
  for apf_runtime_wait in 1 2 3; do
    if ! owned_unrecorded_build_runtime_process; then return 0; fi
    sleep 1
  done
  echo 'unpublished source-build supervisor survived bounded termination' >&2
  return 1
}
remove_incomplete_build_runtime_locked() {
  apf_incomplete_owner=$1
  runtime_paths_for_owner "$apf_incomplete_owner" || return 1
  [ "$runtime_lock_owner" = "$apf_incomplete_owner" ] || return 1
  owned_build_runtime_dir "$apf_incomplete_owner" || return 1
  if [ -e "$build_runtime_intent" ] || [ -L "$build_runtime_intent" ] ||
     [ -e "$build_runtime_marker" ] || [ -L "$build_runtime_marker" ]; then
    return 1
  fi
  apf_incomplete_intent=$build_runtime_dir/intent.pending
  if [ -e "$apf_incomplete_intent" ] || [ -L "$apf_incomplete_intent" ]; then
    if [ ! -f "$apf_incomplete_intent" ] || [ -L "$apf_incomplete_intent" ] ||
       [ "$(stat -c '%u' "$apf_incomplete_intent")" != "$workspace_uid" ] ||
       [ "$(stat -c '%a' "$apf_incomplete_intent")" != 600 ]; then
      echo 'refusing unsafe incomplete source-build runtime intent' >&2
      return 1
    fi
    rm -f -- "$apf_incomplete_intent" || return 1
  fi
  if find "$build_runtime_dir" -mindepth 1 -print -quit | grep -q .; then
    echo 'refusing incomplete source-build runtime containing unknown files' >&2
    return 1
  fi
  rmdir "$build_runtime_dir"
}
remove_build_runtime_secret_files() {
  apf_secret_owner=$1
  runtime_paths_for_owner "$apf_secret_owner" || return 1
  if [ ! -e "$build_runtime_dir" ] && [ ! -L "$build_runtime_dir" ]; then return 0; fi
  owned_build_runtime_dir "$apf_secret_owner" || return 1
  for apf_runtime_secret in "$build_runtime_dir/manifest" "$build_runtime_dir/stdin" "$build_runtime_dir/environment"; do
    if [ -d "$apf_runtime_secret" ] && [ ! -L "$apf_runtime_secret" ]; then
      echo 'refusing directory at source-build protected runtime path' >&2
      return 1
    fi
    rm -f -- "$apf_runtime_secret" || return 1
  done
}
stop_owned_build_runtime_locked() {
  apf_stop_owner=$1
  runtime_paths_for_owner "$apf_stop_owner" || return 1
  [ "$runtime_lock_owner" = "$apf_stop_owner" ] || return 1
  if [ ! -e "$build_runtime_dir" ] && [ ! -L "$build_runtime_dir" ]; then return 0; fi
  if ! owned_build_runtime_dir "$apf_stop_owner"; then
    echo 'refusing to stop an unowned or unsafe source-build runtime' >&2
    return 1
  fi
  if [ ! -e "$build_runtime_intent" ] && [ ! -L "$build_runtime_intent" ]; then
    if ! remove_incomplete_build_runtime_locked "$apf_stop_owner"; then
      echo 'refusing to remove incomplete source-build runtime' >&2
      return 1
    fi
    return 0
  fi
  if [ ! -e "$build_runtime_marker" ] && [ ! -L "$build_runtime_marker" ]; then
    apf_unrecorded_status=0
    find_unrecorded_build_runtime_process "$apf_stop_owner" || apf_unrecorded_status=$?
    if [ "$apf_unrecorded_status" -eq 0 ]; then
      if ! recover_stalled_build_runtime_publication "$apf_stop_owner"; then
        echo 'refusing to remove source-build runtime while an unpublished supervisor is active' >&2
        return 1
      fi
    elif [ "$apf_unrecorded_status" -gt 1 ]; then
      echo 'refusing to remove source-build runtime with invalid launch metadata' >&2
      return 1
    fi
    if [ ! -e "$build_runtime_marker" ] && [ ! -L "$build_runtime_marker" ]; then
      remove_build_runtime_secret_files "$apf_stop_owner"
      return
    fi
  fi
  if ! load_owned_build_runtime "$apf_stop_owner"; then
    echo 'refusing to stop source-build process without valid runtime ownership metadata' >&2
    return 1
  fi
  if owned_build_runtime_process; then
    if ! kill -TERM "-$build_runtime_pgid" >/dev/null 2>&1 && build_runtime_group_exists; then
      echo 'failed to terminate owned source-build process group' >&2
      return 1
    fi
    for apf_runtime_wait in 1 2 3; do
      if ! build_runtime_group_exists; then break; fi
      sleep 1
    done
    if build_runtime_group_exists; then
      if ! owned_build_runtime_process; then
        echo 'refusing to kill source-build process group after its leader identity changed' >&2
        return 1
      fi
      if ! kill -KILL "-$build_runtime_pgid" >/dev/null 2>&1 && build_runtime_group_exists; then
        echo 'failed to kill owned source-build process group' >&2
        return 1
      fi
      for apf_runtime_wait in 1 2 3 4 5; do
        if ! build_runtime_group_exists; then break; fi
        sleep 1
      done
    fi
  elif build_runtime_group_exists; then
    echo 'refusing to signal a leaderless or reused source-build process group' >&2
    return 1
  fi
  if build_runtime_group_exists; then
    echo 'owned source-build process group survived bounded termination' >&2
    return 1
  fi
  remove_build_runtime_secret_files "$apf_stop_owner"
}
finish_owned_build_runtime_locked() {
  apf_finish_owner=$1
  apf_finish_generation=${2:-}
  runtime_paths_for_owner "$apf_finish_owner" || return 1
  [ "$runtime_lock_owner" = "$apf_finish_owner" ] || return 1
  if [ ! -e "$build_runtime_dir" ] && [ ! -L "$build_runtime_dir" ]; then return 0; fi
  if ! owned_build_runtime_dir "$apf_finish_owner"; then
    echo 'refusing to remove an unowned or unsafe source-build runtime' >&2
    return 1
  fi
  if [ ! -e "$build_runtime_intent" ] && [ ! -L "$build_runtime_intent" ]; then
    if ! remove_incomplete_build_runtime_locked "$apf_finish_owner"; then
      echo 'refusing to remove incomplete source-build runtime' >&2
      return 1
    fi
    return 0
  fi
  if ! load_owned_build_runtime_intent "$apf_finish_owner"; then
    echo 'refusing to remove source-build runtime with invalid launch intent' >&2
    return 1
  fi
  if [ -n "$apf_finish_generation" ] && [ "$build_runtime_generation" != "$apf_finish_generation" ]; then
    return 0
  fi
  apf_saved_runtime_intent=$(cat "$build_runtime_intent") || return 1
  apf_saved_runtime_marker=
  if [ -e "$build_runtime_marker" ] || [ -L "$build_runtime_marker" ]; then
    if ! load_owned_build_runtime "$apf_finish_owner"; then
      echo 'refusing to remove source-build runtime with invalid ownership metadata' >&2
      return 1
    fi
    if build_runtime_group_exists; then
      echo 'refusing to remove source-build runtime while its process group is active' >&2
      return 1
    fi
    apf_saved_runtime_marker=$(cat "$build_runtime_marker") || return 1
  fi
  remove_build_runtime_secret_files "$apf_finish_owner" || return 1
  for apf_runtime_file in "$build_runtime_dir/supervisor" "$build_runtime_dir/intent.pending" "$build_runtime_dir/intent.restore" "$build_runtime_dir/process.pending" "$build_runtime_dir/process.ready.pending" "$build_runtime_dir/process.ready" "$build_runtime_dir/process.recover" "$build_runtime_dir/process.restore"; do
    if [ -d "$apf_runtime_file" ] && [ ! -L "$apf_runtime_file" ]; then
      echo 'refusing directory at source-build runtime metadata path' >&2
      return 1
    fi
    rm -f -- "$apf_runtime_file" || return 1
  done
  if find "$build_runtime_dir" -mindepth 1 ! -path "$build_runtime_intent" ! -path "$build_runtime_marker" -print -quit | grep -q .; then
    echo 'refusing to remove source-build runtime containing unknown files' >&2
    return 1
  fi
  rm -f -- "$build_runtime_marker" "$build_runtime_intent" || return 1
  if ! rmdir "$build_runtime_dir"; then
    if owned_build_runtime_dir "$apf_finish_owner"; then
      if [ ! -e "$build_runtime_intent" ] && [ ! -L "$build_runtime_intent" ]; then
        apf_restore_intent=$build_runtime_dir/intent.restore
        if printf '%s\n' "$apf_saved_runtime_intent" >"$apf_restore_intent" && chmod 0600 "$apf_restore_intent"; then
          mv -f "$apf_restore_intent" "$build_runtime_intent" || true
        fi
      fi
      if [ -n "$apf_saved_runtime_marker" ] && [ ! -e "$build_runtime_marker" ] && [ ! -L "$build_runtime_marker" ]; then
        apf_restore_marker=$build_runtime_dir/process.restore
        if printf '%s\n' "$apf_saved_runtime_marker" >"$apf_restore_marker" && chmod 0600 "$apf_restore_marker"; then
          mv -f "$apf_restore_marker" "$build_runtime_marker" || true
        fi
      fi
    fi
    echo 'source-build runtime directory cleanup did not remove the owned path' >&2
    return 1
  fi
}
legacy_dependency_workspace() {
  apf_root=$1
  apf_workspace=$2
  apf_identity=$3
  valid_workspace_tuple "$apf_root" "$apf_workspace" "$apf_identity" || return 1
  safe_workspace_ancestors "$apf_root" || return 1
  no_symlink_boundaries "$apf_workspace" || return 1
  [ -d "$apf_workspace" ] && [ ! -L "$apf_workspace" ] || return 1
  [ "$(stat -c '%u' "$apf_workspace")" = "$workspace_uid" ] || return 1
  [ "$(stat -c '%a' "$apf_workspace")" = 700 ]
}
remove_legacy_dependency_workspace() {
  apf_root=$1
  apf_workspace=$2
  apf_identity=$3
  if [ ! -e "$apf_workspace" ] && [ ! -L "$apf_workspace" ]; then return 0; fi
  legacy_dependency_workspace "$apf_root" "$apf_workspace" "$apf_identity" || return 1
  rm -rf -- "$apf_workspace"
  [ ! -e "$apf_workspace" ] && [ ! -L "$apf_workspace" ]
}
remove_dependency_workspace() {
  if [ "$dependency_lines" = 3 ]; then
    if ! remove_legacy_dependency_workspace "$dependency_root" "$dependency_workspace" "$dependency_identity"; then
      echo 'refusing to remove an unsafe legacy source-build workspace' >&2
      return 1
    fi
  else
    apf_dependency_owner_marker=$dependency_workspace/.alpineform-build-owner
    if { [ -e "$dependency_workspace" ] || [ -L "$dependency_workspace" ]; } && [ ! -e "$apf_dependency_owner_marker" ] && [ ! -L "$apf_dependency_owner_marker" ]; then
      if ! remove_legacy_dependency_workspace "$dependency_root" "$dependency_workspace" "$dependency_identity"; then
        echo 'refusing to remove an unsafe interrupted source-build workspace' >&2
        return 1
      fi
    else
      remove_owned_workspace "$dependency_root" "$dependency_workspace" "$dependency_identity" "$1"
    fi
  fi
}
load_dependency_workspace() {
  apf_dependency_marker=$1
  apf_virtual=$2
  apf_expected_owner=$3
  apf_default_root=$4
  [ -f "$apf_dependency_marker" ] && [ ! -L "$apf_dependency_marker" ] || return 1
  [ "$(stat -c '%u' "$apf_dependency_marker")" = "$workspace_uid" ] || return 1
  [ "$(stat -c '%a' "$apf_dependency_marker")" = 600 ] || return 1
  [ "$(sed -n '1p' "$apf_dependency_marker")" = "$apf_virtual" ] || return 1
  [ "$(sed -n '2p' "$apf_dependency_marker")" = "$apf_expected_owner" ] || return 1
  dependency_identity=$(sed -n '3p' "$apf_dependency_marker")
  valid_build_identity "$dependency_identity" || return 1
  dependency_lines=$(wc -l <"$apf_dependency_marker" | tr -d ' ')
  case "$dependency_lines" in
    3)
      dependency_root=$apf_default_root
      dependency_workspace=$dependency_root/$dependency_identity
      ;;
    5)
      dependency_root=$(sed -n '4p' "$apf_dependency_marker")
      dependency_workspace=$(sed -n '5p' "$apf_dependency_marker")
      ;;
    *) return 1;;
  esac
  valid_workspace_tuple "$dependency_root" "$dependency_workspace" "$dependency_identity"
}
`

const componentBuildWorkspaceInspectScript = `set -eu
` + componentBuildWorkspaceSafetyScript + `
root=$1
workspace=$2
identity=$3
owner=$4
output_marker=$5
output_cache=$6
output=$7
virtual=$8
dependency_marker=$9
default_root=${10}
if verified_build_output "$output_cache" "$output_marker" "$identity"; then
  echo satisfied
  exit 0
fi
if [ ! -e "$workspace" ] && [ ! -L "$workspace" ]; then echo missing; exit 0; fi
if ! owned_workspace "$root" "$workspace" "$identity" "$owner"; then
  if owned_workspace_boundary "$root" "$workspace" "$identity" "$owner"; then
    echo missing
    exit 0
  fi
  workspace_owner_marker=$workspace/.alpineform-build-owner
  if [ -e "$dependency_marker" ] && load_dependency_workspace "$dependency_marker" "$virtual" "$owner" "$default_root" && [ "$dependency_identity" = "$identity" ] && [ "$dependency_root" = "$root" ] && [ "$dependency_workspace" = "$workspace" ] && [ ! -e "$workspace_owner_marker" ] && [ ! -L "$workspace_owner_marker" ] && legacy_dependency_workspace "$dependency_root" "$dependency_workspace" "$dependency_identity"; then
    echo missing
    exit 0
  fi
  echo unsafe
  exit 0
fi
build=$workspace/build
if [ -f "$build/.alpineform-build-ready" ] && [ ! -L "$build/.alpineform-build-ready" ] && [ "$(cat "$build/.alpineform-build-ready")" = "$identity" ] && [ -e "$build/$output" ]; then
  echo active
  exit 0
fi
echo missing
`

const componentBuildWorkspacePrepareScript = `set -eu
` + componentBuildWorkspaceSafetyScript + `
root=$1
workspace=$2
identity=$3
owner=$4
virtual=$5
dependency_marker=$6
default_root=$7
working=$8
shift 8
if ! valid_workspace_tuple "$root" "$workspace" "$identity" || ! valid_build_owner "$owner"; then
  echo 'invalid source-build workspace ownership metadata' >&2
  exit 1
fi
if ! load_dependency_workspace "$dependency_marker" "$virtual" "$owner" "$default_root" || [ "$dependency_identity" != "$identity" ] || [ "$dependency_root" != "$root" ] || [ "$dependency_workspace" != "$workspace" ]; then
  echo 'source-build dependency marker does not own the selected workspace' >&2
  exit 1
fi
begin_owned_build_runtime_transaction "$owner"
stop_owned_build_runtime_locked "$owner"
if ! safe_workspace_ancestors "$root"; then echo 'source-build workspace root has an unsafe ownership, mode, or symbolic-link boundary' >&2; exit 1; fi
umask 077
mkdir -p -- "$root"
if ! safe_workspace_ancestors "$root" || [ ! -d "$root" ]; then echo 'source-build workspace root is unsafe' >&2; exit 1; fi
if ! private_workspace_root "$root"; then echo 'source-build workspace root is not private and root-owned' >&2; exit 1; fi
remove_dependency_workspace "$owner"
finish_owned_build_runtime_locked "$owner" "$runtime_transaction_generation"
release_owned_build_runtime_lock "$owner"
workspace_created=false
cleanup_partial_workspace() {
  operation_status=$?
  trap - EXIT HUP INT TERM
  cleanup_status=0
  if [ "$operation_status" -ne 0 ] && [ "$workspace_created" = true ] && private_workspace_root "$root" && [ -d "$workspace" ] && [ ! -L "$workspace" ] && [ "$(stat -c '%u' "$workspace")" = "$workspace_uid" ] && [ "$(stat -c '%a' "$workspace")" = 700 ]; then
    rm -rf -- "$workspace" || cleanup_status=1
  fi
  if [ "$operation_status" -eq 0 ] && [ "$cleanup_status" -ne 0 ]; then exit 1; fi
  exit "$operation_status"
}
trap cleanup_partial_workspace EXIT
trap 'exit 130' HUP INT TERM
mkdir "$workspace"
workspace_created=true
chmod 0700 "$workspace"
marker_tmp=$workspace/.alpineform-build-owner.tmp
printf 'APFWORKSPACE1\n%s\n%s\n%s\n%s\n' "$owner" "$identity" "$root" "$workspace" >"$marker_tmp"
chmod 0600 "$marker_tmp"
mv -f "$marker_tmp" "$workspace/.alpineform-build-owner"
mkdir "$workspace/build"
chmod 0700 "$workspace/build"
build=$workspace/build
while [ "$#" -gt 0 ]; do
  if [ "$#" -lt 5 ]; then echo 'invalid source-build input manifest' >&2; exit 1; fi
  cache=$1
  destination=$2
  want=$3
  format=$4
  strip=$5
  shift 5
  if [ ! -f "$cache" ] || [ -L "$cache" ]; then echo 'verified source-build input is missing or unsafe' >&2; exit 1; fi
  actual=$(sha256sum "$cache" | awk '{print $1}')
  if [ "$actual" != "$want" ]; then echo 'source-build input checksum changed before execution' >&2; exit 1; fi
  target="$build/$destination"
  parent=${target%/*}
  mkdir -p "$parent"
  if [ -z "$format" ]; then
    cp "$cache" "$target"
    chmod 0600 "$target"
    continue
  fi
  if [ "$format" != tar.gz ]; then echo 'unsupported source-build input archive format' >&2; exit 1; fi
  staging=$(mktemp -d "$build/.alpineform-build-extract.XXXXXX")
  manifest="$staging.archive.list"
  stripped="$staging.stripped.list"
  tar -tzf "$cache" >"$manifest"
  if [ ! -s "$manifest" ]; then echo 'source-build input archive contains no entries' >&2; exit 1; fi
  if [ "$(wc -l <"$manifest" | tr -d ' ')" -gt 100000 ]; then echo 'source-build input archive has too many entries' >&2; exit 1; fi
  while IFS= read -r entry; do
    if [ -z "$entry" ]; then echo 'source-build input archive contains an empty path' >&2; exit 1; fi
    case "$entry" in
      -*|/*|..|../*|*/..|*/../*) echo 'source-build input archive contains an unsafe path' >&2; exit 1;;
      *[[:space:]\\:]*) echo 'source-build input archive paths containing whitespace, backslash, or colon are unsupported' >&2; exit 1;;
    esac
  done <"$manifest"
  if tar -tvzf "$cache" | awk '{print substr($1,1,1)}' | grep -qvE '^[-d]$'; then
    echo 'source-build input archive links and special entries are forbidden' >&2
    exit 1
  fi
  awk -v strip="$strip" '
    {
      n = split($0, part, "/")
      if (part[n] == "") n--
      if (n <= strip) next
      out = part[strip + 1]
      for (i = strip + 2; i <= n; i++) out = out "/" part[i]
      print out
    }
  ' "$manifest" | LC_ALL=C sort >"$stripped"
  if [ ! -s "$stripped" ]; then echo 'source-build input archive has no entries after strip_components' >&2; exit 1; fi
  if uniq -d "$stripped" | grep -q .; then echo 'source-build input archive entries collide after strip_components' >&2; exit 1; fi
  tar -xzf "$cache" -C "$staging" --strip-components "$strip"
  rm -f "$manifest" "$stripped"
  if find "$staging" -type l -print -quit | grep -q . || find "$staging" ! -type f ! -type d -print -quit | grep -q .; then
    echo 'source-build input extraction produced a link or special entry' >&2
    exit 1
  fi
  line_count=$(find "$staging" -mindepth 1 -print | wc -l | tr -d ' ')
  nul_count=$(find "$staging" -mindepth 1 -print0 | tr -cd '\000' | wc -c | tr -d ' ')
  if [ "$line_count" != "$nul_count" ] || [ "$nul_count" = 0 ]; then echo 'source-build input extraction produced unsafe or no entries' >&2; exit 1; fi
  mv "$staging" "$target"
done
case "$working" in .) ;; *) mkdir -p "$build/$working";; esac
trap - EXIT HUP INT TERM
`

const componentBuildCommandScript = `set -eu
` + componentBuildWorkspaceSafetyScript + `
root=$1
workspace=$2
identity=$3
owner=$4
working=$5
shift 5
if ! owned_workspace "$root" "$workspace" "$identity" "$owner"; then echo 'source-build workspace is missing, unowned, or unsafe' >&2; exit 1; fi
build=$workspace/build
case "$working" in .) directory=$build;; *) directory=$build/$working;; esac
if [ ! -d "$directory" ] || [ -L "$directory" ]; then echo 'source-build working directory is missing or unsafe' >&2; exit 1; fi
physical=$(cd -P "$directory" && pwd)
case "$physical" in "$build"|"$build"/*) ;; *) echo 'source-build working directory escapes workspace' >&2; exit 1;; esac
pid=
runtime_generation=
cleanup_runtime() {
  operation_status=$?
  trap - EXIT HUP INT TERM
  cleanup_status=0
  runtime_stop_succeeded=false
  if [ "$runtime_lock_owner" != "$owner" ]; then
    if ! acquire_owned_build_runtime_lock "$owner"; then cleanup_status=1; fi
  fi
  if [ "$runtime_lock_owner" = "$owner" ]; then
    if [ -e "$build_runtime_dir" ] || [ -L "$build_runtime_dir" ]; then
      if [ -n "$runtime_generation" ] &&
         { ! load_owned_build_runtime_intent "$owner" || [ "$build_runtime_generation" != "$runtime_generation" ]; }; then
        cleanup_status=1
      elif stop_owned_build_runtime_locked "$owner"; then
        runtime_stop_succeeded=true
      else
        cleanup_status=1
      fi
    else
      runtime_stop_succeeded=true
    fi
    if [ "$runtime_stop_succeeded" = true ]; then
      if [ -n "$pid" ]; then
        wait "$pid" >/dev/null 2>&1 || true
        pid=
      fi
      if ! finish_owned_build_runtime_locked "$owner" "$runtime_generation"; then cleanup_status=1; fi
    fi
    if ! release_owned_build_runtime_lock "$owner"; then cleanup_status=1; fi
  fi
  if [ "$operation_status" -ne 0 ]; then exit "$operation_status"; fi
  if [ "$cleanup_status" -ne 0 ]; then exit 1; fi
}
trap cleanup_runtime EXIT
trap 'exit 130' HUP INT TERM
umask 077
if ! command -v flock >/dev/null 2>&1; then
  echo 'source builds require flock on the managed target' >&2
  exit 1
fi
acquire_owned_build_runtime_lock "$owner"
prepare_owned_build_runtime "$owner" "$identity" "$root" "$workspace"
runtime_generation=$build_runtime_generation
manifest=$build_runtime_dir/manifest
stdin_file=$build_runtime_dir/stdin
env_names=$build_runtime_dir/environment
supervisor=$build_runtime_dir/supervisor
: >"$manifest"
: >"$stdin_file"
: >"$env_names"
chmod 0600 "$manifest" "$stdin_file" "$env_names"
cat >"$manifest"
exec 3<"$manifest"
IFS= read -r magic <&3 || true
if [ "$magic" != APFBUILD1 ]; then echo 'invalid protected build manifest' >&2; exit 1; fi
IFS= read -r stdin_encoded <&3 || true
printf '%s' "$stdin_encoded" | base64 -d >"$stdin_file"
chmod 0600 "$stdin_file"
env | sed 's/=.*//' >"$env_names"
while IFS= read -r inherited; do
  case "$inherited" in ''|*[!A-Za-z0-9_]*) ;; *) unset "$inherited" || true;; esac
done <"$env_names"
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export HOME=/workspace TMPDIR=/tmp LC_ALL=C LANG=C TZ=UTC SOURCE_DATE_EPOCH=0 USER=root LOGNAME=root
while IFS="$(printf '\t')" read -r name encoded <&3; do
  case "$name" in [A-Za-z_][A-Za-z0-9_]*) ;; *) echo 'invalid protected build environment key' >&2; exit 1;; esac
  value=$(printf '%s' "$encoded" | base64 -d)
  export "$name=$value"
done
exec 3<&-
rm -f "$manifest" "$env_names"
if ! command -v bwrap >/dev/null 2>&1 || ! command -v setsid >/dev/null 2>&1; then
  echo 'source builds require bubblewrap and setsid on the managed target' >&2
  exit 1
fi
case "$working" in .) sandbox_directory=/workspace;; *) sandbox_directory=/workspace/$working;; esac
ulimit -c 0
ulimit -f 2097152
cat >"$supervisor" <<'APF_BUILD_SUPERVISOR'
#!/bin/sh
set -eu
marker=$1
generation=$2
owner=$3
identity=$4
root=$5
workspace=$6
workspace_uid=$7
shift 7
cancelled=false
child=
child_start_time=
read_supervisor_process_stat() {
  supervisor_stat_pid=$1
  [ -r "/proc/$supervisor_stat_pid/stat" ] || return 1
  supervisor_stat_contents=$(cat "/proc/$supervisor_stat_pid/stat") || return 1
  supervisor_stat_actual_pid=${supervisor_stat_contents%% *}
  supervisor_stat_fields=${supervisor_stat_contents##*) }
  [ "$supervisor_stat_fields" != "$supervisor_stat_contents" ] || return 1
  set -- $supervisor_stat_fields
  [ "$#" -ge 20 ] || return 1
  supervisor_stat_state=$1
  supervisor_stat_pgid=$3
  supervisor_stat_session=$4
  shift 19
  supervisor_stat_start_time=$1
}
owned_supervisor_child() {
  [ -n "$child" ] && [ -n "$child_start_time" ] || return 1
  read_supervisor_process_stat "$child" || return 1
  [ "$supervisor_stat_actual_pid" = "$child" ] &&
    [ "$supervisor_stat_pgid" = "$$" ] &&
    [ "$supervisor_stat_session" = "$$" ] &&
    [ "$supervisor_stat_start_time" = "$child_start_time" ]
}
cancel_build() {
  cancelled=true
  if owned_supervisor_child; then kill -TERM "$child" >/dev/null 2>&1 || true; fi
}
trap cancel_build HUP INT TERM
read_supervisor_process_stat "$$"
process_pid=$supervisor_stat_actual_pid
process_pgid=$supervisor_stat_pgid
process_session=$supervisor_stat_session
process_start_time=$supervisor_stat_start_time
[ "$process_pid" = "$$" ]
[ "$process_pgid" = "$$" ]
[ "$process_session" = "$$" ]
case "$process_start_time" in ''|*[!0-9]*) exit 1;; esac
pending=${marker%/*}/process.pending
ready=${marker%/*}/process.ready
printf 'APFPROCESS1\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n' \
  "$generation" "$owner" "$identity" "$root" "$workspace" "$process_pid" "$process_pgid" "$process_start_time" >"$pending"
chmod 0600 "$pending"
mv -f "$pending" "$marker"
while [ ! -e "$ready" ] && [ ! -L "$ready" ]; do
  if [ "$cancelled" = true ]; then exit 130; fi
  sleep 0.1
done
if [ ! -f "$ready" ] || [ -L "$ready" ] ||
   [ "$(stat -c '%u' "$ready")" != "$workspace_uid" ] ||
   [ "$(stat -c '%a' "$ready")" != 600 ] ||
   [ "$(cat "$ready")" != "$generation" ]; then
  exit 1
fi
rm -f -- "$ready"
if [ "$cancelled" = true ]; then exit 130; fi
supervisor_group_has_live_member() {
  for supervisor_stat_path in /proc/[0-9]*/stat; do
    [ -r "$supervisor_stat_path" ] || continue
    supervisor_stat=$(cat "$supervisor_stat_path") || continue
    supervisor_member_pid=${supervisor_stat%% *}
    supervisor_fields=${supervisor_stat##*) }
    [ "$supervisor_fields" != "$supervisor_stat" ] || continue
    set -- $supervisor_fields
    [ "$#" -ge 4 ] || continue
    supervisor_state=$1
    supervisor_pgid=$3
    if [ "$supervisor_member_pid" != "$$" ] && [ "$supervisor_pgid" = "$$" ]; then
      case "$supervisor_state" in Z|X) ;; *) return 0;; esac
    fi
  done
  return 1
}
"$@" &
child=$!
if read_supervisor_process_stat "$child" &&
   [ "$supervisor_stat_actual_pid" = "$child" ] &&
   [ "$supervisor_stat_pgid" = "$$" ] &&
   [ "$supervisor_stat_session" = "$$" ]; then
  child_start_time=$supervisor_stat_start_time
fi
if [ "$cancelled" = true ]; then cancel_build; fi
command_status=0
while [ -n "$child" ]; do
  if wait "$child"; then
    command_status=0
    child=
    child_start_time=
  else
    command_status=$?
    if ! owned_supervisor_child; then
      child=
      child_start_time=
    fi
  fi
done
while supervisor_group_has_live_member; do sleep 1; done
if [ "$cancelled" = true ]; then exit 130; fi
exit "$command_status"
APF_BUILD_SUPERVISOR
chmod 0700 "$supervisor"
setsid sh "$supervisor" "$build_runtime_marker" "$runtime_generation" "$owner" "$identity" "$root" "$workspace" "$workspace_uid" bwrap \
  --die-with-parent \
  --unshare-pid \
  --unshare-ipc \
  --unshare-uts \
  --unshare-cgroup-try \
  --unshare-net \
  --cap-drop ALL \
  --ro-bind /bin /bin \
  --ro-bind /sbin /sbin \
  --ro-bind /lib /lib \
  --ro-bind /usr /usr \
  --dev /dev \
  --proc /proc \
  --tmpfs /tmp \
  --dir /etc \
  --dir /run \
  --dir /var \
  --dir /var/tmp \
  --bind "$build" /workspace \
  --chdir "$sandbox_directory" \
  -- "$@" <"$stdin_file" >/dev/null 2>&1 9>&- &
pid=$!
runtime_ready=false
for runtime_wait in 1 2 3 4 5; do
  if [ -e "$build_runtime_marker" ] || [ -L "$build_runtime_marker" ]; then
    runtime_ready=true
    break
  fi
  if ! kill -0 "$pid" >/dev/null 2>&1; then break; fi
  sleep 1
done
if [ "$runtime_ready" != true ] || ! load_owned_build_runtime "$owner" || [ "$build_runtime_pid" != "$pid" ] || ! owned_build_runtime_process; then
  echo 'source-build sandbox failed to publish valid process ownership metadata' >&2
  exit 1
fi
runtime_ready_pending=$build_runtime_dir/process.ready.pending
printf '%s\n' "$runtime_generation" >"$runtime_ready_pending"
chmod 0600 "$runtime_ready_pending"
mv -f "$runtime_ready_pending" "$build_runtime_dir/process.ready"
release_owned_build_runtime_lock "$owner"
if ! wait "$pid"; then
  pid=
  echo 'source-build command failed; command output omitted' >&2
  exit 1
fi
pid=
`

const componentBuildWorkspaceReadyScript = `set -eu
` + componentBuildWorkspaceSafetyScript + `
root=$1
workspace=$2
identity=$3
owner=$4
output=$5
if ! owned_workspace "$root" "$workspace" "$identity" "$owner"; then
  echo 'source-build workspace is missing, unowned, or unsafe' >&2
  exit 1
fi
build=$workspace/build
if [ ! -e "$build/$output" ]; then
  echo 'source-build command completed without the declared output' >&2
  exit 1
fi
tmp=$(mktemp "$build/.alpineform-build-ready.XXXXXX")
printf '%s' "$identity" >"$tmp"
chmod 0600 "$tmp"
mv -f "$tmp" "$build/.alpineform-build-ready"
`

const componentBuildOutputInspectScript = `set -eu
cache=$1
marker=$2
identity=$3
if [ ! -f "$cache" ] || [ -L "$cache" ] || [ ! -f "$marker" ] || [ -L "$marker" ]; then echo missing; exit 0; fi
if [ "$(sed -n '1p' "$marker")" != "$identity" ]; then echo stale; exit 0; fi
want=$(sed -n '2p' "$marker")
actual=$(sha256sum "$cache" | awk '{print $1}')
if [ "$actual" != "$want" ]; then echo stale; exit 0; fi
echo verified
`

const componentBuildOutputApplyScript = `set -eu
` + componentBuildWorkspaceSafetyScript + `
root=$1
workspace=$2
identity=$3
owner=$4
output=$5
expected=$6
max_bytes=$7
cache=$8
marker=$9
executable=${10}
if ! owned_workspace "$root" "$workspace" "$identity" "$owner"; then echo 'source-build workspace is missing, unowned, or unsafe' >&2; exit 1; fi
build=$workspace/build
source=$build/$output
old_ifs=$IFS
IFS=/
set -- $output
IFS=$old_ifs
probe=$build
for part in "$@"; do
  probe=$probe/$part
  if [ -L "$probe" ]; then echo 'source-build output path contains a symbolic link' >&2; exit 1; fi
done
if [ -L "$source" ] || [ ! -f "$source" ]; then echo 'source-build output must be one regular non-symbolic-link file' >&2; exit 1; fi
if [ "$executable" = true ] && [ ! -x "$source" ]; then echo 'source-build output is not executable' >&2; exit 1; fi
size=$(stat -c '%s' "$source")
if [ "$size" -gt "$max_bytes" ]; then echo 'source-build output exceeds the declared size limit' >&2; exit 1; fi
actual=$(sha256sum "$source" | awk '{print $1}')
if [ -n "$expected" ] && [ "$actual" != "$expected" ]; then echo 'source-build output checksum mismatch' >&2; exit 1; fi
parent=${cache%/*}
mkdir -p "$parent"
if [ -L "$cache" ] || [ -d "$cache" ]; then echo 'unsafe source-build output cache path' >&2; exit 1; fi
tmp=$(mktemp "$parent/.alpineform-build-output.XXXXXX")
marker_tmp=$(mktemp "$parent/.alpineform-build-output-marker.XXXXXX")
cleanup() { rm -f "$tmp" "$marker_tmp"; }
trap cleanup EXIT HUP INT TERM
cp "$source" "$tmp"
copied=$(sha256sum "$tmp" | awk '{print $1}')
if [ "$copied" != "$actual" ]; then echo 'source-build output changed while staging' >&2; exit 1; fi
chmod 0600 "$tmp"
printf '%s\n%s\n%s\n' "$identity" "$actual" "$size" >"$marker_tmp"
chmod 0600 "$marker_tmp"
mv -fT "$tmp" "$cache"
mv -fT "$marker_tmp" "$marker"
trap - EXIT HUP INT TERM
`

const componentBuildCleanupInspectScript = `set -eu
` + componentBuildWorkspaceSafetyScript + `
root=$1
workspace=$2
virtual=$3
dependency_marker=$4
output_marker=$5
identity=$6
owner=$7
default_root=$8
if [ ! -f "$output_marker" ] || [ "$(sed -n '1p' "$output_marker")" != "$identity" ]; then echo missing; exit 0; fi
if ! valid_workspace_tuple "$root" "$workspace" "$identity" || ! valid_build_owner "$owner"; then echo unsafe; exit 0; fi
runtime_paths_for_owner "$owner"
if [ -e "$build_runtime_dir" ] || [ -L "$build_runtime_dir" ]; then
  if ! owned_build_runtime_dir "$owner"; then echo unsafe; exit 0; fi
  echo pending
  exit 0
fi
if [ -e "$dependency_marker" ] || [ -L "$dependency_marker" ]; then
  if ! load_dependency_workspace "$dependency_marker" "$virtual" "$owner" "$default_root"; then echo unsafe; exit 0; fi
  echo pending
  exit 0
fi
if [ -e "$workspace" ] || [ -L "$workspace" ] || apk info -e "$virtual" >/dev/null 2>&1; then echo pending; exit 0; fi
shift 8
for protected_path in "$@"; do if [ -e "$protected_path" ] || [ -L "$protected_path" ]; then echo pending; exit 0; fi; done
echo clean
`

const componentBuildCleanupScript = `set -eu
` + componentBuildWorkspaceSafetyScript + `
root=$1
workspace=$2
virtual=$3
marker=$4
owner=$5
identity=$6
default_root=$7
shift 7
if ! valid_workspace_tuple "$root" "$workspace" "$identity" || ! valid_build_owner "$owner"; then
  echo 'invalid source-build workspace cleanup metadata' >&2
  exit 1
fi
begin_owned_build_runtime_transaction "$owner"
stop_owned_build_runtime_locked "$owner"
marker_owned=false
if [ -e "$marker" ] || [ -L "$marker" ]; then
  if ! load_dependency_workspace "$marker" "$virtual" "$owner" "$default_root"; then
    echo 'refusing to clean unowned source-build dependency state' >&2
    exit 1
  fi
  marker_owned=true
  remove_dependency_workspace "$owner"
  if apk info -e "$virtual" >/dev/null 2>&1; then apk --quiet del "$virtual"; fi
elif apk info -e "$virtual" >/dev/null 2>&1; then
  echo 'refusing to remove source-build virtual package without its ownership marker' >&2
  exit 1
fi
if [ "$workspace" != "${dependency_workspace:-}" ]; then
  remove_owned_workspace "$root" "$workspace" "$identity" "$owner"
fi
for protected_path in "$@"; do
  case "$protected_path" in /run/alpineform/build-inputs/[a-f0-9]*) ;; *) echo 'invalid protected source-build input cleanup path' >&2; exit 1;; esac
  if [ -d "$protected_path" ]; then echo 'refusing directory at protected source-build input path' >&2; exit 1; fi
  rm -f "$protected_path"
done
finish_owned_build_runtime_locked "$owner" "$runtime_transaction_generation"
release_owned_build_runtime_lock "$owner"
if [ "$marker_owned" = true ]; then rm -f "$marker"; fi
`

const componentBuildWorkspaceCapacityScript = `set -eu
root=$1
probe=$root
while [ ! -e "$probe" ] && [ ! -L "$probe" ] && [ "$probe" != / ]; do
  probe=${probe%/*}
  [ -n "$probe" ] || probe=/
done
available=unknown
if [ -d "$probe" ] && [ ! -L "$probe" ]; then
  candidate=$(df -Pk "$probe" 2>/dev/null | awk 'NR == 2 { print $4; exit }')
  case "$candidate" in ''|*[!0-9]*) ;; *) available=$candidate;; esac
fi
printf '%s\n' "$available"
`

const componentBuildInstallInspectScript = `set -eu
path=$1
install_marker=$2
output_marker=$3
identity=$4
owner=$5
group=$6
mode=$7
if [ ! -e "$path" ]; then echo missing; exit 0; fi
if [ -L "$path" ] || [ ! -f "$path" ]; then echo other; exit 0; fi
if [ ! -f "$install_marker" ] || [ -L "$install_marker" ] || [ ! -f "$output_marker" ] || [ -L "$output_marker" ]; then echo unowned; exit 0; fi
if [ "$(sed -n '1p' "$install_marker")" != "$identity" ] || [ "$(sed -n '3p' "$install_marker")" != "$path" ]; then echo stale; exit 0; fi
want=$(sed -n '2p' "$install_marker")
if [ "$(sed -n '1p' "$output_marker")" != "$identity" ] || [ "$(sed -n '2p' "$output_marker")" != "$want" ]; then echo stale; exit 0; fi
if [ "$(sha256sum "$path" | awk '{print $1}')" != "$want" ]; then echo drifted; exit 0; fi
actual_owner=$(stat -c '%U' "$path"); actual_uid=$(stat -c '%u' "$path")
actual_group=$(stat -c '%G' "$path"); actual_gid=$(stat -c '%g' "$path")
actual_mode=$(stat -c '%a' "$path")
[ "${#actual_mode}" -eq 4 ] || actual_mode=0$actual_mode
if [ "$actual_owner" != "$owner" ] && [ "$actual_uid" != "$owner" ]; then echo drifted; exit 0; fi
if [ "$actual_group" != "$group" ] && [ "$actual_gid" != "$group" ]; then echo drifted; exit 0; fi
if [ "$actual_mode" != "$mode" ]; then echo drifted; exit 0; fi
echo installed
`

const componentBuildInstallApplyScript = `set -eu
cache=$1
output_marker=$2
identity=$3
path=$4
owner=$5
group=$6
mode=$7
install_marker=$8
if [ ! -f "$cache" ] || [ -L "$cache" ] || [ ! -f "$output_marker" ] || [ -L "$output_marker" ]; then echo 'verified source-build output cache is missing or unsafe' >&2; exit 1; fi
if [ "$(sed -n '1p' "$output_marker")" != "$identity" ]; then echo 'source-build output identity is stale' >&2; exit 1; fi
want=$(sed -n '2p' "$output_marker")
if [ "$(sha256sum "$cache" | awk '{print $1}')" != "$want" ]; then echo 'source-build output cache checksum mismatch' >&2; exit 1; fi
parent=${path%/*}
[ -n "$parent" ] || parent=/
probe=$parent
while [ "$probe" != / ]; do
  if [ -L "$probe" ]; then echo 'source-build install parent contains a symbolic link' >&2; exit 1; fi
  probe=${probe%/*}; [ -n "$probe" ] || probe=/
done
mkdir -p "$parent"
if [ ! -L "$path" ] && [ -d "$path" ]; then echo 'refusing to replace a directory with source-build output' >&2; exit 1; fi
tmp=$(mktemp "$parent/.alpineform-build-install.XXXXXX")
marker_parent=${install_marker%/*}
probe=$marker_parent
while [ "$probe" != / ]; do
  if [ -L "$probe" ]; then echo 'source-build install marker parent contains a symbolic link' >&2; exit 1; fi
  probe=${probe%/*}; [ -n "$probe" ] || probe=/
done
mkdir -p "$marker_parent"
marker_tmp=$(mktemp "$marker_parent/.alpineform-build-install-marker.XXXXXX")
cleanup() { rm -f "$tmp" "$marker_tmp"; }
trap cleanup EXIT HUP INT TERM
cp "$cache" "$tmp"
if [ "$(sha256sum "$tmp" | awk '{print $1}')" != "$want" ]; then echo 'source-build output changed while installing' >&2; exit 1; fi
chown "$owner:$group" "$tmp"
chmod "$mode" "$tmp"
printf '%s\n%s\n%s\n' "$identity" "$want" "$path" >"$marker_tmp"
chmod 0600 "$marker_tmp"
trap '' HUP INT TERM
mv -fT "$marker_tmp" "$install_marker"
mv -fT "$tmp" "$path"
trap - EXIT HUP INT TERM
`

const componentBuildInstallDeleteScript = `set -eu
path=$1
install_marker=$2
output_marker=$3
cache=$4
identity=$5
if [ ! -f "$install_marker" ] || [ -L "$install_marker" ]; then
  if [ -e "$path" ] || [ -L "$path" ]; then echo 'refusing to destroy source-build installation without its ownership marker' >&2; exit 1; fi
else
  if [ "$(sed -n '1p' "$install_marker")" != "$identity" ] || [ "$(sed -n '3p' "$install_marker")" != "$path" ]; then
    echo 'refusing to destroy source-build installation owned by another identity' >&2
    exit 1
  fi
  want=$(sed -n '2p' "$install_marker")
  if [ -e "$path" ] || [ -L "$path" ]; then
    if [ -L "$path" ] || [ ! -f "$path" ] || [ "$(sha256sum "$path" | awk '{print $1}')" != "$want" ]; then
      echo 'refusing to destroy drifted source-build installation' >&2
      exit 1
    fi
    rm -f "$path"
  fi
fi
if [ -e "$cache" ] || [ -L "$cache" ]; then
  if [ -L "$cache" ] || [ ! -f "$cache" ] || [ ! -f "$output_marker" ] || [ -L "$output_marker" ] || [ "$(sed -n '1p' "$output_marker")" != "$identity" ]; then
    echo 'refusing to destroy unverified source-build output cache' >&2
    exit 1
  fi
  want=$(sed -n '2p' "$output_marker")
  if [ "$(sha256sum "$cache" | awk '{print $1}')" != "$want" ]; then echo 'refusing to destroy drifted source-build output cache' >&2; exit 1; fi
  rm -f "$cache" "$output_marker"
elif [ -e "$output_marker" ] || [ -L "$output_marker" ]; then
  echo 'refusing orphaned source-build output marker during destroy' >&2
  exit 1
fi
rm -f "$install_marker"
`

func inspectComponentBuildInput(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	path, digest, err := componentBuildInputIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.component_build_input", Script: componentSourceInspectScript, Arguments: []string{path}, RedactOutput: node.Sensitive || node.Ephemeral})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "" || lines[0] == "missing" {
		return engine.ObservedResource{}, nil
	}
	if len(lines) != 2 || lines[0] != "file" || strings.ToLower(lines[1]) != digest {
		return engine.ObservedResource{Exists: true, Values: map[string]any{"verified": false}, Protected: node.Sensitive || node.Ephemeral}, nil
	}
	return buildObserved(node), nil
}

func applyComponentBuildInput(ctx context.Context, runner backend.Runner, step engine.Step) (engine.ObservedResource, error) {
	node := step.Node
	path, digest, err := componentBuildInputIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	kind := stringValue(node.Desired, "kind")
	command := backend.Command{Name: "apply.component_build_input", RedactOutput: true}
	switch kind {
	case "url":
		url := stringValue(node.Desired, "url")
		if url == "" {
			return engine.ObservedResource{}, fmt.Errorf("source-build URL input is empty")
		}
		command.Script, command.Arguments = componentSourceApplyScript, []string{url, digest, path, "shared"}
	case "source", "content":
		content, ok := node.Payload["content"].([]byte)
		if !ok {
			return engine.ObservedResource{}, fmt.Errorf("source-build input has no content payload")
		}
		command.Script, command.Arguments, command.Stdin, command.RedactStdin = componentBuildInputWriteScript, []string{path, digest}, content, true
	default:
		return engine.ObservedResource{}, fmt.Errorf("unsupported source-build input kind %q", kind)
	}
	if _, err := runner.Run(ctx, command); err != nil {
		return engine.ObservedResource{}, err
	}
	observed, err := inspectComponentBuildInput(ctx, runner, node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if step.Prior != nil {
		oldPath, _ := step.Prior.Delete["path"].(string)
		if oldPath != "" && oldPath != path {
			if err := deleteBuildFile(ctx, runner, "cleanup.component_build_input_previous", oldPath, stepIsProtected(step)); err != nil {
				return engine.ObservedResource{}, err
			}
		}
	}
	return observed, nil
}

func inspectComponentBuildDependencies(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	virtual, marker, owner, identity, outputMarker, err := componentBuildDependencyIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	outputCache, err := componentBuildOutputCacheSelection(node, outputMarker)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	root, workspace, workspaceIdentity, err := componentBuildWorkspaceSelection(node)
	if err != nil || workspaceIdentity != identity {
		return engine.ObservedResource{}, fmt.Errorf("source-build dependency workspace identity is invalid")
	}
	packages, err := desiredStringList(node.Desired, "packages")
	if err != nil {
		return engine.ObservedResource{}, err
	}
	for _, pkg := range packages {
		if !providerAPKPackageNamePattern.MatchString(pkg) {
			return engine.ObservedResource{}, fmt.Errorf("invalid source-build APK dependency %q", pkg)
		}
	}
	arguments := append([]string{virtual, marker, owner, identity, root, workspace, outputMarker, outputCache, product.DefaultComponentBuildWorkspaceRoot}, packages...)
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.component_build_dependencies", Script: componentBuildDependenciesInspectScript, Arguments: arguments})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	status := lines[0]
	if status == "missing" || status == "" {
		return engine.ObservedResource{}, nil
	}
	if status == "stale" && len(lines) == 2 && componentProviderSHA256Pattern.MatchString(lines[1]) {
		observed := cloneDesired(node.Desired)
		observed["build_identity"] = lines[1]
		observed["workspace_recovery_pending"] = true
		return engine.ObservedResource{Exists: true, Values: observed}, nil
	}
	if status != "active" && status != "satisfied" {
		return engine.ObservedResource{}, fmt.Errorf("inspect source-build dependencies returned invalid status %q", status)
	}
	return buildObserved(node), nil
}

func applyComponentBuildDependencies(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	virtual, marker, owner, identity, _, err := componentBuildDependencyIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	root, workspace, workspaceIdentity, err := componentBuildWorkspaceSelection(node)
	if err != nil || workspaceIdentity != identity {
		return engine.ObservedResource{}, fmt.Errorf("source-build dependency workspace identity is invalid")
	}
	packages, err := desiredStringList(node.Desired, "packages")
	if err != nil {
		return engine.ObservedResource{}, err
	}
	for _, pkg := range packages {
		if !providerAPKPackageNamePattern.MatchString(pkg) {
			return engine.ObservedResource{}, fmt.Errorf("invalid source-build APK dependency %q", pkg)
		}
	}
	arguments := append([]string{virtual, marker, owner, identity, root, workspace, product.DefaultComponentBuildWorkspaceRoot}, packages...)
	if _, err := runner.Run(ctx, backend.Command{Name: "apply.component_build_dependencies", Script: componentBuildDependenciesApplyScript, Arguments: arguments, RedactOutput: true}); err != nil {
		return engine.ObservedResource{}, diagnoseComponentBuildWorkspaceFailure(runner, root, workspace, err)
	}
	return inspectComponentBuildDependencies(ctx, runner, node)
}

func inspectComponentBuildWorkspace(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	root, workspace, identity, outputMarker, err := componentBuildWorkspaceIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	outputCache, err := componentBuildOutputCacheSelection(node, outputMarker)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	owner := stringValue(node.Desired, "owner_id")
	virtual, dependencyMarker := stringValue(node.Desired, "virtual_package"), stringValue(node.Desired, "dependency_marker")
	if !componentBuildOwnerPattern.MatchString(owner) || !componentBuildVirtualPackagePattern.MatchString(virtual) || validateRemoteFilePath(dependencyMarker) != nil {
		return engine.ObservedResource{}, fmt.Errorf("source-build workspace ownership metadata is invalid")
	}
	outputPath := stringValue(node.Desired, "output")
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.component_build_workspace", Script: componentBuildWorkspaceInspectScript, Arguments: []string{root, workspace, identity, owner, outputMarker, outputCache, outputPath, virtual, dependencyMarker, product.DefaultComponentBuildWorkspaceRoot}, RedactOutput: node.Sensitive || node.Ephemeral})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	status := strings.TrimSpace(string(output))
	if status == "missing" || status == "" {
		return engine.ObservedResource{}, nil
	}
	if status == "unsafe" {
		return engine.ObservedResource{}, fmt.Errorf("source-build workspace is unowned or has an unsafe ownership, mode, or symbolic-link boundary")
	}
	if status != "active" && status != "satisfied" {
		return engine.ObservedResource{}, fmt.Errorf("inspect source-build workspace returned invalid status %q", status)
	}
	return buildObserved(node), nil
}

func applyComponentBuildWorkspace(ctx context.Context, runner backend.Runner, node graph.Node) (observed engine.ObservedResource, err error) {
	root, workspace, identity, _, err := componentBuildWorkspaceIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	virtual, owner, dependencyMarker := stringValue(node.Desired, "virtual_package"), stringValue(node.Desired, "owner_id"), stringValue(node.Desired, "dependency_marker")
	if !componentBuildVirtualPackagePattern.MatchString(virtual) || !componentBuildOwnerPattern.MatchString(owner) || validateRemoteFilePath(dependencyMarker) != nil {
		return engine.ObservedResource{}, fmt.Errorf("source-build workspace ownership metadata is invalid")
	}
	defer func() {
		if err != nil {
			primary := err
			if cleanupErr := cleanupComponentBuildFailure(runner, node); cleanupErr != nil {
				err = errors.Join(primary, fmt.Errorf("source-build failure cleanup failed: %w", cleanupErr))
			}
			err = diagnoseComponentBuildWorkspaceFailure(runner, root, workspace, err)
		}
	}()
	inputPaths, ok := node.Desired["input_paths"].(map[string]string)
	if !ok {
		return engine.ObservedResource{}, fmt.Errorf("source-build workspace input paths are invalid")
	}
	inputSHA, ok := node.Payload["input_sha256"].(map[string]string)
	if !ok {
		return engine.ObservedResource{}, fmt.Errorf("source-build workspace input digests are invalid")
	}
	inputExtract, ok := node.Payload["input_extract"].(map[string]map[string]any)
	if !ok {
		return engine.ObservedResource{}, fmt.Errorf("source-build workspace input extraction metadata is invalid")
	}
	destinations := make([]string, 0, len(inputPaths))
	for destination := range inputPaths {
		destinations = append(destinations, destination)
	}
	sort.Strings(destinations)
	working := stringValue(node.Desired, "working_directory")
	arguments := []string{root, workspace, identity, owner, virtual, dependencyMarker, product.DefaultComponentBuildWorkspaceRoot, working}
	for _, destination := range destinations {
		digest := inputSHA[destination]
		if !componentProviderSHA256Pattern.MatchString(digest) {
			return engine.ObservedResource{}, fmt.Errorf("source-build input %q has invalid digest metadata", destination)
		}
		format := ""
		strip := 0
		if extract, exists := inputExtract[destination]; exists {
			format, _ = extract["format"].(string)
			strip, _ = extract["strip_components"].(int)
			if format != "tar.gz" || strip < 0 || strip > 1024 {
				return engine.ObservedResource{}, fmt.Errorf("source-build input %q has invalid extraction metadata", destination)
			}
		}
		arguments = append(arguments, inputPaths[destination], destination, digest, format, strconv.Itoa(strip))
	}
	if _, err = runner.Run(ctx, backend.Command{Name: "apply.component_build_workspace.prepare", Script: componentBuildWorkspacePrepareScript, Arguments: arguments, RedactOutput: true}); err != nil {
		return engine.ObservedResource{}, err
	}
	commands, ok := node.Payload["commands"].([]map[string]any)
	if !ok || len(commands) == 0 {
		return engine.ObservedResource{}, fmt.Errorf("source-build workspace command payload is invalid")
	}
	environment, ok := node.Payload["environment"].(map[string]string)
	if !ok {
		return engine.ObservedResource{}, fmt.Errorf("source-build workspace environment payload is invalid")
	}
	for name, value := range environment {
		if !buildEnvironmentNamePattern.MatchString(name) || strings.ContainsAny(value, "\x00\r\n") {
			return engine.ObservedResource{}, fmt.Errorf("source-build environment payload is invalid")
		}
	}
	for index, command := range commands {
		argv, ok := command["argv"].([]string)
		if !ok || len(argv) == 0 {
			return engine.ObservedResource{}, fmt.Errorf("source-build command %d has invalid argv payload", index)
		}
		stdin, ok := command["stdin"].([]byte)
		if !ok {
			return engine.ObservedResource{}, fmt.Errorf("source-build command %d has invalid stdin payload", index)
		}
		manifest := componentBuildManifest(environment, stdin)
		commandArguments := append([]string{root, workspace, identity, owner, working}, argv...)
		if _, err = runner.Run(ctx, backend.Command{Name: "apply.component_build_workspace.command", Script: componentBuildCommandScript, Arguments: commandArguments, Stdin: manifest, RedactStdin: true, RedactOutput: true}); err != nil {
			return engine.ObservedResource{}, err
		}
	}
	if _, err = runner.Run(ctx, backend.Command{Name: "apply.component_build_workspace.ready", Script: componentBuildWorkspaceReadyScript, Arguments: []string{root, workspace, identity, owner, stringValue(node.Desired, "output")}, RedactOutput: true}); err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectComponentBuildWorkspace(ctx, runner, node)
}

func inspectComponentBuildOutput(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	cache, marker, identity, err := componentBuildOutputIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.component_build_output", Script: componentBuildOutputInspectScript, Arguments: []string{cache, marker, identity}, RedactOutput: node.Sensitive || node.Ephemeral})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if strings.TrimSpace(string(output)) != "verified" {
		return engine.ObservedResource{}, nil
	}
	return buildObserved(node), nil
}

func applyComponentBuildOutput(ctx context.Context, runner backend.Runner, step engine.Step) (observed engine.ObservedResource, err error) {
	node := step.Node
	cache, marker, identity, err := componentBuildOutputIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	root, workspace, workspaceIdentity, err := componentBuildWorkspaceSelection(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	owner := stringValue(node.Desired, "owner_id")
	if workspaceIdentity != identity || !componentBuildOwnerPattern.MatchString(owner) {
		return engine.ObservedResource{}, fmt.Errorf("source-build output workspace ownership metadata is invalid")
	}
	defer func() {
		if err != nil {
			primary := err
			if cleanupErr := cleanupComponentBuildFailure(runner, node); cleanupErr != nil {
				err = errors.Join(primary, fmt.Errorf("source-build failure cleanup failed: %w", cleanupErr))
			}
			err = diagnoseComponentBuildWorkspaceFailure(runner, root, workspace, err)
		}
	}()
	maxBytes, ok := buildInt64Value(node.Desired, "max_output_bytes")
	if !ok || maxBytes < 1 {
		return engine.ObservedResource{}, fmt.Errorf("source-build output has invalid size metadata")
	}
	arguments := []string{root, workspace, identity, owner, stringValue(node.Desired, "output"), stringValue(node.Desired, "output_sha256"), strconv.FormatInt(maxBytes, 10), cache, marker, strconv.FormatBool(boolValue(node.Desired, "executable"))}
	if _, err = runner.Run(ctx, backend.Command{Name: "apply.component_build_output", Script: componentBuildOutputApplyScript, Arguments: arguments, RedactOutput: true}); err != nil {
		return engine.ObservedResource{}, err
	}
	observed, err = inspectComponentBuildOutput(ctx, runner, node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if step.Prior != nil {
		oldCache, _ := step.Prior.Delete["cache_path"].(string)
		oldMarker, _ := step.Prior.Delete["marker_path"].(string)
		for _, previous := range []struct{ operation, path, current string }{
			{"cleanup.component_build_output_previous", oldCache, cache},
			{"cleanup.component_build_output_marker_previous", oldMarker, marker},
		} {
			if previous.path != "" && previous.path != previous.current {
				if err := deleteBuildFile(ctx, runner, previous.operation, previous.path, stepIsProtected(step)); err != nil {
					return engine.ObservedResource{}, err
				}
			}
		}
	}
	return observed, nil
}

func inspectComponentBuildCleanup(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	root, workspace, virtual, dependencyMarker, outputMarker, identity, owner, err := componentBuildCleanupIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	protectedPaths, err := componentBuildProtectedInputPaths(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	arguments := append([]string{root, workspace, virtual, dependencyMarker, outputMarker, identity, owner, product.DefaultComponentBuildWorkspaceRoot}, protectedPaths...)
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.component_build_cleanup", Script: componentBuildCleanupInspectScript, Arguments: arguments})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	status := strings.TrimSpace(string(output))
	if status == "unsafe" {
		return engine.ObservedResource{}, fmt.Errorf("source-build cleanup found unowned or unsafe workspace metadata")
	}
	if status != "clean" {
		return engine.ObservedResource{}, nil
	}
	return buildObserved(node), nil
}

func applyComponentBuildCleanup(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	root, workspace, virtual, marker, _, identity, owner, err := componentBuildCleanupIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	protectedPaths, err := componentBuildProtectedInputPaths(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	arguments := append([]string{root, workspace, virtual, marker, owner, identity, product.DefaultComponentBuildWorkspaceRoot}, protectedPaths...)
	if _, err := runner.Run(ctx, backend.Command{Name: "apply.component_build_cleanup", Script: componentBuildCleanupScript, Arguments: arguments, RedactOutput: true}); err != nil {
		return engine.ObservedResource{}, diagnoseComponentBuildWorkspaceFailure(runner, root, workspace, err)
	}
	observed, err := inspectComponentBuildCleanup(ctx, runner, node)
	if err != nil {
		return engine.ObservedResource{}, diagnoseComponentBuildWorkspaceFailure(runner, root, workspace, err)
	}
	return observed, nil
}

func inspectComponentBuildInstall(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	path, installMarker, outputMarker, identity, err := componentBuildInstallIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	owner, group, mode := stringValue(node.Desired, "owner"), stringValue(node.Desired, "group"), stringValue(node.Desired, "mode")
	if !providerAccountPattern.MatchString(owner) || !providerAccountPattern.MatchString(group) || !validMode(mode) {
		return engine.ObservedResource{}, fmt.Errorf("source-build install has invalid owner, group, or mode metadata")
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.component_build_install", Script: componentBuildInstallInspectScript, Arguments: []string{path, installMarker, outputMarker, identity, owner, group, mode}, RedactOutput: node.Sensitive || node.Ephemeral})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	status := strings.TrimSpace(string(output))
	if status == "missing" || status == "" {
		return engine.ObservedResource{}, nil
	}
	if status != "installed" {
		return engine.ObservedResource{Exists: true, Values: map[string]any{"installed": false}, Protected: node.Sensitive || node.Ephemeral}, nil
	}
	return buildObserved(node), nil
}

func applyComponentBuildInstall(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	path, installMarker, outputMarker, identity, err := componentBuildInstallIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	cache := stringValue(node.Desired, "cache_path")
	if err := validateRemoteFilePath(cache); err != nil {
		return engine.ObservedResource{}, fmt.Errorf("source-build output cache: %w", err)
	}
	owner, group, mode := stringValue(node.Desired, "owner"), stringValue(node.Desired, "group"), stringValue(node.Desired, "mode")
	if !providerAccountPattern.MatchString(owner) || !providerAccountPattern.MatchString(group) || !validMode(mode) {
		return engine.ObservedResource{}, fmt.Errorf("source-build install has invalid owner, group, or mode metadata")
	}
	arguments := []string{cache, outputMarker, identity, path, owner, group, mode, installMarker}
	if _, err := runner.Run(ctx, backend.Command{Name: "apply.component_build_install", Script: componentBuildInstallApplyScript, Arguments: arguments, RedactOutput: true}); err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectComponentBuildInstall(ctx, runner, node)
}

func deleteComponentBuildResource(ctx context.Context, runner backend.Runner, step engine.Step) error {
	kind := step.Node.Kind
	if kind == "" && step.Prior != nil {
		kind = step.Prior.Kind
	}
	deletion := step.Node.Desired
	if len(deletion) == 0 && step.Prior != nil {
		deletion = step.Prior.Delete
	} else if nested, ok := deletion["delete"].(map[string]any); ok {
		deletion = nested
	}
	switch kind {
	case "component_build_input":
		return deleteBuildFile(ctx, runner, "delete.component_build_input", stringValue(deletion, "path"), stepIsProtected(step))
	case "component_build_dependencies":
		virtual, marker := stringValue(deletion, "virtual_package"), stringValue(deletion, "marker_path")
		if !componentBuildVirtualPackagePattern.MatchString(virtual) || validateRemoteFilePath(marker) != nil {
			return fmt.Errorf("invalid source-build dependency deletion metadata")
		}
		identity := stringValue(deletion, "build_identity")
		if identity == "" && step.Node.Desired != nil {
			identity = stringValue(step.Node.Desired, "build_identity")
		}
		if identity == "" {
			return fmt.Errorf("source-build dependency destroy requires current ownership identity")
		}
		owner := stringValue(deletion, "owner_id")
		if owner == "" && step.Node.Desired != nil {
			owner = stringValue(step.Node.Desired, "owner_id")
		}
		if !componentBuildOwnerPattern.MatchString(owner) {
			return fmt.Errorf("source-build dependency destroy requires current ownership metadata")
		}
		selectionNode := step.Node
		selectionNode.Desired = cloneDesired(deletion)
		if selectionNode.Desired["build_identity"] == nil {
			selectionNode.Desired["build_identity"] = identity
		}
		root, workspace, workspaceIdentity, selectionErr := componentBuildWorkspaceSelection(selectionNode)
		if selectionErr != nil || workspaceIdentity != identity {
			return fmt.Errorf("source-build dependency destroy has invalid workspace metadata")
		}
		_, err := runner.Run(ctx, backend.Command{Name: "delete.component_build_dependencies", Script: componentBuildCleanupScript, Arguments: []string{root, workspace, virtual, marker, owner, identity, product.DefaultComponentBuildWorkspaceRoot}, RedactOutput: true})
		if err != nil {
			return diagnoseComponentBuildWorkspaceFailure(runner, root, workspace, err)
		}
		return nil
	case "component_build_workspace", "component_build_cleanup":
		return nil
	case "component_build_output":
		if err := deleteBuildFile(ctx, runner, "delete.component_build_output", stringValue(deletion, "cache_path"), stepIsProtected(step)); err != nil {
			return err
		}
		return deleteBuildFile(ctx, runner, "delete.component_build_output_marker", stringValue(deletion, "marker_path"), stepIsProtected(step))
	case "component_build_install":
		path, installMarker := stringValue(deletion, "path"), stringValue(deletion, "install_marker")
		cache, outputMarker := stringValue(deletion, "cache_path"), stringValue(deletion, "output_marker")
		identity := stringValue(deletion, "build_identity")
		if !componentProviderSHA256Pattern.MatchString(identity) {
			return fmt.Errorf("source-build install destroy has invalid build identity")
		}
		for _, value := range []string{path, installMarker, cache, outputMarker} {
			if err := validateRemoteFilePath(value); err != nil {
				return err
			}
		}
		_, err := runner.Run(ctx, backend.Command{
			Name: "delete.component_build_install", Script: componentBuildInstallDeleteScript,
			Arguments: []string{path, installMarker, outputMarker, cache, identity}, RedactOutput: stepIsProtected(step),
		})
		return err
	default:
		return fmt.Errorf("unsupported source-build deletion kind %q", kind)
	}
}

func componentBuildInputIdentity(node graph.Node) (string, string, error) {
	path := stringValue(node.Desired, "path")
	if err := validateRemoteFilePath(path); err != nil {
		return "", "", err
	}
	digest := stringValue(node.Desired, "sha256")
	if payload, ok := node.Payload["sha256"].(string); ok && payload != "" {
		digest = payload
	}
	if !componentProviderSHA256Pattern.MatchString(digest) {
		return "", "", fmt.Errorf("source-build input has invalid SHA-256 metadata")
	}
	return path, digest, nil
}

func componentBuildDependencyIdentity(node graph.Node) (string, string, string, string, string, error) {
	virtual, marker := stringValue(node.Desired, "virtual_package"), stringValue(node.Desired, "marker_path")
	owner, identity, outputMarker := stringValue(node.Desired, "owner_id"), stringValue(node.Desired, "build_identity"), stringValue(node.Desired, "output_marker")
	if !componentBuildVirtualPackagePattern.MatchString(virtual) || !componentBuildOwnerPattern.MatchString(owner) || !componentProviderSHA256Pattern.MatchString(identity) {
		return "", "", "", "", "", fmt.Errorf("source-build dependency ownership metadata is invalid")
	}
	if err := validateRemoteFilePath(marker); err != nil {
		return "", "", "", "", "", err
	}
	if err := validateRemoteFilePath(outputMarker); err != nil {
		return "", "", "", "", "", err
	}
	return virtual, marker, owner, identity, outputMarker, nil
}

func componentBuildOutputCacheSelection(node graph.Node, outputMarker string) (string, error) {
	if raw, exists := node.Payload[componentBuildOutputCachePayload]; exists {
		cache, ok := raw.(string)
		if !ok || validateRemoteFilePath(cache) != nil || outputMarker != cache+".sha256" {
			return "", fmt.Errorf("source-build output cache payload is invalid")
		}
		return cache, nil
	}
	const markerSuffix = ".sha256"
	if !strings.HasSuffix(outputMarker, markerSuffix) {
		return "", fmt.Errorf("source-build output marker does not identify its cache")
	}
	cache := strings.TrimSuffix(outputMarker, markerSuffix)
	if err := validateRemoteFilePath(cache); err != nil {
		return "", fmt.Errorf("source-build output cache: %w", err)
	}
	return cache, nil
}

func componentBuildWorkspaceSelection(node graph.Node) (string, string, string, error) {
	identity := stringValue(node.Desired, "build_identity")
	if !componentProviderSHA256Pattern.MatchString(identity) {
		return "", "", "", fmt.Errorf("source-build workspace identity is invalid")
	}
	root := ""
	if raw, exists := node.Payload[componentBuildWorkspaceRootPayload]; exists {
		var ok bool
		root, ok = raw.(string)
		if !ok || root == "" {
			return "", "", "", fmt.Errorf("source-build workspace root payload is invalid")
		}
	} else {
		legacyWorkspace := stringValue(node.Desired, "workspace")
		want := product.DefaultComponentBuildWorkspaceRoot + "/" + identity
		if legacyWorkspace != "" && legacyWorkspace != want {
			return "", "", "", fmt.Errorf("legacy source-build workspace identity is invalid")
		}
		root = product.DefaultComponentBuildWorkspaceRoot
	}
	if err := validateRemoteFilePath(root); err != nil || filepath.Clean(root) != root {
		return "", "", "", fmt.Errorf("source-build workspace root %q must be a clean absolute non-root path", root)
	}
	workspace := root + "/" + identity
	if filepath.Dir(workspace) != root || filepath.Base(workspace) != identity {
		return "", "", "", fmt.Errorf("source-build workspace path derivation is invalid")
	}
	return root, workspace, identity, nil
}

func componentBuildWorkspaceIdentity(node graph.Node) (string, string, string, string, error) {
	root, workspace, identity, err := componentBuildWorkspaceSelection(node)
	if err != nil {
		return "", "", "", "", err
	}
	outputMarker := stringValue(node.Desired, "output_marker")
	if err := validateRemoteFilePath(outputMarker); err != nil {
		return "", "", "", "", err
	}
	return root, workspace, identity, outputMarker, nil
}

func componentBuildOutputIdentity(node graph.Node) (string, string, string, error) {
	cache, marker, identity := stringValue(node.Desired, "cache_path"), stringValue(node.Desired, "marker_path"), stringValue(node.Desired, "build_identity")
	if !componentProviderSHA256Pattern.MatchString(identity) {
		return "", "", "", fmt.Errorf("source-build output identity is invalid")
	}
	if err := validateRemoteFilePath(cache); err != nil {
		return "", "", "", err
	}
	if err := validateRemoteFilePath(marker); err != nil {
		return "", "", "", err
	}
	return cache, marker, identity, nil
}

func componentBuildCleanupIdentity(node graph.Node) (string, string, string, string, string, string, string, error) {
	root, workspace, identity, outputMarker, err := componentBuildWorkspaceIdentity(node)
	if err != nil {
		return "", "", "", "", "", "", "", err
	}
	virtual, owner, dependencyMarker := stringValue(node.Desired, "virtual_package"), stringValue(node.Desired, "owner_id"), stringValue(node.Desired, "dependency_marker")
	if !componentBuildVirtualPackagePattern.MatchString(virtual) || !componentBuildOwnerPattern.MatchString(owner) {
		return "", "", "", "", "", "", "", fmt.Errorf("source-build cleanup ownership is invalid")
	}
	if err := validateRemoteFilePath(dependencyMarker); err != nil {
		return "", "", "", "", "", "", "", err
	}
	return root, workspace, virtual, dependencyMarker, outputMarker, identity, owner, nil
}

func componentBuildInstallIdentity(node graph.Node) (string, string, string, string, error) {
	path, installMarker, outputMarker := stringValue(node.Desired, "path"), stringValue(node.Desired, "install_marker"), stringValue(node.Desired, "output_marker")
	identity := stringValue(node.Desired, "build_identity")
	if !componentProviderSHA256Pattern.MatchString(identity) {
		return "", "", "", "", fmt.Errorf("source-build install identity is invalid")
	}
	for _, value := range []string{path, installMarker, outputMarker} {
		if err := validateRemoteFilePath(value); err != nil {
			return "", "", "", "", err
		}
	}
	return path, installMarker, outputMarker, identity, nil
}

func cleanupComponentBuildFailure(runner backend.Runner, node graph.Node) error {
	root, workspace, identity, err := componentBuildWorkspaceSelection(node)
	if err != nil {
		return err
	}
	virtual, marker, owner := stringValue(node.Desired, "virtual_package"), stringValue(node.Desired, "dependency_marker"), stringValue(node.Desired, "owner_id")
	if !componentBuildVirtualPackagePattern.MatchString(virtual) || !componentBuildOwnerPattern.MatchString(owner) {
		return fmt.Errorf("source-build failure cleanup ownership metadata is invalid")
	}
	if err := validateRemoteFilePath(marker); err != nil {
		return err
	}
	protectedPaths, err := componentBuildProtectedInputPaths(node)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	arguments := append([]string{root, workspace, virtual, marker, owner, identity, product.DefaultComponentBuildWorkspaceRoot}, protectedPaths...)
	_, err = runner.Run(ctx, backend.Command{Name: "cleanup.component_build_failure", Script: componentBuildCleanupScript, Arguments: arguments, RedactOutput: true})
	return err
}

type componentBuildWorkspaceFailure struct {
	root      string
	workspace string
	available string
	cause     error
}

func (failure componentBuildWorkspaceFailure) Error() string {
	return failure.SafeMessage() + ": " + failure.cause.Error()
}

func (failure componentBuildWorkspaceFailure) Unwrap() error { return failure.cause }

func (failure componentBuildWorkspaceFailure) SafeMessage() string {
	return fmt.Sprintf("source-build workspace failed: staging_root=%s work_path=%s available_kib=%s", failure.root, failure.workspace, failure.available)
}

func diagnoseComponentBuildWorkspaceFailure(runner backend.Runner, root, workspace string, cause error) error {
	if cause == nil {
		return nil
	}
	available := "unknown"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := runner.Run(ctx, backend.Command{
		Name: "diagnose.component_build_workspace_capacity", Script: componentBuildWorkspaceCapacityScript,
		Arguments: []string{root}, RedactOutput: true,
	})
	if err == nil {
		candidate := strings.TrimSpace(string(output))
		if _, parseErr := strconv.ParseUint(candidate, 10, 64); parseErr == nil {
			available = candidate
		}
	}
	return componentBuildWorkspaceFailure{root: root, workspace: workspace, available: available, cause: cause}
}

func componentBuildManifest(environment map[string]string, stdin []byte) []byte {
	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	sort.Strings(names)
	var manifest strings.Builder
	manifest.WriteString("APFBUILD1\n")
	manifest.WriteString(base64.StdEncoding.EncodeToString(stdin))
	manifest.WriteByte('\n')
	for _, name := range names {
		manifest.WriteString(name)
		manifest.WriteByte('\t')
		manifest.WriteString(base64.StdEncoding.EncodeToString([]byte(environment[name])))
		manifest.WriteByte('\n')
	}
	return []byte(manifest.String())
}

func buildObserved(node graph.Node) engine.ObservedResource {
	return engine.ObservedResource{
		Exists: true, Values: cloneDesired(node.Desired), Digest: corestate.Digest(node.Desired),
		Protected: node.Sensitive || node.Ephemeral,
	}
}

func desiredStringList(input map[string]any, name string) ([]string, error) {
	values, ok := input[name].([]string)
	if !ok {
		return nil, fmt.Errorf("source-build %s metadata is invalid", name)
	}
	return append([]string(nil), values...), nil
}

func optionalDesiredStringList(input map[string]any, name string) ([]string, error) {
	if _, exists := input[name]; !exists {
		return nil, nil
	}
	return desiredStringList(input, name)
}

func componentBuildProtectedInputPaths(node graph.Node) ([]string, error) {
	paths, err := optionalDesiredStringList(node.Desired, "protected_input_paths")
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		if filepath.Dir(path) != product.ComponentBuildProtectedInputRoot || !componentProviderSHA256Pattern.MatchString(filepath.Base(path)) {
			return nil, fmt.Errorf("source-build protected input path is outside the runtime-owned boundary")
		}
	}
	return paths, nil
}

func buildInt64Value(input map[string]any, name string) (int64, bool) {
	value, ok := input[name].(int64)
	return value, ok
}

func deleteBuildFile(ctx context.Context, runner backend.Runner, operation, path string, protected bool) error {
	if err := validateRemoteFilePath(path); err != nil {
		return err
	}
	_, err := runner.Run(ctx, backend.Command{Name: operation, Script: fileDeleteScript, Arguments: []string{path}, RedactOutput: protected})
	return err
}

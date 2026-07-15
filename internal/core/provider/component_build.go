package provider

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

var (
	componentBuildVirtualPackagePattern = regexp.MustCompile(`^\.alpineform-build-[a-f0-9]{24}$`)
	componentBuildOwnerPattern          = regexp.MustCompile(`^[a-f0-9]{32}$`)
	buildEnvironmentNamePattern         = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
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
virtual=$1
marker=$2
owner=$3
identity=$4
output_marker=$5
shift 5
if [ -f "$output_marker" ] && [ "$(sed -n '1p' "$output_marker")" = "$identity" ] && ! apk info --exists "$virtual" >/dev/null 2>&1 && [ ! -e "$marker" ]; then
  echo satisfied
  exit 0
fi
if [ -L /etc/apk/world ] || { [ -e /etc/apk/world ] && [ ! -f /etc/apk/world ]; }; then
  echo 'refusing unsafe APK world path during source-build dependency inspection' >&2
  exit 1
fi
installed=false
if apk info --exists "$virtual" >/dev/null 2>&1; then installed=true; fi
owned=false
marker_identity=
if [ -f "$marker" ] && [ ! -L "$marker" ] && [ "$(sed -n '1p' "$marker")" = "$virtual" ] && [ "$(sed -n '2p' "$marker")" = "$owner" ]; then
  owned=true
  marker_identity=$(sed -n '3p' "$marker")
fi
if [ "$installed" = true ] && [ "$owned" != true ]; then
  echo 'source-build virtual package collides with unowned APK state' >&2
  exit 1
fi
if [ -e "$marker" ] && [ "$owned" != true ]; then
  echo 'source-build dependency marker collides with another owner' >&2
  exit 1
fi
if [ "$installed" != true ]; then
  if [ "$#" -eq 0 ] && [ "$owned" = true ] && [ "$marker_identity" = "$identity" ]; then echo active; exit 0; fi
  echo missing
  exit 0
fi
world=false
if [ -f /etc/apk/world ] && awk -v virtual="$virtual" '$0 == virtual { found=1 } END { exit !found }' /etc/apk/world; then world=true; fi
packages_ok=true
for package in "$@"; do
  if ! apk info --exists "$package" >/dev/null 2>&1; then packages_ok=false; fi
done
if [ "$marker_identity" = "$identity" ] && [ "$world" = true ] && [ "$packages_ok" = true ]; then
  echo active
else
  printf 'stale\n%s\n' "$marker_identity"
fi
`

const componentBuildDependenciesApplyScript = `set -eu
virtual=$1
marker=$2
owner=$3
identity=$4
shift 4
if [ -L /etc/apk/world ] || { [ -e /etc/apk/world ] && [ ! -f /etc/apk/world ]; }; then
  echo 'refusing unsafe APK world path during source-build dependency apply' >&2
  exit 1
fi
if [ -e "$marker" ]; then
  if [ ! -f "$marker" ] || [ -L "$marker" ] || [ "$(sed -n '1p' "$marker")" != "$virtual" ] || [ "$(sed -n '2p' "$marker")" != "$owner" ]; then
    echo 'refusing source-build dependency marker owned by another resource' >&2
    exit 1
  fi
fi
if [ -f /etc/apk/world ] && awk -v virtual="$virtual" '$0 == virtual { found=1 } END { exit !found }' /etc/apk/world && [ ! -f "$marker" ]; then
  echo 'refusing to adopt unowned source-build virtual package world intent' >&2
  exit 1
fi
if apk info --exists "$virtual" >/dev/null 2>&1; then
  if [ ! -f "$marker" ]; then
    echo 'refusing to adopt an unowned source-build virtual package' >&2
    exit 1
  fi
  apk --quiet del "$virtual"
fi
parent=${marker%/*}
mkdir -p "$parent"
tmp=$(mktemp "$parent/.alpineform-build-dependencies.XXXXXX")
success=0
cleanup() {
  rm -f "$tmp"
  if [ "$success" != 1 ]; then
    if apk info --exists "$virtual" >/dev/null 2>&1; then apk --quiet del "$virtual" >/dev/null 2>&1 || true; fi
    rm -f "$marker"
  fi
}
trap cleanup EXIT HUP INT TERM
printf '%s\n%s\n%s\n' "$virtual" "$owner" "$identity" >"$tmp"
chmod 0600 "$tmp"
mv -f "$tmp" "$marker"
if [ "$#" -gt 0 ]; then apk --quiet add --virtual "$virtual" "$@"; fi
if [ "$#" -gt 0 ] && { [ ! -f /etc/apk/world ] || ! awk -v virtual="$virtual" '$0 == virtual { found=1 } END { exit !found }' /etc/apk/world; }; then
  echo 'source-build virtual package was not recorded in APK world' >&2
  exit 1
fi
for package in "$@"; do
  if ! apk info --exists "$package" >/dev/null 2>&1; then echo 'source-build dependency is not installed after apk add' >&2; exit 1; fi
done
success=1
trap - EXIT HUP INT TERM
`

const componentBuildWorkspaceInspectScript = `set -eu
workspace=$1
identity=$2
output_marker=$3
output=$4
if [ -f "$output_marker" ] && [ "$(sed -n '1p' "$output_marker")" = "$identity" ]; then
  echo satisfied
  exit 0
fi
if [ -d "$workspace" ] && [ ! -L "$workspace" ] && [ -f "$workspace/.alpineform-build-ready" ] && [ "$(cat "$workspace/.alpineform-build-ready")" = "$identity" ] && [ -e "$workspace/$output" ]; then
  echo active
  exit 0
fi
echo missing
`

const componentBuildWorkspacePrepareScript = `set -eu
workspace=$1
working=$2
shift 2
case "$workspace" in /var/tmp/alpineform/builds/[a-f0-9]*) ;; *) echo 'invalid source-build workspace' >&2; exit 1;; esac
if [ -L "$workspace" ]; then echo 'refusing symbolic-link source-build workspace' >&2; exit 1; fi
rm -rf "$workspace"
mkdir -p "$workspace"
chmod 0700 "$workspace"
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
  target="$workspace/$destination"
  parent=${target%/*}
  mkdir -p "$parent"
  if [ -z "$format" ]; then
    cp "$cache" "$target"
    chmod 0600 "$target"
    continue
  fi
  if [ "$format" != tar.gz ]; then echo 'unsupported source-build input archive format' >&2; exit 1; fi
  staging=$(mktemp -d "$workspace/.alpineform-build-extract.XXXXXX")
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
case "$working" in .) ;; *) mkdir -p "$workspace/$working";; esac
`

const componentBuildCommandScript = `set -eu
workspace=$1
working=$2
shift 2
case "$workspace" in /var/tmp/alpineform/builds/[a-f0-9]*) ;; *) echo 'invalid source-build workspace' >&2; exit 1;; esac
if [ ! -d "$workspace" ] || [ -L "$workspace" ]; then echo 'source-build workspace is missing or unsafe' >&2; exit 1; fi
case "$working" in .) directory=$workspace;; *) directory=$workspace/$working;; esac
if [ ! -d "$directory" ] || [ -L "$directory" ]; then echo 'source-build working directory is missing or unsafe' >&2; exit 1; fi
physical=$(cd -P "$directory" && pwd)
case "$physical" in "$workspace"|"$workspace"/*) ;; *) echo 'source-build working directory escapes workspace' >&2; exit 1;; esac
runtime_root=/run/alpineform/build-runtime
mkdir -p "$runtime_root"
chmod 0700 "$runtime_root"
manifest=$(mktemp "$runtime_root/manifest.XXXXXX")
stdin_file=$(mktemp "$runtime_root/stdin.XXXXXX")
env_names=$(mktemp "$runtime_root/env.XXXXXX")
pid=
cleanup() { rm -f "$manifest" "$stdin_file" "$env_names"; }
terminate() {
  if [ -n "$pid" ]; then kill -TERM "-$pid" >/dev/null 2>&1 || true; fi
  cleanup
  exit 130
}
trap cleanup EXIT
trap terminate HUP INT TERM
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
setsid bwrap \
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
  --bind "$workspace" /workspace \
  --chdir "$sandbox_directory" \
  -- "$@" <"$stdin_file" >/dev/null 2>&1 &
pid=$!
if ! wait "$pid"; then
  pid=
  echo 'source-build command failed; command output omitted' >&2
  exit 1
fi
pid=
cleanup
trap - EXIT HUP INT TERM
`

const componentBuildWorkspaceReadyScript = `set -eu
workspace=$1
identity=$2
output=$3
if [ ! -e "$workspace/$output" ]; then
  echo 'source-build command completed without the declared output' >&2
  exit 1
fi
tmp=$(mktemp "$workspace/.alpineform-build-ready.XXXXXX")
printf '%s' "$identity" >"$tmp"
chmod 0600 "$tmp"
mv -f "$tmp" "$workspace/.alpineform-build-ready"
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
workspace=$1
output=$2
expected=$3
max_bytes=$4
cache=$5
marker=$6
identity=$7
executable=$8
source=$workspace/$output
old_ifs=$IFS
IFS=/
set -- $output
IFS=$old_ifs
probe=$workspace
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
workspace=$1
virtual=$2
dependency_marker=$3
output_marker=$4
identity=$5
if [ ! -f "$output_marker" ] || [ "$(sed -n '1p' "$output_marker")" != "$identity" ]; then echo missing; exit 0; fi
if [ -e "$workspace" ] || [ -e "$dependency_marker" ] || apk info --exists "$virtual" >/dev/null 2>&1; then echo pending; exit 0; fi
shift 5
for protected_path in "$@"; do if [ -e "$protected_path" ]; then echo pending; exit 0; fi; done
echo clean
`

const componentBuildCleanupScript = `set -eu
workspace=$1
virtual=$2
marker=$3
owner=$4
shift 4
case "$workspace" in /var/tmp/alpineform/builds/[a-f0-9]*) ;; *) echo 'invalid source-build workspace cleanup path' >&2; exit 1;; esac
if [ -e "$marker" ]; then
  if [ ! -f "$marker" ] || [ -L "$marker" ] || [ "$(sed -n '1p' "$marker")" != "$virtual" ] || [ "$(sed -n '2p' "$marker")" != "$owner" ]; then
    echo 'refusing to clean unowned source-build dependency state' >&2
    exit 1
  fi
  if apk info --exists "$virtual" >/dev/null 2>&1; then apk --quiet del "$virtual"; fi
  rm -f "$marker"
elif apk info --exists "$virtual" >/dev/null 2>&1; then
  echo 'refusing to remove source-build virtual package without its ownership marker' >&2
  exit 1
fi
if [ -L "$workspace" ]; then echo 'refusing symbolic-link source-build workspace cleanup' >&2; exit 1; fi
rm -rf "$workspace"
for protected_path in "$@"; do
  case "$protected_path" in /run/alpineform/build-inputs/[a-f0-9]*) ;; *) echo 'invalid protected source-build input cleanup path' >&2; exit 1;; esac
  if [ -d "$protected_path" ]; then echo 'refusing directory at protected source-build input path' >&2; exit 1; fi
  rm -f "$protected_path"
done
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
		command.Script, command.Arguments = componentSourceApplyScript, []string{url, digest, path}
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
	packages, err := desiredStringList(node.Desired, "packages")
	if err != nil {
		return engine.ObservedResource{}, err
	}
	for _, pkg := range packages {
		if !providerAPKPackageNamePattern.MatchString(pkg) {
			return engine.ObservedResource{}, fmt.Errorf("invalid source-build APK dependency %q", pkg)
		}
	}
	arguments := append([]string{virtual, marker, owner, identity, outputMarker}, packages...)
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.component_build_dependencies", Script: componentBuildDependenciesInspectScript, Arguments: arguments})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	status := lines[0]
	if status == "missing" || status == "" {
		return engine.ObservedResource{}, nil
	}
	if status == "stale" && len(lines) == 2 {
		observed := cloneDesired(node.Desired)
		observed["build_identity"] = lines[1]
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
	packages, err := desiredStringList(node.Desired, "packages")
	if err != nil {
		return engine.ObservedResource{}, err
	}
	for _, pkg := range packages {
		if !providerAPKPackageNamePattern.MatchString(pkg) {
			return engine.ObservedResource{}, fmt.Errorf("invalid source-build APK dependency %q", pkg)
		}
	}
	arguments := append([]string{virtual, marker, owner, identity}, packages...)
	if _, err := runner.Run(ctx, backend.Command{Name: "apply.component_build_dependencies", Script: componentBuildDependenciesApplyScript, Arguments: arguments, RedactOutput: true}); err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectComponentBuildDependencies(ctx, runner, node)
}

func inspectComponentBuildWorkspace(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	workspace, identity, outputMarker, err := componentBuildWorkspaceIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	outputPath := stringValue(node.Desired, "output")
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.component_build_workspace", Script: componentBuildWorkspaceInspectScript, Arguments: []string{workspace, identity, outputMarker, outputPath}, RedactOutput: node.Sensitive || node.Ephemeral})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	status := strings.TrimSpace(string(output))
	if status == "missing" || status == "" {
		return engine.ObservedResource{}, nil
	}
	if status != "active" && status != "satisfied" {
		return engine.ObservedResource{}, fmt.Errorf("inspect source-build workspace returned invalid status %q", status)
	}
	return buildObserved(node), nil
}

func applyComponentBuildWorkspace(ctx context.Context, runner backend.Runner, node graph.Node) (observed engine.ObservedResource, err error) {
	workspace, identity, _, err := componentBuildWorkspaceIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	defer func() {
		if err != nil {
			cleanupComponentBuildFailure(runner, node)
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
	arguments := []string{workspace, working}
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
		commandArguments := append([]string{workspace, working}, argv...)
		if _, err = runner.Run(ctx, backend.Command{Name: "apply.component_build_workspace.command", Script: componentBuildCommandScript, Arguments: commandArguments, Stdin: manifest, RedactStdin: true, RedactOutput: true}); err != nil {
			return engine.ObservedResource{}, err
		}
	}
	if _, err = runner.Run(ctx, backend.Command{Name: "apply.component_build_workspace.ready", Script: componentBuildWorkspaceReadyScript, Arguments: []string{workspace, identity, stringValue(node.Desired, "output")}, RedactOutput: true}); err != nil {
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
	defer func() {
		if err != nil {
			cleanupComponentBuildFailure(runner, node)
		}
	}()
	workspace := stringValue(node.Desired, "workspace")
	maxBytes, ok := buildInt64Value(node.Desired, "max_output_bytes")
	if !ok || maxBytes < 1 {
		return engine.ObservedResource{}, fmt.Errorf("source-build output has invalid size metadata")
	}
	arguments := []string{workspace, stringValue(node.Desired, "output"), stringValue(node.Desired, "output_sha256"), strconv.FormatInt(maxBytes, 10), cache, marker, identity, strconv.FormatBool(boolValue(node.Desired, "executable"))}
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
	workspace, virtual, dependencyMarker, outputMarker, identity, _, err := componentBuildCleanupIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	protectedPaths, err := optionalDesiredStringList(node.Desired, "protected_input_paths")
	if err != nil {
		return engine.ObservedResource{}, err
	}
	arguments := append([]string{workspace, virtual, dependencyMarker, outputMarker, identity}, protectedPaths...)
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.component_build_cleanup", Script: componentBuildCleanupInspectScript, Arguments: arguments})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if strings.TrimSpace(string(output)) != "clean" {
		return engine.ObservedResource{}, nil
	}
	return buildObserved(node), nil
}

func applyComponentBuildCleanup(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	workspace, virtual, marker, _, _, owner, err := componentBuildCleanupIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	protectedPaths, err := optionalDesiredStringList(node.Desired, "protected_input_paths")
	if err != nil {
		return engine.ObservedResource{}, err
	}
	arguments := append([]string{workspace, virtual, marker, owner}, protectedPaths...)
	if _, err := runner.Run(ctx, backend.Command{Name: "apply.component_build_cleanup", Script: componentBuildCleanupScript, Arguments: arguments, RedactOutput: true}); err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectComponentBuildCleanup(ctx, runner, node)
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
		workspace := stringValue(deletion, "workspace")
		if workspace == "" {
			workspace = "/var/tmp/alpineform/builds/" + identity
		}
		_, err := runner.Run(ctx, backend.Command{Name: "delete.component_build_dependencies", Script: componentBuildCleanupScript, Arguments: []string{workspace, virtual, marker, owner}, RedactOutput: true})
		return err
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

func componentBuildWorkspaceIdentity(node graph.Node) (string, string, string, error) {
	workspace, identity, outputMarker := stringValue(node.Desired, "workspace"), stringValue(node.Desired, "build_identity"), stringValue(node.Desired, "output_marker")
	if !strings.HasPrefix(workspace, "/var/tmp/alpineform/builds/") || !componentProviderSHA256Pattern.MatchString(identity) || workspace != "/var/tmp/alpineform/builds/"+identity {
		return "", "", "", fmt.Errorf("source-build workspace identity is invalid")
	}
	if err := validateRemoteFilePath(outputMarker); err != nil {
		return "", "", "", err
	}
	return workspace, identity, outputMarker, nil
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

func componentBuildCleanupIdentity(node graph.Node) (string, string, string, string, string, string, error) {
	workspace, identity, outputMarker, err := componentBuildWorkspaceIdentity(node)
	if err != nil {
		return "", "", "", "", "", "", err
	}
	virtual, owner, dependencyMarker := stringValue(node.Desired, "virtual_package"), stringValue(node.Desired, "owner_id"), stringValue(node.Desired, "dependency_marker")
	if !componentBuildVirtualPackagePattern.MatchString(virtual) || !componentBuildOwnerPattern.MatchString(owner) {
		return "", "", "", "", "", "", fmt.Errorf("source-build cleanup ownership is invalid")
	}
	if err := validateRemoteFilePath(dependencyMarker); err != nil {
		return "", "", "", "", "", "", err
	}
	return workspace, virtual, dependencyMarker, outputMarker, identity, owner, nil
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

func cleanupComponentBuildFailure(runner backend.Runner, node graph.Node) {
	workspace := stringValue(node.Desired, "workspace")
	virtual := stringValue(node.Desired, "virtual_package")
	marker := stringValue(node.Desired, "dependency_marker")
	owner := stringValue(node.Desired, "owner_id")
	if workspace == "" || virtual == "" || marker == "" || owner == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	protectedPaths, _ := optionalDesiredStringList(node.Desired, "protected_input_paths")
	arguments := append([]string{workspace, virtual, marker, owner}, protectedPaths...)
	_, _ = runner.Run(ctx, backend.Command{Name: "cleanup.component_build_failure", Script: componentBuildCleanupScript, Arguments: arguments, RedactOutput: true})
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

package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
)

const componentArchiveInspectScript = `set -eu
path=$1
want=$2
if [ ! -e "$path" ]; then
  echo missing
  exit 0
fi
if [ ! -d "$path" ] || [ -L "$path" ]; then
  echo other
  exit 0
fi
status=clean
if [ ! -f "$path/.alpineform-artifact.sha256" ] || [ "$(cat "$path/.alpineform-artifact.sha256")" != "$want" ] || [ ! -f "$path/.alpineform-manifest.sha256" ]; then
  status=drift
else
  work=$(mktemp -d)
  trap 'rm -rf "$work"' EXIT HUP INT TERM
  if find "$path" -type l -print -quit | grep -q .; then
    status=drift
  else
    (
      cd "$path"
      find . -type f ! -name '.alpineform-artifact.sha256' ! -name '.alpineform-manifest.sha256' | LC_ALL=C sort >"$work/current"
      awk '{print $2}' .alpineform-manifest.sha256 | LC_ALL=C sort >"$work/expected"
      cmp -s "$work/current" "$work/expected" && sha256sum -c .alpineform-manifest.sha256 >/dev/null 2>&1
    ) || status=drift
  fi
fi
echo directory
stat -c '%U' "$path"
stat -c '%u' "$path"
stat -c '%G' "$path"
stat -c '%g' "$path"
stat -c '%a' "$path"
printf '%s\n' "$status"
`

const componentArchiveApplyScript = `set -eu
cache=$1
want=$2
path=$3
owner=$4
group=$5
mode=$6
strip=$7
if [ ! -f "$cache" ]; then
  echo 'verified archive cache file is missing' >&2
  exit 1
fi
actual=$(sha256sum "$cache" | awk '{print $1}')
if [ "$actual" != "$want" ]; then
  echo "archive checksum mismatch before extraction: expected $want, got $actual" >&2
  exit 1
fi
parent=${path%/*}
[ -n "$parent" ] || parent=/
mkdir -p "$parent"
work=$(mktemp -d "$parent/.alpineform-archive-work.XXXXXX")
staging="$work/staging"
mkdir "$staging"
old=
replaced=0
cleanup() {
  rm -rf "$staging"
  if [ "$replaced" = 1 ] && [ ! -e "$path" ] && [ -n "$old" ] && [ -e "$old" ]; then
    mv "$old" "$path" || true
  fi
  [ -z "$old" ] || rm -rf "$old"
  rm -rf "$work"
}
trap cleanup EXIT HUP INT TERM
manifest="$work/archive.list"
tar -tzf "$cache" >"$manifest"
if [ ! -s "$manifest" ]; then
  echo 'archive contains no entries' >&2
  exit 1
fi
while IFS= read -r entry; do
  if [ -z "$entry" ]; then
    echo 'archive contains an empty path' >&2
    exit 1
  fi
  case "$entry" in
    /*|..|../*|*/..|*/../*) echo "archive contains unsafe path: $entry" >&2; exit 1 ;;
    *[[:space:]\\:]*) echo 'archive paths containing whitespace, backslash, or colon are unsupported' >&2; exit 1 ;;
  esac
done <"$manifest"
if tar -tvzf "$cache" | awk '{print substr($1,1,1)}' | grep -qvE '^[-d]$'; then
  echo 'archive links and special entries are forbidden' >&2
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
' "$manifest" | LC_ALL=C sort >"$work/stripped.list"
if [ ! -s "$work/stripped.list" ]; then
  echo 'archive has no entries after strip_components' >&2
  exit 1
fi
if uniq -d "$work/stripped.list" | grep -q .; then
  echo 'archive entries collide after strip_components' >&2
  exit 1
fi
tar -xzf "$cache" -C "$staging" --strip-components "$strip"
if find "$staging" -type l -print -quit | grep -q . || find "$staging" ! -type f ! -type d -print -quit | grep -q .; then
  echo 'archive extraction produced a link or special entry' >&2
  exit 1
fi
line_count=$(find "$staging" -mindepth 1 -print | wc -l | tr -d ' ')
nul_count=$(find "$staging" -mindepth 1 -print0 | tr -cd '\000' | wc -c | tr -d ' ')
if [ "$line_count" != "$nul_count" ]; then
  echo 'archive paths containing newlines are forbidden' >&2
  exit 1
fi
if [ "$nul_count" = 0 ]; then
  echo 'archive extraction produced no installable entries' >&2
  exit 1
fi
chown -R "$owner:$group" "$staging"
chmod "$mode" "$staging"
(
  cd "$staging"
  find . -type f | LC_ALL=C sort >"$work/files.list"
  : >.alpineform-manifest.sha256
  while IFS= read -r file; do
    sha256sum "$file" >>.alpineform-manifest.sha256
  done <"$work/files.list"
  printf '%s' "$want" >.alpineform-artifact.sha256
  chmod 0600 .alpineform-manifest.sha256 .alpineform-artifact.sha256
)
old=$(mktemp -d "$parent/.alpineform-archive-old.XXXXXX")
rmdir "$old"
if [ -e "$path" ] || [ -L "$path" ]; then
  mv "$path" "$old"
  replaced=1
fi
mv "$staging" "$path"
rm -rf "$old"
old=
replaced=0
trap - EXIT HUP INT TERM
rm -rf "$work"
`

const componentArchiveDeleteScript = `set -eu
path=$1
if [ "$path" = / ] || [ -z "$path" ]; then
  echo 'refusing unsafe archive delete path' >&2
  exit 1
fi
rm -rf "$path"
`

func inspectComponentArchive(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	path, digest, err := componentInstallIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.component_archive", Script: componentArchiveInspectScript, Arguments: []string{path, digest}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "" || lines[0] == "missing" {
		return engine.ObservedResource{}, nil
	}
	observed := cloneDesired(node.Desired)
	if lines[0] != "directory" {
		observed["type"] = lines[0]
		return engine.ObservedResource{Exists: true, Values: observed}, nil
	}
	if len(lines) != 7 {
		return engine.ObservedResource{}, fmt.Errorf("inspect component archive %q returned %d fields, want 7", path, len(lines))
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
	if lines[6] != "clean" {
		observed["tree_integrity"] = lines[6]
	}
	return engine.ObservedResource{Exists: true, Values: observed}, nil
}

func applyComponentArchive(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	path, digest, err := componentInstallIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if format := stringValue(node.Desired, "extract_format"); format != "tar.gz" {
		return engine.ObservedResource{}, fmt.Errorf("component archive has unsupported extract format %q", format)
	}
	strip, ok := node.Desired["strip_components"].(int)
	if !ok || strip < 0 {
		return engine.ObservedResource{}, fmt.Errorf("component archive has invalid strip_components metadata")
	}
	cachePath := stringValue(node.Desired, "cache_path")
	if err := validateRemoteFilePath(cachePath); err != nil {
		return engine.ObservedResource{}, fmt.Errorf("component archive cache: %w", err)
	}
	owner := stringValue(node.Desired, "owner")
	group := stringValue(node.Desired, "group")
	mode := stringValue(node.Desired, "mode")
	if !providerAccountPattern.MatchString(owner) || !providerAccountPattern.MatchString(group) || !validMode(mode) {
		return engine.ObservedResource{}, fmt.Errorf("component archive %q has invalid owner, group, or mode metadata", path)
	}
	_, err = runner.Run(ctx, backend.Command{
		Name: "apply.component_archive", Script: componentArchiveApplyScript,
		Arguments: []string{cachePath, digest, path, owner, group, mode, strconv.Itoa(strip)}, RedactOutput: true,
	})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectComponentArchive(ctx, runner, node)
}

func deleteComponentArchive(ctx context.Context, runner backend.Runner, step engine.Step) error {
	path := componentDeletePath(step)
	if err := validateRemoteFilePath(path); err != nil {
		return err
	}
	_, err := runner.Run(ctx, backend.Command{Name: "delete.component_archive", Script: componentArchiveDeleteScript, Arguments: []string{path}, RedactOutput: stepIsProtected(step)})
	return err
}

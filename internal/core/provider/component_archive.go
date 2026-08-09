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
if grep -qE '(^|/)\.alpineform-(artifact|manifest)\.sha256$' "$work/stripped.list"; then
  echo 'archive contains a reserved AlpineForm metadata path' >&2
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

const componentProtectedArchiveMarkerValue = "alpineform-protected-archive-v1"

const componentProtectedArchiveSemanticInventoryScript = `write_semantic_inventory() {
  inventory_root=$1
  inventory_output=$2
  inventory_paths="$inventory_output.paths"
  if [ ! -d "$inventory_root" ] || [ -L "$inventory_root" ]; then return 1; fi
  if find "$inventory_root" -type l -print -quit | grep -q . || find "$inventory_root" ! -type f ! -type d -print -quit | grep -q .; then return 1; fi
  inventory_line_count=$(find "$inventory_root" -mindepth 1 -print | wc -l | tr -d ' ') || return 1
  inventory_nul_count=$(find "$inventory_root" -mindepth 1 -print0 | tr -cd '\000' | wc -c | tr -d ' ') || return 1
  if [ "$inventory_line_count" != "$inventory_nul_count" ]; then return 1; fi
  (
    cd "$inventory_root" || exit 1
    find . -mindepth 1 -print >"$inventory_paths.unsorted" || exit 1
    LC_ALL=C sort "$inventory_paths.unsorted" >"$inventory_paths" || exit 1
    inventory_uid=$(stat -c '%u' .) || exit 1
    inventory_gid=$(stat -c '%g' .) || exit 1
    inventory_mode=$(stat -c '%a' .) || exit 1
    {
      printf '.\tdirectory\t%s\t%s\t%s\t-\n' "$inventory_uid" "$inventory_gid" "$inventory_mode"
      while IFS= read -r inventory_entry; do
        case "$inventory_entry" in *[[:space:]\\:]*) exit 1 ;; esac
        if [ -f "$inventory_entry" ] && [ ! -L "$inventory_entry" ]; then
          inventory_type=file
          inventory_links=$(stat -c '%h' "$inventory_entry") || exit 1
          if [ "$inventory_links" != 1 ]; then exit 1; fi
          inventory_checksum=$(sha256sum "$inventory_entry") || exit 1
          inventory_digest=${inventory_checksum%% *}
        elif [ -d "$inventory_entry" ] && [ ! -L "$inventory_entry" ]; then
          inventory_type=directory
          inventory_digest=-
        else
          exit 1
        fi
        inventory_uid=$(stat -c '%u' "$inventory_entry") || exit 1
        inventory_gid=$(stat -c '%g' "$inventory_entry") || exit 1
        inventory_mode=$(stat -c '%a' "$inventory_entry") || exit 1
        printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
          "$inventory_entry" "$inventory_type" \
          "$inventory_uid" "$inventory_gid" "$inventory_mode" "$inventory_digest"
      done <"$inventory_paths"
    } >"$inventory_output"
  )
}
`

const componentProtectedArchiveInspectScript = `set -eu
path=$1
cache=$2
owner=$3
group=$4
mode=$5
strip=$6
` + componentProtectedArchiveSemanticInventoryScript + `if ! IFS= read -r want; then
  echo 'protected archive verification input is missing' >&2
  exit 1
fi
if [ ! -e "$path" ]; then
  echo missing
  exit 0
fi
if [ ! -d "$path" ] || [ -L "$path" ]; then
  echo other
  exit 0
fi
tree=clean
content=unverified
if [ ! -f "$path/.alpineform-artifact.sha256" ] || [ -L "$path/.alpineform-artifact.sha256" ] || [ "$(cat "$path/.alpineform-artifact.sha256")" != '` + componentProtectedArchiveMarkerValue + `' ] || [ ! -f "$path/.alpineform-manifest.sha256" ] || [ -L "$path/.alpineform-manifest.sha256" ]; then
  tree=drift
else
  work=$(mktemp -d)
  trap 'rm -rf "$work"' EXIT HUP INT TERM
  if find "$path" -type l -print -quit | grep -q .; then
    tree=drift
  else
    (
      cd "$path"
      find . -type f ! -name '.alpineform-artifact.sha256' ! -name '.alpineform-manifest.sha256' | LC_ALL=C sort >"$work/current"
      awk '{print $2}' .alpineform-manifest.sha256 | LC_ALL=C sort >"$work/expected"
      cmp -s "$work/current" "$work/expected" && sha256sum -c .alpineform-manifest.sha256 >/dev/null 2>&1
    ) || tree=drift
  fi
  rm -rf "$work"
  trap - EXIT HUP INT TERM
fi
if [ -e "$cache" ] || [ -L "$cache" ]; then
  if [ ! -f "$cache" ] || [ -L "$cache" ] || [ "$(sha256sum "$cache" | awk '{print $1}')" != "$want" ]; then
    content=unverified
  else
    work=$(mktemp -d)
    cleanup_cache_compare() { rm -rf "$work"; }
    trap cleanup_cache_compare EXIT HUP INT TERM
    manifest="$work/archive.list"
    if ! tar -tzf "$cache" >"$manifest" || [ ! -s "$manifest" ]; then
      content=unverified
    else
      safe=true
      while IFS= read -r entry; do
        if [ -z "$entry" ]; then safe=false; break; fi
        case "$entry" in
          /*|..|../*|*/..|*/../*|*[[:space:]\\:]*) safe=false; break ;;
        esac
      done <"$manifest"
      if [ "$safe" = true ] && tar -tvzf "$cache" | awk '{print substr($1,1,1)}' | grep -qvE '^[-d]$'; then safe=false; fi
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
      if [ ! -s "$work/stripped.list" ] || uniq -d "$work/stripped.list" | grep -q .; then safe=false; fi
      if grep -qE '(^|/)\.alpineform-(artifact|manifest)\.sha256$' "$work/stripped.list"; then safe=false; fi
      mkdir "$work/staging"
      if [ "$safe" != true ] || ! tar -xzf "$cache" -C "$work/staging" --strip-components "$strip"; then
        content=unverified
      elif find "$work/staging" -type l -print -quit | grep -q . || find "$work/staging" ! -type f ! -type d -print -quit | grep -q .; then
        content=unverified
      else
        chown -R "$owner:$group" "$work/staging"
        chmod "$mode" "$work/staging"
        (
          cd "$work/staging"
          find . -type f | LC_ALL=C sort >"$work/files.list"
          : >.alpineform-manifest.sha256
          while IFS= read -r file; do sha256sum "$file" >>.alpineform-manifest.sha256; done <"$work/files.list"
          printf '%s' '` + componentProtectedArchiveMarkerValue + `' >.alpineform-artifact.sha256
          chmod 0600 .alpineform-manifest.sha256 .alpineform-artifact.sha256
        )
        if write_semantic_inventory "$work/staging" "$work/cache.semantic" && \
           write_semantic_inventory "$path" "$work/installed.semantic" && \
           cmp -s "$work/cache.semantic" "$work/installed.semantic"; then
          content=verified
        fi
      fi
    fi
    rm -rf "$work"
    trap - EXIT HUP INT TERM
  fi
fi
echo directory
stat -c '%U' "$path"
stat -c '%u' "$path"
stat -c '%G' "$path"
stat -c '%g' "$path"
stat -c '%a' "$path"
printf '%s\n%s\n' "$content" "$tree"
`

const componentProtectedArchiveApplyScript = `set -eu
cache=$1
path=$2
owner=$3
group=$4
mode=$5
strip=$6
if ! IFS= read -r want; then
  echo 'protected archive verification input is missing' >&2
  exit 1
fi
if [ ! -f "$cache" ]; then
  echo 'verified archive cache file is missing' >&2
  exit 1
fi
actual=$(sha256sum "$cache" | awk '{print $1}')
if [ "$actual" != "$want" ]; then
  echo 'protected archive checksum mismatch before extraction' >&2
  exit 1
fi
parent=${path%/*}
[ -n "$parent" ] || parent=/
work=
staging=
old=
replaced=0
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
` + componentProtectedArchiveSemanticInventoryScript + `
cleanup() {
  status=$?
  trap - EXIT
  trap '' HUP INT TERM
  [ -z "$staging" ] || rm -rf "$staging" || true
  restore_failed=0
  if [ "$replaced" = 1 ] && [ -n "$old" ] && [ -e "$old" ]; then
    if ! rm -rf "$path"; then
      restore_failed=1
    elif mv "$old" "$path"; then
      old=
      replaced=0
    else
      restore_failed=1
    fi
  fi
  if [ "$replaced" != 1 ] && [ -n "$old" ]; then rm -rf "$old" || true; fi
  [ -z "$work" ] || rm -rf "$work" || true
  if [ "$restore_failed" = 1 ]; then
    echo 'protected archive rollback failed; prior tree was retained for recovery' >&2
    exit 1
  fi
  exit "$status"
}
trap cleanup EXIT
arm_signals
mkdir -p "$parent"
defer_signals
work=$(mktemp -d "$parent/.alpineform-archive-work.XXXXXX")
resume_signals
staging="$work/staging"
mkdir "$staging"
manifest="$work/archive.list"
tar -tzf "$cache" >"$manifest"
if [ ! -s "$manifest" ]; then
  echo 'protected archive contains no entries' >&2
  exit 1
fi
while IFS= read -r entry; do
  if [ -z "$entry" ]; then echo 'protected archive contains an empty path' >&2; exit 1; fi
  case "$entry" in
    /*|..|../*|*/..|*/../*) echo 'protected archive contains an unsafe path' >&2; exit 1 ;;
    *[[:space:]\\:]*) echo 'protected archive contains an unsupported path' >&2; exit 1 ;;
  esac
done <"$manifest"
if tar -tvzf "$cache" | awk '{print substr($1,1,1)}' | grep -qvE '^[-d]$'; then
  echo 'protected archive contains a forbidden entry' >&2
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
if [ ! -s "$work/stripped.list" ]; then echo 'protected archive has no entries after stripping' >&2; exit 1; fi
if uniq -d "$work/stripped.list" | grep -q .; then echo 'protected archive entries collide after stripping' >&2; exit 1; fi
if grep -qE '(^|/)\.alpineform-(artifact|manifest)\.sha256$' "$work/stripped.list"; then
  echo 'protected archive contains a reserved metadata path' >&2
  exit 1
fi
tar -xzf "$cache" -C "$staging" --strip-components "$strip"
if find "$staging" -type l -print -quit | grep -q . || find "$staging" ! -type f ! -type d -print -quit | grep -q .; then
  echo 'protected archive extraction produced a forbidden entry' >&2
  exit 1
fi
line_count=$(find "$staging" -mindepth 1 -print | wc -l | tr -d ' ')
nul_count=$(find "$staging" -mindepth 1 -print0 | tr -cd '\000' | wc -c | tr -d ' ')
if [ "$line_count" != "$nul_count" ] || [ "$nul_count" = 0 ]; then
  echo 'protected archive extraction produced unsafe or no entries' >&2
  exit 1
fi
chown -R "$owner:$group" "$staging"
chmod "$mode" "$staging"
(
  cd "$staging"
  find . -type f | LC_ALL=C sort >"$work/files.list"
  : >.alpineform-manifest.sha256
  while IFS= read -r file; do sha256sum "$file" >>.alpineform-manifest.sha256; done <"$work/files.list"
  printf '%s' '` + componentProtectedArchiveMarkerValue + `' >.alpineform-artifact.sha256
  chmod 0600 .alpineform-manifest.sha256 .alpineform-artifact.sha256
)
if write_semantic_inventory "$staging" "$work/staging.semantic" && \
   write_semantic_inventory "$path" "$work/installed.semantic" && \
   cmp -s "$work/staging.semantic" "$work/installed.semantic"; then
  defer_signals
  if ! rm -rf "$work"; then
    if [ -e "$work" ]; then
      resume_signals
      exit 1
    fi
  fi
  work=
  staging=
  resume_signals
  trap - EXIT
  exit 0
fi
defer_signals
old=$(mktemp -d "$parent/.alpineform-archive-old.XXXXXX")
resume_signals
rmdir "$old"
if [ -e "$path" ] || [ -L "$path" ]; then
  defer_signals
  mv "$path" "$old"
  replaced=1
  resume_signals
fi
trap '' HUP INT TERM
mv "$staging" "$path"
staging=
if ! rm -rf "$work"; then
  if [ -e "$work" ]; then exit 1; fi
fi
work=
if ! rm -rf "$old"; then
  if [ -e "$old" ]; then exit 1; fi
fi
old=
replaced=0
trap - EXIT
exit 0
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
	path, digest, protected, err := componentInstallIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	command := backend.Command{Name: "inspect.component_archive", Script: componentArchiveInspectScript, Arguments: []string{path, digest}}
	if protected {
		cachePath := stringValue(node.Desired, "cache_path")
		if err := validateRemoteFilePath(cachePath); err != nil {
			return engine.ObservedResource{}, fmt.Errorf("component archive cache: %w", err)
		}
		strip, ok := node.Desired["strip_components"].(int)
		if !ok || strip < 0 {
			return engine.ObservedResource{}, fmt.Errorf("component archive has invalid strip_components metadata")
		}
		owner := stringValue(node.Desired, "owner")
		group := stringValue(node.Desired, "group")
		mode := stringValue(node.Desired, "mode")
		if !providerAccountPattern.MatchString(owner) || !providerAccountPattern.MatchString(group) || !validMode(mode) {
			return engine.ObservedResource{}, fmt.Errorf("component archive %q has invalid owner, group, or mode metadata", path)
		}
		command.Script = componentProtectedArchiveInspectScript
		command.Arguments = []string{path, cachePath, owner, group, mode, strconv.Itoa(strip)}
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
	if lines[0] != "directory" {
		observed["type"] = lines[0]
		return engine.ObservedResource{Exists: true, Values: observed, Protected: protected}, nil
	}
	wantFields := 7
	if protected {
		wantFields = 8
	}
	if len(lines) != wantFields {
		if protected {
			return engine.ObservedResource{}, fmt.Errorf("inspect protected component archive returned an invalid response")
		}
		return engine.ObservedResource{}, fmt.Errorf("inspect component archive %q returned %d fields, want %d", path, len(lines), wantFields)
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
	statusIndex := 6
	if protected {
		switch lines[6] {
		case "verified":
			observed["content_verified"] = true
		case "unverified":
			observed["content_verified"] = false
		default:
			return engine.ObservedResource{}, fmt.Errorf("inspect protected component archive returned an invalid response")
		}
		statusIndex = 7
	}
	if lines[statusIndex] != "clean" && lines[statusIndex] != "drift" {
		if protected {
			return engine.ObservedResource{}, fmt.Errorf("inspect protected component archive returned an invalid response")
		}
	}
	if lines[statusIndex] != "clean" {
		observed["tree_integrity"] = lines[statusIndex]
	}
	return engine.ObservedResource{Exists: true, Values: observed, Protected: protected}, nil
}

func applyComponentArchive(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	path, digest, protected, err := componentInstallIdentity(node)
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
	command := backend.Command{
		Name: "apply.component_archive", Script: componentArchiveApplyScript,
		Arguments: []string{cachePath, digest, path, owner, group, mode, strconv.Itoa(strip)}, RedactOutput: true,
	}
	if protected {
		command.Script = componentProtectedArchiveApplyScript
		command.Arguments = []string{cachePath, path, owner, group, mode, strconv.Itoa(strip)}
		command.Stdin = []byte(digest + "\n")
		command.RedactStdin = true
	}
	_, err = runner.Run(ctx, command)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	observed, err := inspectComponentArchive(ctx, runner, node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if !componentArchiveObservationVerified(observed, node) {
		return engine.ObservedResource{}, fmt.Errorf("component archive verification failed after apply")
	}
	return observed, nil
}

func componentArchiveObservationVerified(observed engine.ObservedResource, node graph.Node) bool {
	if !observed.Exists || stringValue(observed.Values, "type") != "" {
		return false
	}
	for _, name := range []string{"owner", "group", "mode"} {
		if stringValue(observed.Values, name) != stringValue(node.Desired, name) {
			return false
		}
	}
	if stringValue(observed.Values, "tree_integrity") != stringValue(node.Desired, "tree_integrity") {
		return false
	}
	_, _, protected, err := componentInstallIdentity(node)
	if err != nil {
		return false
	}
	if protected {
		verified, _ := observed.Values["content_verified"].(bool)
		return verified
	}
	return true
}

func deleteComponentArchive(ctx context.Context, runner backend.Runner, step engine.Step) error {
	path := componentDeletePath(step)
	if err := validateRemoteFilePath(path); err != nil {
		return err
	}
	_, err := runner.Run(ctx, backend.Command{Name: "delete.component_archive", Script: componentArchiveDeleteScript, Arguments: []string{path}, RedactOutput: stepIsProtected(step)})
	return err
}

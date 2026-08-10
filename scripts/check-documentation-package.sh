#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFEST="$ROOT_DIR/scripts/documentation-package-files.txt"

usage() {
  printf 'Usage: %s tree DIRECTORY | archive TAR_GZ | snapshot DIRECTORY\n' \
    "${0##*/}" >&2
  exit 2
}

fail() {
  printf 'documentation package check: %s\n' "$*" >&2
  exit 1
}

[[ $# -eq 2 ]] || usage
[[ -s "$MANIFEST" ]] || fail "missing package manifest"
mode=$1
target=$2

check_archive() {
  archive=$1
  [[ -s "$archive" ]] || fail "archive does not exist: $archive"
  work="$(mktemp -d "${TMPDIR:-/tmp}/alpineform-doc-package.XXXXXX")"
  tar -tzf "$archive" >"$work/raw-files"
  if ! awk '
    {
      path = $0
      sub(/^\.\//, "", path)
      if (path ~ /^\//) exit 1
      count = split(path, component, "/")
      for (i = 1; i <= count; i++) {
        if (component[i] == "..") exit 1
      }
    }
  ' "$work/raw-files"; then
    fail "archive contains an unsafe path"
  fi
  sed 's#^\./##' "$work/raw-files" >"$work/files"
  binary_count="$(awk '$0 == "apf" { count++ } END { print count + 0 }' "$work/files")"
  [[ "$binary_count" -eq 1 ]] || fail "archive contains $binary_count root copies of apf"
  manifest_count="$(awk -v expected="scripts/documentation-package-files.txt" '
    $0 == expected { count++ }
    END { print count + 0 }
  ' "$work/files")"
  [[ "$manifest_count" -eq 1 ]] ||
    fail "archive contains $manifest_count copies of scripts/documentation-package-files.txt"
  mkdir "$work/tree"
  tar -xzf "$archive" -C "$work/tree"
  [[ -z "$(find "$work/tree" ! -type f ! -type d -print -quit)" ]] ||
    fail "archive contains an unsupported filesystem entry"
  [[ -f "$work/tree/apf" && ! -L "$work/tree/apf" && -s "$work/tree/apf" ]] ||
    fail "archive does not contain a regular nonempty apf binary"
  [[ -f "$work/tree/scripts/documentation-package-files.txt" && \
    ! -L "$work/tree/scripts/documentation-package-files.txt" ]] ||
    fail "archived documentation package manifest is not a regular file"
  cmp "$MANIFEST" "$work/tree/scripts/documentation-package-files.txt" ||
    fail "archived documentation package manifest differs"
  for required in LICENSE examples/quickstart.apf.hcl; do
    count="$(awk -v expected="$required" '
      $0 == expected { count++ }
      END { print count + 0 }
    ' "$work/files")"
    [[ "$count" -eq 1 ]] || fail "archive contains $count copies of $required"
    [[ -f "$work/tree/$required" && ! -L "$work/tree/$required" && \
      -r "$work/tree/$required" && -s "$work/tree/$required" ]] ||
      fail "archive contains an unreadable or empty $required"
  done
  while IFS= read -r document; do
    [[ -n "$document" && "${document:0:1}" != "#" ]] || continue
    count="$(awk -v expected="$document" '
      $0 == expected { count++ }
      END { print count + 0 }
    ' "$work/files")"
    [[ "$count" -eq 1 ]] || fail "archive contains $count copies of $document"
    [[ -f "$work/tree/$document" && ! -L "$work/tree/$document" && \
      -r "$work/tree/$document" && -s "$work/tree/$document" ]] ||
      fail "archive contains an unreadable or empty $document"
  done <"$MANIFEST"
  rm -rf "$work"
  work=""
}

work=""
cleanup() {
  [[ -z "$work" ]] || rm -rf "$work"
}
trap cleanup EXIT

case "$mode" in
  tree)
    [[ -d "$target" ]] || fail "installed data directory does not exist: $target"
    [[ -z "$(find "$target" ! -type f ! -type d -print -quit)" ]] ||
      fail "installed data contains an unsupported filesystem entry"
    [[ -f "$target/documentation-package-files.txt" && \
      ! -L "$target/documentation-package-files.txt" && \
      -r "$target/documentation-package-files.txt" && \
      -s "$target/documentation-package-files.txt" ]] ||
      fail "installed data omits documentation-package-files.txt"
    cmp "$MANIFEST" "$target/documentation-package-files.txt" ||
      fail "installed documentation package manifest differs"
    while IFS= read -r document; do
      [[ -n "$document" && "${document:0:1}" != "#" ]] || continue
      [[ -f "$target/$document" && ! -L "$target/$document" && \
        -r "$target/$document" && -s "$target/$document" ]] ||
        fail "installed data omits or cannot read $document"
    done <"$MANIFEST"
    for required in LICENSE examples/quickstart.apf.hcl; do
      [[ -f "$target/$required" && ! -L "$target/$required" && \
        -r "$target/$required" && -s "$target/$required" ]] ||
        fail "installed data omits or cannot read $required"
    done
    ;;
  archive)
    check_archive "$target"
    ;;
  snapshot)
    [[ -d "$target" ]] || fail "snapshot directory does not exist: $target"
    shopt -s nullglob
    archives=("$target"/apf_*.tar.gz)
    [[ "${#archives[@]}" -eq 4 ]] ||
      fail "snapshot contains ${#archives[@]} release archives, expected 4"
    for platform in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do
      matches=("$target"/apf_*_"$platform".tar.gz)
      [[ "${#matches[@]}" -eq 1 ]] ||
        fail "snapshot contains ${#matches[@]} archives for $platform, expected 1"
      check_archive "${matches[0]}"
    done
    ;;
  *) usage ;;
esac

printf 'documentation package check passed: %s %s\n' "$mode" "$target"

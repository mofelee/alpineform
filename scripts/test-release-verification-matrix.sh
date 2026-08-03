#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD="$ROOT_DIR/scripts/build-release-verification-matrix.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/alpineform-release-matrix-test.XXXXXX")"

cleanup() {
  rm -rf "$WORK"
}
trap cleanup EXIT

mkdir -p \
  "$WORK/results/release-verification-linux" \
  "$WORK/results/release-verification-darwin-amd64" \
  "$WORK/results/release-verification-darwin-arm64" \
  "$WORK/results/release-verification-alpine-v3.21" \
  "$WORK/results/release-verification-alpine-v3.22" \
  "$WORK/results/release-verification-alpine-v3.23" \
  "$WORK/results/release-verification-alpine-v3.24"
printf '%s\n' 'linux_amd64_installer=yes' 'supply_chain=yes' \
  > "$WORK/results/release-verification-linux/linux.env"
printf '%s\n' 'darwin_amd64_installer=yes' \
  > "$WORK/results/release-verification-darwin-amd64/macos.env"
printf '%s\n' 'darwin_arm64_installer=yes' \
  > "$WORK/results/release-verification-darwin-arm64/macos.env"
for version in 3_21 3_22 3_23 3_24; do
  printf 'alpine_%s_x86_64_quickstart=yes\n' "$version" \
    > "$WORK/results/release-verification-alpine-v${version/_/.}/alpine-v${version/_/.}.env"
done

"$BUILD" "$WORK/results" "$WORK/matrix.md"
! grep -q failed "$WORK/matrix.md"
grep -Fq '| Installer | yes | build-only | yes | yes |' "$WORK/matrix.md"
grep -Fq '| Supply chain | yes | yes | yes | yes |' "$WORK/matrix.md"
for version in 3.21 3.22 3.23 3.24; do
  grep -Fq "| Alpine $version x86_64 quickstart | yes | n/a | n/a | n/a |" \
    "$WORK/matrix.md"
done

mkdir "$WORK/flat"
cp "$WORK/results/release-verification-linux/linux.env" "$WORK/flat/linux.env"
cp "$WORK/results/release-verification-darwin-amd64/macos.env" \
  "$WORK/flat/darwin-amd64.env"
cp "$WORK/results/release-verification-darwin-arm64/macos.env" \
  "$WORK/flat/darwin-arm64.env"
for version in 3.21 3.22 3.23 3.24; do
  cp "$WORK/results/release-verification-alpine-v$version/alpine-v$version.env" \
    "$WORK/flat/alpine-v$version.env"
done
"$BUILD" "$WORK/flat" "$WORK/flat-matrix.md"
! grep -q failed "$WORK/flat-matrix.md"

rm "$WORK/results/release-verification-alpine-v3.21/alpine-v3.21.env"
if "$BUILD" "$WORK/results" "$WORK/incomplete.md" >"$WORK/incomplete.log" 2>&1; then
  printf 'incomplete verification results were accepted\n' >&2
  exit 1
fi
grep -Fq 'contains failed checks' "$WORK/incomplete.log"
grep -Fq '| Alpine 3.21 x86_64 quickstart | failed |' "$WORK/incomplete.md"

printf '%s\n' 'unexpected=yes' \
  > "$WORK/results/release-verification-linux/unknown.env"
if "$BUILD" "$WORK/results" "$WORK/invalid.md" >"$WORK/invalid.log" 2>&1; then
  printf 'unknown verification key was accepted\n' >&2
  exit 1
fi
grep -Fq 'invalid verification result' "$WORK/invalid.log"

printf 'release verification matrix tests passed\n'

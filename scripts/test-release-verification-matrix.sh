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
  "$WORK/results/release-verification-alpine"
printf '%s\n' 'linux_amd64_installer=yes' 'supply_chain=yes' \
  > "$WORK/results/release-verification-linux/linux.env"
printf '%s\n' 'darwin_amd64_installer=yes' \
  > "$WORK/results/release-verification-darwin-amd64/macos.env"
printf '%s\n' 'darwin_arm64_installer=yes' \
  > "$WORK/results/release-verification-darwin-arm64/macos.env"
printf '%s\n' 'alpine_3_24_x86_64_quickstart=yes' \
  > "$WORK/results/release-verification-alpine/alpine.env"

"$BUILD" "$WORK/results" "$WORK/matrix.md"
! grep -q failed "$WORK/matrix.md"
grep -Fq '| Installer | yes | build-only | yes | yes |' "$WORK/matrix.md"
grep -Fq '| Supply chain | yes | yes | yes | yes |' "$WORK/matrix.md"
grep -Fq '| Alpine 3.24 x86_64 quickstart | yes | n/a | n/a | n/a |' \
  "$WORK/matrix.md"

mkdir "$WORK/flat"
cp "$WORK/results/release-verification-linux/linux.env" "$WORK/flat/linux.env"
cp "$WORK/results/release-verification-darwin-amd64/macos.env" \
  "$WORK/flat/darwin-amd64.env"
cp "$WORK/results/release-verification-darwin-arm64/macos.env" \
  "$WORK/flat/darwin-arm64.env"
cp "$WORK/results/release-verification-alpine/alpine.env" "$WORK/flat/alpine.env"
"$BUILD" "$WORK/flat" "$WORK/flat-matrix.md"
! grep -q failed "$WORK/flat-matrix.md"

rm "$WORK/results/release-verification-alpine/alpine.env"
if "$BUILD" "$WORK/results" "$WORK/incomplete.md" >"$WORK/incomplete.log" 2>&1; then
  printf 'incomplete verification results were accepted\n' >&2
  exit 1
fi
grep -Fq 'contains failed checks' "$WORK/incomplete.log"
grep -Fq '| Alpine 3.24 x86_64 quickstart | failed |' "$WORK/incomplete.md"

printf '%s\n' 'unexpected=yes' \
  > "$WORK/results/release-verification-linux/unknown.env"
if "$BUILD" "$WORK/results" "$WORK/invalid.md" >"$WORK/invalid.log" 2>&1; then
  printf 'unknown verification key was accepted\n' >&2
  exit 1
fi
grep -Fq 'invalid verification result' "$WORK/invalid.log"

printf 'release verification matrix tests passed\n'

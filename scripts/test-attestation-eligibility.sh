#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECK="$ROOT_DIR/scripts/check-attestation-eligibility.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/alpineform-attestation-test.XXXXXX")"

cleanup() {
  rm -rf "$WORK"
}
trap cleanup EXIT

APF_REPOSITORY_VISIBILITY=public "$CHECK" >"$WORK/public.log"
grep -Fq 'available for this public repository' "$WORK/public.log"

if APF_REPOSITORY_VISIBILITY=private "$CHECK" >"$WORK/private.log" 2>&1; then
  printf 'private repository passed without Enterprise confirmation\n' >&2
  exit 1
fi
grep -Fq 'require a public repository or confirmed Enterprise Cloud support' \
  "$WORK/private.log"

APF_REPOSITORY_VISIBILITY=private APF_PRIVATE_ATTESTATIONS_ENABLED=true \
  "$CHECK" >"$WORK/enterprise.log"
grep -Fq 'explicitly confirmed' "$WORK/enterprise.log"

if APF_REPOSITORY_VISIBILITY=unknown "$CHECK" >"$WORK/unknown.log" 2>&1; then
  printf 'unknown repository visibility was accepted\n' >&2
  exit 1
fi
grep -Fq 'unsupported repository visibility' "$WORK/unknown.log"

printf 'attestation eligibility tests passed\n'

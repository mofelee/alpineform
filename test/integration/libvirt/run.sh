#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT_DIR="$ROOT_DIR/test/integration/libvirt"
CASES_DIR="$SCRIPT_DIR/cases"
source "$SCRIPT_DIR/alpine-target.sh"

WORK_ROOT="${APF_INTEGRATION_WORKDIR:-$(mktemp -d "${TMPDIR:-/tmp}/alpineform-core-integration.XXXXXX")}"
ARTIFACT_ROOT="${APF_INTEGRATION_ARTIFACT_DIR:-${TMPDIR:-/tmp}/alpineform-core-integration-artifacts}"
INPUT_APF_BIN="${APF_INTEGRATION_APF_BIN:-}"
APF_BIN="$WORK_ROOT/apf"
BASE_IMAGE="$WORK_ROOT/$APF_INTEGRATION_CLOUD_IMAGE"

log() {
  printf '[integration] %s\n' "$*"
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'required command not found: %s\n' "$1" >&2
    exit 1
  }
}

cleanup() {
  local status=$?
  trap - EXIT
  if [[ "${APF_INTEGRATION_KEEP_WORKDIR:-0}" != "1" ]]; then
    rm -rf "$WORK_ROOT"
  else
    log "preserving work directory: $WORK_ROOT"
  fi
  exit "$status"
}
trap cleanup EXIT

for command in curl python3 sha512sum ssh ssh-keygen virsh; do
  require_command "$command"
done
if [[ -z "$INPUT_APF_BIN" ]]; then
  require_command go
fi
if [[ -z "${APF_LIBVIRT_URI:-${VIRSH_DEFAULT_CONNECT_URI:-${LIBVIRT_DEFAULT_URI:-}}}" ]]; then
  for command in cloud-localds qemu-img sudo; do
    require_command "$command"
  done
fi
if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 ]]; then
  printf 'libvirt integration requires Linux x86_64\n' >&2
  exit 1
fi

mkdir -p "$WORK_ROOT" "$ARTIFACT_ROOT"
chmod 0755 "$WORK_ROOT"

if [[ -n "$INPUT_APF_BIN" ]]; then
  [[ -x "$INPUT_APF_BIN" ]] || {
    printf 'integration apf binary is not executable: %s\n' "$INPUT_APF_BIN" >&2
    exit 1
  }
  log "using supplied apf binary"
  cp "$INPUT_APF_BIN" "$APF_BIN"
else
  log "building apf"
  (
    cd "$ROOT_DIR"
    go build -trimpath -o "$APF_BIN" ./cmd/apf
  )
fi

log "verifying pinned Alpine $APF_INTEGRATION_ALPINE_VERSION cloud image metadata"
published_sha="$(curl --fail --location --retry 3 --show-error --silent \
  "$APF_INTEGRATION_CLOUD_URL/$APF_INTEGRATION_CLOUD_IMAGE.sha512" | awk '{print $1; exit}')"
if [[ "$published_sha" != "$APF_INTEGRATION_CLOUD_IMAGE_SHA512" ]]; then
  printf 'published Alpine image SHA-512 differs from the pinned value\n' >&2
  exit 1
fi

IMAGE_CACHE_DIR="${APF_INTEGRATION_IMAGE_CACHE:-}"
CACHED_IMAGE="${IMAGE_CACHE_DIR:+$IMAGE_CACHE_DIR/$APF_INTEGRATION_CLOUD_IMAGE}"
if [[ -n "$CACHED_IMAGE" && -f "$CACHED_IMAGE" ]] &&
  printf '%s  %s\n' "$APF_INTEGRATION_CLOUD_IMAGE_SHA512" "$CACHED_IMAGE" | sha512sum --check --status; then
  log "using cached Alpine image from $IMAGE_CACHE_DIR"
  cp "$CACHED_IMAGE" "$BASE_IMAGE"
else
  log "downloading official Alpine cloud image"
  curl --fail --location --retry 3 --show-error --silent \
    "$APF_INTEGRATION_CLOUD_URL/$APF_INTEGRATION_CLOUD_IMAGE" --output "$BASE_IMAGE"
fi
printf '%s  %s\n' "$APF_INTEGRATION_CLOUD_IMAGE_SHA512" "$BASE_IMAGE" | sha512sum --check

if [[ -n "$CACHED_IMAGE" ]] &&
  { [[ ! -f "$CACHED_IMAGE" ]] || ! printf '%s  %s\n' "$APF_INTEGRATION_CLOUD_IMAGE_SHA512" "$CACHED_IMAGE" | sha512sum --check --status; }; then
  mkdir -p "$IMAGE_CACHE_DIR"
  cp "$BASE_IMAGE" "$CACHED_IMAGE.partial"
  mv "$CACHED_IMAGE.partial" "$CACHED_IMAGE"
fi
chmod 0644 "$BASE_IMAGE"

declare -a CASE_DIRS=()
if [[ -n "${APF_INTEGRATION_CASE_SOURCE:-}" ]]; then
  case_dir="$(cd "$APF_INTEGRATION_CASE_SOURCE" && pwd)"
  CASE_DIRS+=("$case_dir")
elif [[ -n "${APF_INTEGRATION_CASE:-}" ]]; then
  case_dir="$CASES_DIR/$APF_INTEGRATION_CASE"
  [[ -d "$case_dir" ]] || {
    printf 'integration case not found: %s\n' "$APF_INTEGRATION_CASE" >&2
    exit 1
  }
  CASE_DIRS+=("$case_dir")
else
  while IFS= read -r case_dir; do
    CASE_DIRS+=("$case_dir")
  done < <(find "$CASES_DIR" -mindepth 1 -maxdepth 1 -type d | sort)
fi
(( ${#CASE_DIRS[@]} > 0 )) || {
  printf 'no integration cases found under %s\n' "$CASES_DIR" >&2
  exit 1
}

for case_dir in "${CASE_DIRS[@]}"; do
  case_name="$(basename "$case_dir")"
  log "running $case_name in a fresh Alpine $APF_INTEGRATION_ALPINE_VERSION VM"
  APF_INTEGRATION_APF_BIN="$APF_BIN" \
  APF_INTEGRATION_BASE_IMAGE="$BASE_IMAGE" \
  APF_INTEGRATION_CASE_WORK="$WORK_ROOT/cases/$case_name" \
  APF_INTEGRATION_CASE_ARTIFACTS="$ARTIFACT_ROOT/$case_name" \
    "$SCRIPT_DIR/run-case.sh" "$case_dir"
done

log "all integration cases passed"

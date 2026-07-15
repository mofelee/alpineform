#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT_DIR="$ROOT_DIR/test/integration/libvirt"
CASES_DIR="$SCRIPT_DIR/cases"
EXPECTED_CASE_COUNT=11
APF_BIN="${APF_INTEGRATION_APF_BIN:-}"
TEMP_APF=""
TEMP_PLAN=""

cleanup() {
  [[ -z "$TEMP_APF" ]] || rm -f "$TEMP_APF"
  [[ -z "$TEMP_PLAN" ]] || rm -f "$TEMP_PLAN"
}
trap cleanup EXIT

for script in alpine-target.sh network.sh run.sh run-case.sh validate-cases.sh; do
  bash -n "$SCRIPT_DIR/$script"
done
python3 -c 'compile(open("test/integration/libvirt/assert-noop-plan.py", encoding="utf-8").read(), "assert-noop-plan.py", "exec")'

target="$(bash "$SCRIPT_DIR/alpine-target.sh")"
grep -qx 'version=3.24.1' <<<"$target"
grep -qx 'architecture=x86_64' <<<"$target"
grep -qx 'platform_architecture=amd64' <<<"$target"
grep -qx 'cloud_image=generic_alpine-3.24.1-x86_64-uefi-cloudinit-r0.qcow2' <<<"$target"
grep -qx 'sha512=ed976ef40de1f73adcb0a3b253ec9e73e43c408208fcc3c30dcdf7a69b91a387a4777f88c6b72345123edf3832d7cb49403ecce28ec84d496d4b3bad6fbd0923' <<<"$target"

if [[ -z "$APF_BIN" ]]; then
  TEMP_APF="$(mktemp "${TMPDIR:-/tmp}/apf-integration-layout.XXXXXX")"
  (
    cd "$ROOT_DIR"
    go build -o "$TEMP_APF" ./cmd/apf
  )
  APF_BIN="$TEMP_APF"
fi

TEMP_PLAN="$(mktemp "${TMPDIR:-/tmp}/apf-noop-plan.XXXXXX.json")"
printf '%s\n' '{"format_version":"alpineform.plan.alpha1","summary":{"create":0,"update":0,"no_op":1}}' >"$TEMP_PLAN"
python3 "$SCRIPT_DIR/assert-noop-plan.py" "$TEMP_PLAN"
printf '%s\n' '{"format_version":"alpineform.plan.alpha1","summary":{"create":0,"update":1,"no_op":0}}' >"$TEMP_PLAN"
if python3 "$SCRIPT_DIR/assert-noop-plan.py" "$TEMP_PLAN" >/dev/null 2>&1; then
  printf 'assert-noop-plan.py accepted an update plan\n' >&2
  exit 1
fi

failed=0
case_count=0
while IFS= read -r case_dir; do
  case_count=$((case_count + 1))
  case_name="$(basename "$case_dir")"
  if [[ -f "$case_dir/.allow-network-disruption" && "$case_name" != nftables ]]; then
    printf '%s: only the nftables case may pre-authorize network disruption\n' "$case_name" >&2
    failed=1
  fi
  if [[ "$case_name" == nftables && ! -f "$case_dir/.allow-network-disruption" ]]; then
    printf 'nftables: missing explicit network disruption case marker\n' >&2
    failed=1
  fi
  configs=()
  next_step=1
  while [[ -f "$case_dir/$next_step.apf.hcl" ]]; do
    configs+=("$case_dir/$next_step.apf.hcl")
    next_step=$((next_step + 1))
  done
  config_count="$(find "$case_dir" -maxdepth 1 -type f -name '[0-9]*.apf.hcl' | wc -l | tr -d '[:space:]')"
  if (( config_count != ${#configs[@]} || config_count == 0 )); then
    printf '%s: numbered configs must start at 1 and be contiguous\n' "$case_name" >&2
    failed=1
    continue
  fi
  drift_count=0
  for config in "${configs[@]}"; do
    step="$(basename "$config" .apf.hcl)"
    check_hook="$case_dir/$step.check.sh"
    if [[ ! -f "$check_hook" ]]; then
      printf '%s: missing %s.check.sh\n' "$case_name" "$step" >&2
      failed=1
      continue
    fi
    bash -n "$check_hook"
    if ! grep -q 'assert_remote' "$check_hook"; then
      printf '%s: %s.check.sh must contain assert_remote checks\n' "$case_name" "$step" >&2
      failed=1
    fi
    if [[ -f "$case_dir/$step.drift.sh" ]]; then
      drift_count=$((drift_count + 1))
      bash -n "$case_dir/$step.drift.sh"
    fi
    if ! grep -q '__APF_VM_HOST__' "$config" ||
      ! grep -q 'architecture = "amd64"' "$config" ||
      ! grep -q 'version      = "3.24.1"' "$config"; then
      printf '%s: %s must pin the VM host and offline platform facts\n' "$case_name" "$(basename "$config")" >&2
      failed=1
    fi
    validation="$($APF_BIN validate -f "$config")"
    printf '[layout:%s:%s] %s\n' "$case_name" "$step" "$validation"
  done
  if (( drift_count == 0 )); then
    printf '%s: requires at least one drift hook\n' "$case_name" >&2
    failed=1
  fi
  for hook in prepare.sh negative.sh; do
    [[ ! -f "$case_dir/$hook" ]] || bash -n "$case_dir/$hook"
  done
done < <(find "$CASES_DIR" -mindepth 1 -maxdepth 1 -type d | sort)

if (( case_count != EXPECTED_CASE_COUNT )); then
  printf 'expected %d integration cases, found %d\n' "$EXPECTED_CASE_COUNT" "$case_count" >&2
  exit 1
fi
if find "$CASES_DIR" -type f \( -name 'id_*' -o -name '*.key' -o -name '*.state.json' \) -print -quit | grep -q .; then
  printf 'integration cases must not contain keys or state files\n' >&2
  exit 1
fi
exit "$failed"

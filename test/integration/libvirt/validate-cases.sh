#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT_DIR="$ROOT_DIR/test/integration/libvirt"
CASES_DIR="$SCRIPT_DIR/cases"
EXPECTED_CASE_COUNT=12
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
for helper in assert-moved-plan.py assert-noop-plan.py assert-source-rebuild-plan.py; do
  python3 -c 'import sys; compile(open(sys.argv[1], encoding="utf-8").read(), sys.argv[1], "exec")' "$SCRIPT_DIR/$helper"
done

while read -r branch version sha512; do
  target="$(APF_INTEGRATION_ALPINE_BRANCH="$branch" bash "$SCRIPT_DIR/alpine-target.sh")"
  grep -qx "version=$version" <<<"$target"
  grep -qx "branch=$branch" <<<"$target"
  grep -qx 'architecture=x86_64' <<<"$target"
  grep -qx 'platform_architecture=amd64' <<<"$target"
  grep -qx "cloud_image=generic_alpine-${version}-x86_64-uefi-cloudinit-r0.qcow2" <<<"$target"
  grep -qx "sha512=$sha512" <<<"$target"
done <<'TARGETS'
v3.21 3.21.7 612691a05c8ea3181b08d11a33572bc06ad3c2679760f6b20c5525dfcdf47f99f597ea485b3e7a4b533612aa283e52468c6b51a05a1e561c13b32ab59f6ec821
v3.22 3.22.5 132c8f0f3926c2c63e389b251c144e173472049b39c2527e7b6bf3692bafdd17c09e6d2897f7cffcfaf9256ba2ca6de2545992ef2c0b9cca12e2548265954ab4
v3.23 3.23.5 7f8818009bb80fb72c81e3dcb8a8aa4c55eda24606e571159e0c3ecaf521fd14d7fbdca06ce55f746db8c19a62302fbfda6b29fb6791619a21648edd5f340e31
v3.24 3.24.1 ed976ef40de1f73adcb0a3b253ec9e73e43c408208fcc3c30dcdf7a69b91a387a4777f88c6b72345123edf3832d7cb49403ecce28ec84d496d4b3bad6fbd0923
TARGETS

if APF_INTEGRATION_ALPINE_BRANCH=v3.20 bash "$SCRIPT_DIR/alpine-target.sh" >/dev/null 2>&1; then
  printf 'alpine-target.sh accepted unsupported v3.20\n' >&2
  exit 1
fi

if [[ -z "$APF_BIN" ]]; then
  TEMP_APF="$(mktemp "${TMPDIR:-/tmp}/apf-integration-layout.XXXXXX")"
  (
    cd "$ROOT_DIR"
    go build -o "$TEMP_APF" ./cmd/apf
  )
  APF_BIN="$TEMP_APF"
fi

TEMP_PLAN="$(mktemp "${TMPDIR:-/tmp}/apf-plan-assertions.XXXXXX.json")"

expect_rejected() {
  local description=$1
  shift
  if "$@" >/dev/null 2>&1; then
    printf '%s\n' "$description" >&2
    exit 1
  fi
}

cat >"$TEMP_PLAN" <<'JSON'
{
  "format_version": "alpineform.plan.alpha1",
  "summary": {
    "move": 0,
    "create": 0,
    "update": 0,
    "delete": 0,
    "no_op": 1
  },
  "moves": [],
  "changes": [
    {"address": "host.cihost.file.noop", "action": "no-op"}
  ]
}
JSON
python3 "$SCRIPT_DIR/assert-noop-plan.py" "$TEMP_PLAN"

cat >"$TEMP_PLAN" <<'JSON'
{
  "format_version": "alpineform.plan.alpha1",
  "summary": {
    "move": 1,
    "create": 0,
    "update": 0,
    "delete": 0,
    "no_op": 1
  },
  "moves": [],
  "changes": [
    {"address": "host.cihost.file.noop", "action": "no-op"}
  ]
}
JSON
expect_rejected \
  'assert-noop-plan.py accepted summary.move=1' \
  python3 "$SCRIPT_DIR/assert-noop-plan.py" "$TEMP_PLAN"

cat >"$TEMP_PLAN" <<'JSON'
{
  "format_version": "alpineform.plan.alpha1",
  "summary": {
    "move": 0,
    "create": 0,
    "update": 0,
    "delete": 0,
    "no_op": 1
  },
  "moves": [
    {"host": "cihost", "from": "host.cihost.component.old.file.noop", "to": "host.cihost.component.current.file.noop"}
  ],
  "changes": [
    {"address": "host.cihost.file.noop", "action": "no-op"}
  ]
}
JSON
expect_rejected \
  'assert-noop-plan.py accepted a nonempty moves array' \
  python3 "$SCRIPT_DIR/assert-noop-plan.py" "$TEMP_PLAN"

cat >"$TEMP_PLAN" <<'JSON'
{
  "format_version": "alpineform.plan.alpha1",
  "summary": {
    "move": 4,
    "create": 0,
    "update": 2,
    "delete": 0,
    "no_op": 2,
    "managed_resources": 4,
    "graph_nodes": 4
  },
  "moves": [
    {
      "host": "cihost",
      "from": "host.cihost.component.legacy_builder.build.input[\"source\"]",
      "to": "host.cihost.component.builder.build.input[\"source\"]"
    },
    {
      "host": "cihost",
      "from": "host.cihost.component.legacy_builder.build.install[\"/usr/local/bin/apf-moved-builder\"]",
      "to": "host.cihost.component.builder.build.install[\"/usr/local/bin/apf-moved-builder\"]"
    },
    {
      "host": "cihost",
      "from": "host.cihost.component.legacy_worker.file[\"/etc/alpineform-moved/worker.conf\"]",
      "to": "host.cihost.component.worker.file[\"/etc/alpineform-moved/worker.conf\"]"
    },
    {
      "host": "cihost",
      "from": "host.cihost.component.legacy_worker.script[\"reload_worker\"]",
      "to": "host.cihost.component.worker.script[\"reload_worker\"]"
    }
  ],
  "changes": [
    {"address": "host.cihost.component.builder.build.input[\"source\"]", "action": "no-op"},
    {"address": "host.cihost.component.builder.build.install[\"/usr/local/bin/apf-moved-builder\"]", "action": "no-op"},
    {"address": "host.cihost.component.worker.file[\"/etc/alpineform-moved/worker.conf\"]", "action": "update"},
    {
      "address": "host.cihost.component.worker.script[\"reload_worker\"]",
      "action": "update",
      "triggered_by": ["host.cihost.component.worker.file[\"/etc/alpineform-moved/worker.conf\"]"]
    }
  ]
}
JSON
MOVED_PLAN_ARGS=(
  "$TEMP_PLAN"
  --host cihost
  --move 'host.cihost.component.legacy_worker.file["/etc/alpineform-moved/worker.conf"]' 'host.cihost.component.worker.file["/etc/alpineform-moved/worker.conf"]'
  --move 'host.cihost.component.legacy_worker.script["reload_worker"]' 'host.cihost.component.worker.script["reload_worker"]'
  --move 'host.cihost.component.legacy_builder.build.input["source"]' 'host.cihost.component.builder.build.input["source"]'
  --move 'host.cihost.component.legacy_builder.build.install["/usr/local/bin/apf-moved-builder"]' 'host.cihost.component.builder.build.install["/usr/local/bin/apf-moved-builder"]'
  --update-address 'host.cihost.component.worker.file["/etc/alpineform-moved/worker.conf"]'
  --update-address 'host.cihost.component.worker.script["reload_worker"]'
  --trigger 'host.cihost.component.worker.script["reload_worker"]' 'host.cihost.component.worker.file["/etc/alpineform-moved/worker.conf"]'
)
python3 "$SCRIPT_DIR/assert-moved-plan.py" "${MOVED_PLAN_ARGS[@]}"
expect_rejected \
  'assert-moved-plan.py accepted an incomplete mapping set' \
  python3 "$SCRIPT_DIR/assert-moved-plan.py" \
  "$TEMP_PLAN" \
  --host cihost \
  --move 'host.cihost.component.legacy_worker.file["/etc/alpineform-moved/worker.conf"]' 'host.cihost.component.worker.file["/etc/alpineform-moved/worker.conf"]' \
  --move 'host.cihost.component.legacy_worker.script["reload_worker"]' 'host.cihost.component.worker.script["reload_worker"]' \
  --move 'host.cihost.component.legacy_builder.build.input["source"]' 'host.cihost.component.builder.build.input["source"]' \
  --update-address 'host.cihost.component.worker.file["/etc/alpineform-moved/worker.conf"]' \
  --update-address 'host.cihost.component.worker.script["reload_worker"]' \
  --trigger 'host.cihost.component.worker.script["reload_worker"]' 'host.cihost.component.worker.file["/etc/alpineform-moved/worker.conf"]'

cat >"$TEMP_PLAN" <<'JSON'
{
  "format_version": "alpineform.plan.alpha1",
  "summary": {
    "move": 0,
    "create": 5,
    "update": 1,
    "delete": 0,
    "no_op": 2,
    "managed_resources": 8,
    "graph_nodes": 8
  },
  "moves": [],
  "changes": [
    {"address": "host.cihost.component.builder.build.input[\"source\"]", "action": "create", "summary": "rebuild: stage input"},
    {"address": "host.cihost.component.builder.build.dependencies", "action": "create", "summary": "rebuild: own dependencies"},
    {"address": "host.cihost.component.builder.build.workspace", "action": "create", "summary": "rebuild: execute build"},
    {"address": "host.cihost.component.builder.build.output[\"build/builder\"]", "action": "create", "summary": "rebuild: verify output"},
    {"address": "host.cihost.component.builder.build.cleanup", "action": "create", "summary": "rebuild: clean workspace"},
    {"address": "host.cihost.component.builder.build.install[\"/usr/local/bin/apf-moved-builder\"]", "action": "update", "summary": "rebuild: install output"},
    {"address": "host.cihost.component.worker.package[\"jq\"]", "action": "no-op", "summary": "manage package"},
    {"address": "host.cihost.component.worker.file[\"/etc/alpineform-moved/worker.conf\"]", "action": "no-op", "summary": "manage file"}
  ]
}
JSON
SOURCE_REBUILD_ARGS=(
  "$TEMP_PLAN"
  --create-address 'host.cihost.component.builder.build.input["source"]'
  --create-address 'host.cihost.component.builder.build.dependencies'
  --create-address 'host.cihost.component.builder.build.workspace'
  --create-address 'host.cihost.component.builder.build.output["build/builder"]'
  --create-address 'host.cihost.component.builder.build.cleanup'
  --update-address 'host.cihost.component.builder.build.install["/usr/local/bin/apf-moved-builder"]'
  --no-op-count 2
)
python3 "$SCRIPT_DIR/assert-source-rebuild-plan.py" "${SOURCE_REBUILD_ARGS[@]}"
python3 - "$TEMP_PLAN" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as plan_file:
    document = json.load(plan_file)
document["changes"][0]["summary"] = "repair: own dependencies"
with open(path, "w", encoding="utf-8") as plan_file:
    json.dump(document, plan_file)
PY
expect_rejected \
  'assert-source-rebuild-plan.py accepted a non-rebuild action summary' \
  python3 "$SCRIPT_DIR/assert-source-rebuild-plan.py" "${SOURCE_REBUILD_ARGS[@]}"

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

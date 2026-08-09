OLD_WORKER_ROOT=host.cihost.component.legacy_worker
WORKER_ROOT=host.cihost.component.worker
OLD_BUILDER_ROOT=host.cihost.component.legacy_builder
BUILDER_ROOT=host.cihost.component.builder

WORKER_SUFFIXES=(
  '.artifact.install["/usr/local/bin/apf-moved-worker"]'
  '.artifact.source["amd64"]'
  '.directories.directory["/etc/alpineform-moved"]'
  '.files.file["/etc/alpineform-moved/worker.conf"]'
  '.files.file["/etc/conf.d/apf-moved-worker"]'
  '.files.file["/etc/init.d/apf-moved-worker"]'
  '.groups.group["apfmoved"]'
  '.packages.package["jq"]'
  '.script["reload_worker"]'
  '.services.service["apf-moved-worker"]'
  '.users.user["apfmoved"]'
)
BUILDER_SUFFIXES=(
  '.build.cleanup'
  '.build.dependencies'
  '.build.input["source"]'
  '.build.input["verify_environment"]'
  '.build.install["/usr/local/bin/apf-moved-builder"]'
  '.build.output["build/builder"]'
  '.build.workspace'
)

assert_exact_rename_plan() {
  local plan=$1 suffix
  local -a arguments=("$plan" --host cihost)
  for suffix in "${WORKER_SUFFIXES[@]}"; do
    arguments+=(--move "$OLD_WORKER_ROOT$suffix" "$WORKER_ROOT$suffix")
  done
  for suffix in "${BUILDER_SUFFIXES[@]}"; do
    arguments+=(--move "$OLD_BUILDER_ROOT$suffix" "$BUILDER_ROOT$suffix")
  done
  arguments+=(
    --update-address "$WORKER_ROOT.files.file[\"/etc/alpineform-moved/worker.conf\"]"
    --update-address "$WORKER_ROOT.script[\"reload_worker\"]"
    --trigger "$WORKER_ROOT.script[\"reload_worker\"]" "$WORKER_ROOT.files.file[\"/etc/alpineform-moved/worker.conf\"]"
  )
  python3 "$SCRIPT_DIR/assert-moved-plan.py" "${arguments[@]}"
}

assert_exact_move_only_plan() {
  local plan=$1 suffix
  local -a arguments=("$plan" --host cihost)
  for suffix in "${WORKER_SUFFIXES[@]}"; do
    arguments+=(--move "$OLD_WORKER_ROOT$suffix" "$WORKER_ROOT$suffix")
  done
  for suffix in "${BUILDER_SUFFIXES[@]}"; do
    arguments+=(--move "$OLD_BUILDER_ROOT$suffix" "$BUILDER_ROOT$suffix")
  done
  python3 "$SCRIPT_DIR/assert-moved-plan.py" "${arguments[@]}"
}

assert_exact_source_rebuild_plan() {
  local plan=$1
  python3 "$SCRIPT_DIR/assert-source-rebuild-plan.py" "$plan" \
    --create-address "$BUILDER_ROOT.build.input[\"source\"]" \
    --create-address "$BUILDER_ROOT.build.dependencies" \
    --create-address "$BUILDER_ROOT.build.workspace" \
    --create-address "$BUILDER_ROOT.build.output[\"build/builder\"]" \
    --create-address "$BUILDER_ROOT.build.cleanup" \
    --update-address "$BUILDER_ROOT.build.install[\"/usr/local/bin/apf-moved-builder\"]" \
    --no-op-count 12
}

assert_exact_teardown_plan() {
  local plan=$1
  python3 - "$plan" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as plan_file:
    document = json.load(plan_file)
worker = "host.cihost.component.worker"
builder = "host.cihost.component.builder"
expected = {
    "delete": {
        worker + '.artifact.source["amd64"]',
    },
    "destroy": {
        worker + '.artifact.install["/usr/local/bin/apf-moved-worker"]',
        worker + '.directories.directory["/etc/alpineform-moved"]',
        worker + '.files.file["/etc/alpineform-moved/worker.conf"]',
        worker + '.groups.group["apfmoved"]',
        worker + '.users.user["apfmoved"]',
        builder + '.build.dependencies',
        builder + '.build.input["source"]',
        builder + '.build.input["verify_environment"]',
        builder + '.build.install["/usr/local/bin/apf-moved-builder"]',
        builder + '.build.output["build/builder"]',
        builder + '.build.workspace',
    },
    "forget": {
        worker + '.files.file["/etc/conf.d/apf-moved-worker"]',
        worker + '.files.file["/etc/init.d/apf-moved-worker"]',
        worker + '.packages.package["jq"]',
        worker + '.script["reload_worker"]',
        worker + '.services.service["apf-moved-worker"]',
        builder + '.build.cleanup',
    },
}
actual = {action: set() for action in expected}
for change in document.get("changes", []):
    action = change.get("action")
    if action not in actual:
        raise SystemExit(f"unexpected teardown action: {change!r}")
    actual[action].add(change.get("address"))
if actual != expected:
    raise SystemExit(f"unexpected teardown actions: expected {expected!r}, got {actual!r}")
summary = document.get("summary", {})
expected_summary = {
    "move": 0,
    "create": 0,
    "update": 0,
    "adopt": 0,
    "delete": 1,
    "destroy": 11,
    "forget": 6,
    "no_op": 0,
}
for name, count in expected_summary.items():
    if summary.get(name, 0) != count:
        raise SystemExit(f"expected summary.{name}={count}, got {summary.get(name, 0)!r}")
if summary.get("managed_resources") != 18 or summary.get("graph_nodes") != 18:
    raise SystemExit(f"unexpected teardown resource counts: {summary!r}")
if document.get("moves") != []:
    raise SystemExit(f"teardown unexpectedly realized moves: {document.get('moves')!r}")
PY
}

assert_component_state() {
  local description=$1 worker_root=$2 builder_root=$3 expect_physical=$4
  ASSERTION_COUNT=$((ASSERTION_COUNT + 1))
  log "ASSERT $ASSERTION_COUNT: $description"
  ssh_vm python3 - "$worker_root" "$builder_root" "$expect_physical" <<'PY' || fail "$description"
import json
import sys

worker_root, builder_root, expect_physical = sys.argv[1:]
worker_suffixes = (
    '.artifact.install["/usr/local/bin/apf-moved-worker"]',
    '.artifact.source["amd64"]',
    '.directories.directory["/etc/alpineform-moved"]',
    '.files.file["/etc/alpineform-moved/worker.conf"]',
    '.files.file["/etc/conf.d/apf-moved-worker"]',
    '.files.file["/etc/init.d/apf-moved-worker"]',
    '.groups.group["apfmoved"]',
    '.packages.package["jq"]',
    '.script["reload_worker"]',
    '.services.service["apf-moved-worker"]',
    '.users.user["apfmoved"]',
)
builder_suffixes = (
    '.build.cleanup',
    '.build.dependencies',
    '.build.input["source"]',
    '.build.input["verify_environment"]',
    '.build.install["/usr/local/bin/apf-moved-builder"]',
    '.build.output["build/builder"]',
    '.build.workspace',
)
expected = {worker_root + suffix for suffix in worker_suffixes}
expected.update(builder_root + suffix for suffix in builder_suffixes)
with open("/var/lib/alpineform/state.json", encoding="utf-8") as state_file:
    state = json.load(state_file)
if state.get("product") != "alpineform" or state.get("schema_version") != 3 or state.get("host") != "cihost":
    raise SystemExit(
        "unexpected state header: "
        f"product={state.get('product')!r}, "
        f"schema_version={state.get('schema_version')!r}, "
        f"host={state.get('host')!r}, serial={state.get('serial')!r}"
    )
resources = state.get("resources")
if not isinstance(resources, dict) or set(resources) != expected:
    raise SystemExit(f"unexpected resources: expected {sorted(expected)!r}, got {sorted(resources or {})!r}")
identities = state.get("component_identities", {})
expected_identities = {}
if expect_physical == "yes":
    expected_identities = {
        worker_root: {"physical_name": "legacy_worker"},
        builder_root: {"physical_name": "legacy_builder"},
    }
if identities != expected_identities:
    raise SystemExit(f"unexpected component identities: {identities!r}")
PY
}

assert_no_duplicate_physical_ownership() {
  assert_remote "prebuilt artifact cache remains in the legacy worker namespace only" \
    "test -f '/var/cache/alpineform/components/legacy_worker/$APF_MOVED_WORKER_SHA/artifact' && test ! -e /var/cache/alpineform/components/worker && test \"\$(find /var/cache/alpineform/components -type f -name artifact | wc -l | tr -d ' ')\" = 1"
  assert_remote "source build retains one legacy owner marker and no active temporary owner" \
    "test -f '/var/lib/alpineform/builds/$APF_MOVED_BUILDER_OWNER.installed' && test ! -e '/var/lib/alpineform/builds/$APF_MOVED_CURRENT_BUILDER_OWNER.installed' && test \"\$(find /var/lib/alpineform/builds -type f -name '*.installed' | wc -l | tr -d ' ')\" = 1 && test ! -e '/var/lib/alpineform/builds/$APF_MOVED_BUILDER_OWNER.dependencies' && ! apk info | grep -Eq '^\\.alpineform-build-'"
  assert_remote "source build leaves one verified output and no workspace" \
    "test \"\$(find /var/cache/alpineform/builds/outputs -type f -name artifact | wc -l | tr -d ' ')\" = 1 && test -z \"\$(find /var/tmp/alpineform/builds -mindepth 1 -print -quit 2>/dev/null)\""
}

assert_and_record_worker_runtime() {
  if [[ "$APF_TEST_PHASE" != rebooted ]] && ssh_vm test -f /var/lib/alpineform-moved/service.pid; then
    assert_remote "component refactor did not restart the OpenRC worker" \
      "test \"\$(cat /run/apf-moved-worker.pid)\" = \"\$(cat /var/lib/alpineform-moved/service.pid)\""
  fi
  run_remote "record the worker process for the next configuration step" \
    "cat /run/apf-moved-worker.pid > /var/lib/alpineform-moved/service.pid"
}

assert_worker_invariants() {
  assert_remote "worker native resources remain converged without duplicate ownership" \
    "test \"\$(getent group apfmoved | cut -d: -f3)\" = 2401 && test \"\$(getent passwd apfmoved | cut -d: -f3)\" = 2401 && test \"\$(getent passwd apfmoved | cut -d: -f6)\" = /var/lib/alpineform-moved/home && test -d /var/lib/alpineform-moved/home && test \"\$(stat -c '%U:%G' /var/lib/alpineform-moved/home)\" = apfmoved:apfmoved && test \"\$(stat -c '%U:%G' /var/empty)\" = root:root && permissions=\$(stat -c '%a' /var/empty) && test \"\$((0\$permissions & 022))\" -eq 0 && apk info -e jq && test \"\$(grep -xc jq /etc/apk/world)\" = 1"
  assert_remote "worker artifact and OpenRC service remain converged" \
    "test \"\$(sha256sum /usr/local/bin/apf-moved-worker | awk '{print \$1}')\" = '$APF_MOVED_WORKER_SHA' && rc-update show default | grep -Eq '(^|[[:space:]])apf-moved-worker([[:space:]]|$)' && rc-service apf-moved-worker status >/dev/null && test -s /run/apf-moved-worker.pid"
  assert_remote "generated OpenRC files remain singular and unchanged" \
    "test \"\$(grep -Fc \"command='/usr/local/bin/apf-moved-worker'\" /etc/init.d/apf-moved-worker)\" = 1 && grep -qx 'APF_MOVED_WORKER=enabled' /etc/conf.d/apf-moved-worker"
}

capture_move_readonly_snapshot() {
  local path=$1
  run_remote "capture component move read-only snapshot" \
    "{ sha256sum /var/lib/alpineform/state.json /usr/local/bin/apf-moved-worker /usr/local/bin/apf-moved-builder /etc/alpineform-moved/worker.conf /etc/init.d/apf-moved-worker /etc/conf.d/apf-moved-worker; stat -c '%d:%i:%U:%G:%a' /usr/local/bin/apf-moved-worker /usr/local/bin/apf-moved-builder /etc/alpineform-moved/worker.conf; cat /run/apf-moved-worker.pid /var/lib/alpineform-moved/reload.count /var/lib/alpineform-moved/last-trigger; getent passwd apfmoved; getent group apfmoved; grep -x jq /etc/apk/world; apk info -e jq; find /var/cache/alpineform/components /var/cache/alpineform/builds /var/lib/alpineform/builds /var/lib/alpineform/scripts -type f 2>/dev/null | sort | while IFS= read -r file; do printf '%s ' \"\$file\"; sha256sum \"\$file\"; done; } > '$path'"
}

PACKAGE_ADDRESS='host.cihost.packages.package["jq"]'
CONFIG_ADDRESS='host.cihost.files.file["/etc/alpineform-dependency.json"]'
RAW_INIT_ADDRESS='host.cihost.files.file["/etc/init.d/apf-ci-raw"]'
RAW_SERVICE_ADDRESS='host.cihost.services.service["apf-ci-raw"]'

assert_dependency_plan() {
  local plan=$1 stage=$2
  assert_local "dependency $stage plan has exact actions, relationships, and order" \
    python3 "$CASE_DIR/assert-dependency.py" plan "$plan" "$stage"
}

assert_dependency_state() {
  local mode=$1 description=$2
  local captured="$CASE_WORK/openrc-state.$CURRENT_STEP.$APF_TEST_PHASE.json"
  if ! ssh_vm cat /var/lib/alpineform/state.json >"$captured"; then
    fail "$description"
  fi
  assert_local "$description" \
    python3 "$CASE_DIR/assert-dependency.py" state "$captured" "$mode"
  rm -f "$captured"
}

assert_dependency_files() {
  assert_remote "dependency package and managed JSON config are converged" \
    "apk info -e jq && grep -qx jq /etc/apk/world && jq -e '.enabled == true and .revision == 1' /etc/alpineform-dependency.json >/dev/null"
  assert_remote "raw init exposes dependency-checked start, stop, and reload hooks" \
    "test -x /etc/init.d/apf-ci-raw && grep -Fq 'start_pre()' /etc/init.d/apf-ci-raw && grep -Fq 'stop_pre()' /etc/init.d/apf-ci-raw && grep -Fq 'reload()' /etc/init.d/apf-ci-raw && grep -Fq 'jq -e' /etc/init.d/apf-ci-raw"
}

assert_generated_worker_running() {
  assert_remote "generated OpenRC init and conf files are converged" \
    "test -x /etc/init.d/apf-ci-worker && grep -Fq \"command='/bin/sleep'\" /etc/init.d/apf-ci-worker && grep -qx 'APF_CI=enabled' /etc/conf.d/apf-ci-worker"
  assert_remote "generated worker is enabled and running with its managed pidfile" \
    "rc-update show default | grep -Eq '(^|[[:space:]])apf-ci-worker([[:space:]]|$)' && rc-service apf-ci-worker status >/dev/null && test -s /run/apf-ci-worker.pid && kill -0 \$(cat /run/apf-ci-worker.pid)"
}

assert_raw_worker_running() {
  assert_remote "raw worker is enabled and running with its managed pidfile" \
    "rc-update show default | grep -Eq '(^|[[:space:]])apf-ci-raw([[:space:]]|$)' && rc-service apf-ci-raw status >/dev/null && test -s /run/apf-ci-raw.pid && kill -0 \$(cat /run/apf-ci-raw.pid)"
}

assert_dependency_stack_running() {
  assert_dependency_files
  assert_generated_worker_running
  assert_raw_worker_running
}

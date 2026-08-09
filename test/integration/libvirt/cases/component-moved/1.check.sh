source "$CASE_DIR/assertions.sh"

assert_worker_invariants
assert_remote "initial worker configuration and one component-local trigger are recorded" \
  "grep -qx 'revision=one' /etc/alpineform-moved/worker.conf && test \"\$(cat /var/lib/alpineform-moved/reload.count)\" = 1 && grep -Fqx '$OLD_WORKER_ROOT.files.file[\"/etc/alpineform-moved/worker.conf\"]' /var/lib/alpineform-moved/last-trigger"
assert_remote "initial source build v1 is installed" \
  "test \"\$(/usr/local/bin/apf-moved-builder)\" = alpineform-moved-builder-v1"
assert_component_state "initial state contains exactly 18 legacy component resources" \
  "$OLD_WORKER_ROOT" "$OLD_BUILDER_ROOT" no
assert_no_duplicate_physical_ownership
assert_remote "component script has exactly one physical marker" \
  "test \"\$(find /var/lib/alpineform/scripts -type f -name '*.outputs' | wc -l | tr -d ' ')\" = 1"

if [[ "$APF_TEST_PHASE" == applied ]]; then
  capture_move_readonly_snapshot /tmp/apf-component-moved-readonly.before
  apf plan -f "$CASE_DIR/rename-only.apf.hcl" --format json >"$LOG_DIR/rename-only-plan.json"
  assert_exact_move_only_plan "$LOG_DIR/rename-only-plan.json"
  if apf check -f "$CASE_DIR/rename-only.apf.hcl" --format json --color never \
    >"$LOG_DIR/rename-only-check.json" 2>"$LOG_DIR/rename-only-check.log"; then
    fail "rename-only check unexpectedly accepted pending moves"
  fi
  cat "$LOG_DIR/rename-only-check.log"
  assert_exact_move_only_plan "$LOG_DIR/rename-only-check.json"
  assert_local "rename-only check rejected pending component moves specifically" \
    grep -Fq 'remote resources have drift or unapplied changes' "$LOG_DIR/rename-only-check.log"
  capture_move_readonly_snapshot /tmp/apf-component-moved-readonly.after
  assert_remote "rename-only plan and check mutate neither state nor remote resources" \
    "cmp -s /tmp/apf-component-moved-readonly.before /tmp/apf-component-moved-readonly.after"
  assert_component_state "rename-only plan and check leave all 18 legacy state addresses untouched" \
    "$OLD_WORKER_ROOT" "$OLD_BUILDER_ROOT" no
fi

run_remote "record physical identities before the component rename" \
  "stat -c '%d:%i' /usr/local/bin/apf-moved-worker > /var/lib/alpineform-moved/worker.identity && find /var/lib/alpineform/scripts -type f -name '*.outputs' > /var/lib/alpineform-moved/script.marker && sed -n '1p' '/var/lib/alpineform/builds/$APF_MOVED_BUILDER_OWNER.installed' > /var/lib/alpineform-moved/builder.identity-v1"
assert_and_record_worker_runtime

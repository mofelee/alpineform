source "$CASE_DIR/assertions.sh"

python3 "$SCRIPT_DIR/assert-noop-plan.py" "$LOG_DIR/5.pre-apply-plan.json"
assert_worker_invariants
expected_runs=2
if [[ "$APF_TEST_PHASE" == repaired || "$APF_TEST_PHASE" == rebooted ]]; then
  expected_runs=3
fi
assert_remote "removing completed moved blocks stays clean and preserves normal drift repair" \
  "grep -qx 'revision=two' /etc/alpineform-moved/worker.conf && test \"\$(cat /var/lib/alpineform-moved/reload.count)\" = $expected_runs && grep -Fqx '$WORKER_ROOT.files.file[\"/etc/alpineform-moved/worker.conf\"]' /var/lib/alpineform-moved/last-trigger"
assert_remote "removed moved blocks retain the rebuilt source output" \
  "test \"\$(/usr/local/bin/apf-moved-builder)\" = alpineform-moved-builder-v2"
assert_component_state "block removal keeps exactly 18 target resources and physical bindings" \
  "$WORKER_ROOT" "$BUILDER_ROOT" yes
assert_no_duplicate_physical_ownership
assert_and_record_worker_runtime

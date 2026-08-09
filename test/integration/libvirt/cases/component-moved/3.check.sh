source "$CASE_DIR/assertions.sh"

python3 "$SCRIPT_DIR/assert-noop-plan.py" "$LOG_DIR/3.pre-apply-plan.json"
assert_worker_invariants
assert_remote "retaining completed moved blocks does not rerun the worker script" \
  "grep -qx 'revision=two' /etc/alpineform-moved/worker.conf && test \"\$(cat /var/lib/alpineform-moved/reload.count)\" = 2"
assert_remote "retaining completed moved blocks does not rebuild the source component" \
  "test \"\$(/usr/local/bin/apf-moved-builder)\" = alpineform-moved-builder-v1 && test \"\$(sed -n '1p' '/var/lib/alpineform/builds/$APF_MOVED_BUILDER_OWNER.installed')\" = \"\$(cat /var/lib/alpineform-moved/builder.identity-v1)\""
assert_component_state "retained-block state keeps exactly 18 target resources and legacy physical bindings" \
  "$WORKER_ROOT" "$BUILDER_ROOT" yes
assert_no_duplicate_physical_ownership
assert_and_record_worker_runtime

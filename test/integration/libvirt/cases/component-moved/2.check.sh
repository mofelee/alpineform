source "$CASE_DIR/assertions.sh"

assert_exact_rename_plan "$LOG_DIR/2.pre-apply-plan.json"
assert_worker_invariants
assert_remote "rename applied only the real worker configuration update and its trigger" \
  "grep -qx 'revision=two' /etc/alpineform-moved/worker.conf && test \"\$(cat /var/lib/alpineform-moved/reload.count)\" = 2 && grep -Fqx '$WORKER_ROOT.files.file[\"/etc/alpineform-moved/worker.conf\"]' /var/lib/alpineform-moved/last-trigger"
assert_remote "rename preserved the worker artifact and script marker identities" \
  "test \"\$(stat -c '%d:%i' /usr/local/bin/apf-moved-worker)\" = \"\$(cat /var/lib/alpineform-moved/worker.identity)\" && test \"\$(find /var/lib/alpineform/scripts -type f -name '*.outputs')\" = \"\$(cat /var/lib/alpineform-moved/script.marker)\""
assert_remote "rename did not rebuild the source component" \
  "test \"\$(/usr/local/bin/apf-moved-builder)\" = alpineform-moved-builder-v1 && test \"\$(sed -n '1p' '/var/lib/alpineform/builds/$APF_MOVED_BUILDER_OWNER.installed')\" = \"\$(cat /var/lib/alpineform-moved/builder.identity-v1)\""
assert_component_state "renamed state contains exactly 18 target resources and both physical bindings" \
  "$WORKER_ROOT" "$BUILDER_ROOT" yes
assert_no_duplicate_physical_ownership
assert_and_record_worker_runtime

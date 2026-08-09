source "$CASE_DIR/assertions.sh"

assert_exact_source_rebuild_plan "$LOG_DIR/4.pre-apply-plan.json"
assert_worker_invariants
assert_remote "source input v2 rebuilt the installed source component" \
  "test \"\$(/usr/local/bin/apf-moved-builder)\" = alpineform-moved-builder-v2"
assert_remote "source rebuild changed the build identity under the retained legacy owner" \
  "test \"\$(sed -n '1p' '/var/lib/alpineform/builds/$APF_MOVED_BUILDER_OWNER.installed')\" != \"\$(cat /var/lib/alpineform-moved/builder.identity-v1)\" && test ! -e '/var/lib/alpineform/builds/$APF_MOVED_CURRENT_BUILDER_OWNER.installed'"
assert_remote "source rebuild did not rerun the unrelated worker script" \
  "test \"\$(cat /var/lib/alpineform-moved/reload.count)\" = 2"
assert_component_state "rebuilt state keeps exactly 18 target resources and legacy physical bindings" \
  "$WORKER_ROOT" "$BUILDER_ROOT" yes
assert_no_duplicate_physical_ownership
assert_and_record_worker_runtime

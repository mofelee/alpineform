source "$CASE_DIR/assertions.sh"

ensure_component_fixture_server
assert_selected_amd64_sources "$LOG_DIR/1.offline-plan.json" offline
assert_selected_amd64_sources "$LOG_DIR/1.pre-apply-plan.json" online
assert_component_runtime
assert_literal_component_runtime
assert_component_state "configuration 1 persists the exact combined resource identities"
assert_mirror_a_selection
assert_literal_source_requests
assert_remote "protected component fixture server remains available for lifecycle probes" \
  "test -s /var/tmp/apf-component-http/server.pid && kill -0 \"\$(cat /var/tmp/apf-component-http/server.pid)\""

if [[ "$APF_TEST_PHASE" == applied ]]; then
  assert_wrong_checksum_preserves_runtime
elif [[ "$APF_TEST_PHASE" == repaired ]]; then
  assert_four_install_repair_plan "$LOG_DIR/1.install-repair-plan.json"
  assert_combined_component_repair_plan "$LOG_DIR/1.combined-repair-plan.json"
fi

assert_all_protected_surfaces

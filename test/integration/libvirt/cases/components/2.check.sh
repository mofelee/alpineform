source "$CASE_DIR/assertions.sh"

ensure_component_fixture_server
assert_selected_amd64_sources "$LOG_DIR/2.offline-plan.json" offline
assert_selected_amd64_sources "$LOG_DIR/2.pre-apply-plan.json" online
python3 "$SCRIPT_DIR/assert-noop-plan.py" "$LOG_DIR/2.pre-apply-plan.json"
assert_component_runtime
assert_literal_component_runtime
assert_component_state "configuration 2 retains all configuration-1 addresses and cache identities"
assert_literal_source_requests
assert_remote "protected component fixture server remains available for mirror-B repair" \
  "test -s /var/tmp/apf-component-http/server.pid && kill -0 \"\$(cat /var/tmp/apf-component-http/server.pid)\""

case "$APF_TEST_PHASE" in
  applied)
    assert_mirror_b_unused
    APF_COMPONENT_INSTALL_FINGERPRINT="$(component_install_fingerprint)"
    ;;
  repaired|rebooted)
    assert_local "mirror-B cache repair conservatively verifies without physically reinstalling equivalent content" \
      test "$(component_install_fingerprint)" = "$APF_COMPONENT_INSTALL_FINGERPRINT"
    assert_cache_only_repair_plan "$LOG_DIR/2.cache-repair-plan.json"
    assert_mirror_b_cache_repair_requests
    ;;
esac

assert_all_protected_surfaces

source "$CASE_DIR/assertions.sh"

assert_dependency_plan "$LOG_DIR/4.pre-apply-plan.json" recreate
assert_dependency_stack_running
assert_dependency_state present "recreate restores authored dependency metadata"
assert_remote "recreate start hook observed package and config dependencies" \
  "grep -qx dependencies-ready /run/apf-ci-raw.start-pre"
assert_remote "config recreation remains ordering-only and does not trigger reload" \
  "test ! -e /run/apf-ci-raw.reload"

if [[ "$APF_TEST_PHASE" == rebooted ]]; then
  run_remote "record raw service PID before default forget" \
    "cat /run/apf-ci-raw.pid > /run/apf-ci-raw.stage4-reboot.pid"
fi

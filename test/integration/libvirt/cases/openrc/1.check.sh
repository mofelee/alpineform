source "$CASE_DIR/assertions.sh"

assert_dependency_plan "$LOG_DIR/1.pre-apply-plan.json" create
assert_dependency_stack_running
assert_dependency_state present "first apply persists only authored dependency metadata"

case "$APF_TEST_PHASE" in
  applied)
    assert_remote "raw service start hook observed package and config dependencies" \
      "grep -qx dependencies-ready /run/apf-ci-raw.start-pre"
    ;;
  repaired)
    assert_remote "raw service reload observed repaired package and config dependencies" \
      "grep -qx dependencies-ready /run/apf-ci-raw.reload"
    assert_remote "raw reload repaired drift without restarting the service" \
      "test \"\$(cat /run/apf-ci-raw.pid)\" = \"\$(cat /run/apf-ci-raw.before-repair.pid)\""
    ;;
  rebooted)
    assert_remote "raw service reboot start observed retained dependencies" \
      "grep -qx dependencies-ready /run/apf-ci-raw.start-pre"
    run_remote "record raw service PID before the identical configuration" \
      "cat /run/apf-ci-raw.pid > /run/apf-ci-raw.stage1-reboot.pid"
    ;;
esac

source "$CASE_DIR/assertions.sh"

assert_dependency_plan "$LOG_DIR/2.pre-apply-plan.json" noop
assert_local "identical pre-apply plan is entirely no-op" \
  python3 "$SCRIPT_DIR/assert-noop-plan.py" "$LOG_DIR/2.pre-apply-plan.json"
assert_dependency_stack_running
assert_dependency_state present "identical apply retains authored dependency metadata"

case "$APF_TEST_PHASE" in
  applied)
    assert_remote "identical apply did not restart or reload the raw service" \
      "test \"\$(cat /run/apf-ci-raw.pid)\" = \"\$(cat /run/apf-ci-raw.stage1-reboot.pid)\" && test ! -e /run/apf-ci-raw.reload"
    ;;
  rebooted)
    run_remote "prepare the guarded APK deletion probe directory" \
      "mkdir -p /usr/local/sbin && chmod 0755 /usr/local/sbin"
    copy_to_vm "$CASE_DIR/apk-delete-guard.sh" /usr/local/sbin/apk
    run_remote "activate the guarded APK deletion probe" \
      "chmod 0755 /usr/local/sbin/apk"
    assert_remote "guarded APK wrapper precedes the system binary" \
      "test \"\$(command -v apk)\" = /usr/local/sbin/apk && apk info -e jq"
    ;;
esac

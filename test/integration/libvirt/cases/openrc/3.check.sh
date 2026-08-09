source "$CASE_DIR/assertions.sh"

assert_dependency_plan "$LOG_DIR/3.pre-apply-plan.json" cleanup
assert_generated_worker_running
assert_remote "explicit cleanup stops and disables the raw service while retaining its init" \
  "test -x /etc/init.d/apf-ci-raw && ! rc-service apf-ci-raw status >/dev/null 2>&1 && ! rc-update show default | grep -Eq '(^|[[:space:]])apf-ci-raw([[:space:]]|$)'"
assert_remote "explicit cleanup removes the managed config and APK world intent" \
  "test ! -e /etc/alpineform-dependency.json && ! /sbin/apk info -e jq >/dev/null 2>&1 && ! grep -qx jq /etc/apk/world"
assert_dependency_state pruned "explicit cleanup prunes removed resources and dependency references"

if [[ "$APF_TEST_PHASE" == applied ]]; then
  assert_remote "service stop hook ran while package and config dependencies still existed" \
    "grep -qx dependencies-ready /run/apf-ci-raw.stop-pre"
  assert_remote "APK deletion ran only after service and config cleanup" \
    "grep -qx service-before-config-before-package /run/apf-ci-dependency-delete.guard"
  run_remote "remove the guarded APK deletion probe" \
    "rm -f /usr/local/sbin/apk"
  assert_remote "system APK binary is restored after the deletion probe" \
    "test \"\$(command -v apk)\" = /sbin/apk"
fi

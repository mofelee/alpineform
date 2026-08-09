source "$CASE_DIR/assertions.sh"

assert_dependency_plan "$LOG_DIR/5.pre-apply-plan.json" forget
assert_dependency_stack_running
assert_dependency_state empty "default forget leaves no managed resources"

if [[ "$APF_TEST_PHASE" == applied ]]; then
  assert_remote "default forget performs no raw service operation" \
    "test \"\$(cat /run/apf-ci-raw.pid)\" = \"\$(cat /run/apf-ci-raw.stage4-reboot.pid)\" && test ! -e /run/apf-ci-raw.reload"
else
  assert_remote "forgotten raw service starts after reboot using retained dependencies" \
    "grep -qx dependencies-ready /run/apf-ci-raw.start-pre"
fi

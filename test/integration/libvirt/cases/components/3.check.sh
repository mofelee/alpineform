source "$CASE_DIR/assertions.sh"

if [[ "$APF_TEST_PHASE" == applied ]]; then
  ensure_component_fixture_server
fi
assert_exact_teardown_plan "$LOG_DIR/3.pre-apply-plan.json"
assert_teardown_runtime

if [[ "$APF_TEST_PHASE" == applied ]]; then
  capture_server_log
  assert_no_protected_logs
  run_remote "stop and remove the exact protected component fixture server" \
    "pid=\$(cat /var/tmp/apf-component-http/server.pid) && test -n \"\$pid\" && tr '\\000' ' ' <\"/proc/\$pid/cmdline\" | grep -Fq /var/tmp/apf-component-http/fixture-server.py && kill \"\$pid\" && attempt=0 && while kill -0 \"\$pid\" 2>/dev/null && test \"\$attempt\" -lt 20; do attempt=\$((attempt + 1)); sleep 1; done && ! kill -0 \"\$pid\" 2>/dev/null && rm -rf /var/tmp/apf-component-http"
fi

assert_remote "component fixture directory and server process are absent" \
  "test ! -e /var/tmp/apf-component-http && ! ps | grep -q '[f]ixture-server.py'"
assert_all_protected_surfaces

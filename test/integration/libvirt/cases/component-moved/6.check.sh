source "$CASE_DIR/assertions.sh"

assert_exact_teardown_plan "$LOG_DIR/6.pre-apply-plan.json"
assert_remote "managed component artifacts, build outputs, native files, and accounts are destroyed" \
  "test ! -e /usr/local/bin/apf-moved-worker && test ! -e /usr/local/bin/apf-moved-builder && test ! -e /etc/alpineform-moved && ! getent passwd apfmoved >/dev/null && ! getent group apfmoved >/dev/null && test \"\$(stat -c '%U:%G' /var/empty)\" = root:root && permissions=\$(stat -c '%a' /var/empty) && test \"\$((0\$permissions & 022))\" -eq 0 && test -z \"\$(find /var/cache/alpineform/components -type f -print -quit 2>/dev/null)\" && test -z \"\$(find /var/cache/alpineform/builds -type f -print -quit 2>/dev/null)\" && test -z \"\$(find /var/lib/alpineform/builds -type f -print -quit 2>/dev/null)\" && test -z \"\$(find /var/tmp/alpineform/builds -mindepth 1 -print -quit 2>/dev/null)\" && ! apk info | grep -Eq '^\\.alpineform-build-'"

if [[ "$APF_TEST_PHASE" == applied ]]; then
  assert_remote "component teardown leaves empty state without physical bindings" \
    "python3 -c 'import json; state=json.load(open(\"/var/lib/alpineform/state.json\", encoding=\"utf-8\")); assert state.get(\"resources\") == {}; assert state.get(\"component_identities\", {}) == {}'"
  assert_remote "forget-only resources and the preserved account home remain for explicit cleanup" \
    "apk info -e jq && test -e /etc/init.d/apf-moved-worker && test -e /etc/conf.d/apf-moved-worker && test -e /var/lib/alpineform-moved/reload.count && test -d /var/lib/alpineform-moved/home && test \"\$(stat -c '%u:%g' /var/lib/alpineform-moved/home)\" = 2401:2401 && test \"\$(find /var/lib/alpineform/scripts -type f -name '*.outputs' | wc -l | tr -d ' ')\" = 1"
  run_remote "stop the exact forgotten worker process before removing its metadata" \
    "worker_pid=\$(cat /run/apf-moved-worker.pid) && test -n \"\$worker_pid\" && kill -0 \"\$worker_pid\" && rc-service apf-moved-worker stop && attempts=0 && while kill -0 \"\$worker_pid\" 2>/dev/null && [ \"\$attempts\" -lt 10 ]; do sleep 1; attempts=\$((attempts + 1)); done && ! kill -0 \"\$worker_pid\" 2>/dev/null && rc-update del apf-moved-worker default"
fi

run_remote "remove the exact forget-only and fixture residue" \
  "if apk info -e jq >/dev/null 2>&1; then apk --quiet del jq; fi; rm -f /etc/init.d/apf-moved-worker /etc/conf.d/apf-moved-worker /run/apf-moved-worker.pid /tmp/apf-component-moved-readonly.before /tmp/apf-component-moved-readonly.after; rm -rf /tmp/apf-component-moved-http /var/lib/alpineform-moved /var/lib/alpineform /var/cache/alpineform /var/tmp/alpineform"
assert_remote "component-moved case cleanup leaves no owned remote object or state" \
  "test ! -e /usr/local/bin/apf-moved-worker && test ! -e /usr/local/bin/apf-moved-builder && test ! -e /etc/init.d/apf-moved-worker && test ! -e /etc/conf.d/apf-moved-worker && test ! -e /etc/runlevels/default/apf-moved-worker && test ! -L /etc/runlevels/default/apf-moved-worker && test ! -e /run/apf-moved-worker.pid && test ! -e /etc/alpineform-moved && test ! -e /var/lib/alpineform-moved && test ! -e /var/lib/alpineform && test ! -e /var/cache/alpineform && test ! -e /var/tmp/alpineform && test ! -e /tmp/apf-component-moved-http && test ! -e /tmp/apf-component-moved-readonly.before && test ! -e /tmp/apf-component-moved-readonly.after && test \"\$(stat -c '%U:%G' /var/empty)\" = root:root && permissions=\$(stat -c '%a' /var/empty) && test \"\$((0\$permissions & 022))\" -eq 0 && ! apk info -e jq >/dev/null 2>&1 && ! getent passwd apfmoved >/dev/null && ! getent group apfmoved >/dev/null"

assert_remote "selected amd64 tool digest and mode are converged" \
  "test \"\$(sha256sum /usr/local/bin/apf-ci-tool | awk '{print \$1}')\" = '$APF_TOOL_SHA' && test \"\$(stat -c %a /usr/local/bin/apf-ci-tool)\" = 755"
assert_remote "archive tree and manifest are converged" \
  "grep -qx 'AlpineForm archive integration fixture' /opt/apf-ci-bundle/bin/message.txt && grep -qx 'libc=musl' /opt/apf-ci-bundle/share/platform.txt && test ! -e /opt/apf-ci-bundle/unmanaged"
if [[ "$APF_TEST_PHASE" == applied ]]; then
  assert_remote "two first-apply triggers execute one shared script" \
    "test \"\$(wc -l < /var/lib/alpineform/component-ci-runs | tr -d ' ')\" = 1 && test \"\$(wc -l < /var/lib/alpineform/component-ci-triggers | tr -d ' ')\" = 2"
else
  assert_remote "drift repair executes the shared script only once more" \
    "test \"\$(wc -l < /var/lib/alpineform/component-ci-runs | tr -d ' ')\" = 2 && test \"\$(wc -l < /var/lib/alpineform/component-ci-triggers | tr -d ' ')\" = 2"
fi

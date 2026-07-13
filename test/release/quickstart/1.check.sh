assert_remote "promoted quickstart directory and file are converged" \
  "test -d /etc/alpineform-example && test \"\$(stat -c %a /etc/alpineform-example)\" = 755 && grep -qx 'managed-by=alpineform' /etc/alpineform-example/managed.conf && test \"\$(stat -c %a /etc/alpineform-example/managed.conf)\" = 644"
assert_remote "promoted quickstart resources are recorded in state" \
  "grep -Fq '/etc/alpineform-example' /var/lib/alpineform/state.json && grep -Fq '/etc/alpineform-example/managed.conf' /var/lib/alpineform/state.json"

assert_remote "directory metadata is converged" \
  "test -d /etc/alpineform-ci && test \"\$(stat -c %a /etc/alpineform-ci)\" = 750 && test \"\$(stat -c %U:%G /etc/alpineform-ci)\" = root:root"
assert_remote "ordinary file content and metadata are converged" \
  "grep -qx 'enabled=true' /etc/alpineform-ci/app.conf && test \"\$(stat -c %a /etc/alpineform-ci/app.conf)\" = 640"
assert_remote "protected write-only file content and metadata are converged" \
  "grep -qx 'alpineform-ci-secret-sentinel' /etc/alpineform-ci/token && test \"\$(stat -c %a /etc/alpineform-ci/token)\" = 600"
assert_remote "state records a protected resource without the secret" \
  "grep -Fq '\"protected\": true' /var/lib/alpineform/state.json && ! grep -Fq 'alpineform-ci-secret-sentinel' /var/lib/alpineform/state.json"

assert_remote "generated OpenRC init and conf files are converged" \
  "test -x /etc/init.d/apf-ci-worker && grep -Fq \"command='/bin/sleep'\" /etc/init.d/apf-ci-worker && grep -qx 'APF_CI=enabled' /etc/conf.d/apf-ci-worker"
assert_remote "service is enabled in the default runlevel" \
  "rc-update show default | grep -Eq '(^|[[:space:]])apf-ci-worker([[:space:]]|$)'"
assert_remote "service is running with its managed pidfile" \
  "rc-service apf-ci-worker status >/dev/null && test -s /run/apf-ci-worker.pid && kill -0 \$(cat /run/apf-ci-worker.pid)"
assert_remote "raw init service is enabled and running with its managed pidfile" \
  "test -x /etc/init.d/apf-ci-raw && rc-update show default | grep -Eq '(^|[[:space:]])apf-ci-raw([[:space:]]|$)' && rc-service apf-ci-raw status >/dev/null && test -s /run/apf-ci-raw.pid && kill -0 \$(cat /run/apf-ci-raw.pid)"
assert_remote "state records generated and raw init files and services" \
  "grep -Fq '/etc/init.d/apf-ci-worker' /var/lib/alpineform/state.json && grep -Fq '/etc/init.d/apf-ci-raw' /var/lib/alpineform/state.json && grep -Fq 'services.service' /var/lib/alpineform/state.json"

source "$CASE_DIR/assertions.sh"

run_remote "drift package, managed config, raw init, and generated service" \
  "cat /run/apf-ci-raw.pid > /run/apf-ci-raw.before-repair.pid && apk --quiet del jq && printf '%s\n' '{\"enabled\":false,\"revision\":0}' > /etc/alpineform-dependency.json && printf '%s\n' '# drift' >> /etc/init.d/apf-ci-raw && rc-service apf-ci-worker stop && rc-update del apf-ci-worker default"

apf plan -f "$CASE_DIR/1.apf.hcl" --format json >"$LOG_DIR/1.drift-plan.json"
assert_dependency_plan "$LOG_DIR/1.drift-plan.json" repair

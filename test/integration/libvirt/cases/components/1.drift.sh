source "$CASE_DIR/assertions.sh"

run_remote "drift all four protected artifact installs while retaining verified caches" \
  "printf drift > /usr/local/bin/apf-protected-tool && printf 'mode=drift\n' > /etc/alpineform-protected.conf && printf drift > /opt/alpineform-protected/bin/message.txt && printf unmanaged > /opt/alpineform-protected/unmanaged && printf drift > /usr/local/share/ca-certificates/alpineform-protected-root.crt"
apf plan -f "$CASE_DIR/1.apf.hcl" --format json >"$LOG_DIR/1.install-repair-plan.json"
assert_four_install_repair_plan "$LOG_DIR/1.install-repair-plan.json"
assert_no_protected_file "$LOG_DIR/1.install-repair-plan.json" "four-install drift plan"

run_remote "drift the literal binary, archive tree, and both shared-script triggers" \
  "printf drift > /usr/local/bin/apf-ci-tool && printf 'enabled=false\n' > /etc/apf-ci-component.conf && printf unmanaged > /opt/apf-ci-bundle/unmanaged"
apf plan -f "$CASE_DIR/1.apf.hcl" --format json >"$LOG_DIR/1.combined-repair-plan.json"
assert_combined_component_repair_plan "$LOG_DIR/1.combined-repair-plan.json"
assert_no_protected_file "$LOG_DIR/1.combined-repair-plan.json" "combined component drift plan"

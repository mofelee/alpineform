source "$CASE_DIR/assertions.sh"

run_remote "remove all four protected cache artifacts without drifting installs" \
  "rm -f /var/cache/alpineform/components/binary/protected/amd64/artifact /var/cache/alpineform/components/config_file/protected/any/artifact /var/cache/alpineform/components/protected_archive/protected/amd64/artifact /var/cache/alpineform/components/root_ca/protected/any/artifact"
apf plan -f "$CASE_DIR/2.apf.hcl" --format json >"$LOG_DIR/2.cache-repair-plan.json"
assert_cache_only_repair_plan "$LOG_DIR/2.cache-repair-plan.json"
assert_no_protected_file "$LOG_DIR/2.cache-repair-plan.json" "cache-only mirror-B repair plan"
assert_mirror_b_unused

run_remote "seed a path protected from explicit deletion" \
  "printf protected > /tmp/alpineform-protected-delete"
if apf plan -f "$CASE_DIR/protected.apf.hcl" --format json >"$LOG_DIR/prevent-destroy.log" 2>&1; then
  fail "plan accepted a prevent_destroy deletion"
fi
grep -Fq 'prevent_destroy blocks' "$LOG_DIR/prevent-destroy.log"
assert_remote "prevent_destroy leaves the target untouched" \
  "test \"\$(cat /tmp/alpineform-protected-delete)\" = protected"
run_remote "remove unmanaged prevent_destroy fixture" \
  "rm -f /tmp/alpineform-protected-delete"

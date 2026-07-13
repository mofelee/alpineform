assert_remote "state is absent before negative checks" \
  "test ! -e /var/lib/alpineform/state.json"

if apf plan -f "$CASE_DIR/mismatch.apf.hcl" --format json >"$LOG_DIR/platform-mismatch.log" 2>&1; then
  fail "online plan accepted an architecture mismatch"
fi
grep -Eqi 'declares .*detected architecture' "$LOG_DIR/platform-mismatch.log"
assert_remote "platform mismatch did not create state" \
  "test ! -e /var/lib/alpineform/state.json"

run_remote "temporarily present a non-Alpine os-release" \
  "cp /etc/os-release /tmp/alpineform-os-release && printf 'ID=debian\nVERSION_ID=13\n' > /etc/os-release"
if apf plan -f "$CASE_DIR/1.apf.hcl" --format json >"$LOG_DIR/non-alpine.log" 2>&1; then
  run_remote "restore Alpine os-release" "mv /tmp/alpineform-os-release /etc/os-release"
  fail "online plan accepted a non-Alpine target"
fi
run_remote "restore Alpine os-release" "mv /tmp/alpineform-os-release /etc/os-release"
grep -Eqi 'unsupported target|requires Alpine' "$LOG_DIR/non-alpine.log"
assert_remote "non-Alpine rejection did not create state" \
  "test ! -e /var/lib/alpineform/state.json"

lease_expiry=$(($(date +%s) + 60))
run_remote "seed a live competing lease" \
  "mkdir -p /run/lock/alpineform/lock && printf '%s\n' 00000000000000000000000000000000 > /run/lock/alpineform/lock/owner && printf '%s\n' '$lease_expiry' > /run/lock/alpineform/lock/expires_at"
if apf apply -f "$CASE_DIR/1.apf.hcl" --auto-approve --lock-timeout 0 --color never >"$LOG_DIR/lock-contention.log" 2>&1; then
  run_remote "remove competing lease" "rm -rf /run/lock/alpineform/lock"
  fail "concurrent apply unexpectedly acquired a live lease"
fi
grep -Fq 'lock acquisition timed out' "$LOG_DIR/lock-contention.log"
run_remote "remove competing lease" "rm -rf /run/lock/alpineform/lock"
assert_remote "lock contention did not create state or resources" \
  "test ! -e /var/lib/alpineform/state.json && test ! -e /etc/alpineform-ci-facts"

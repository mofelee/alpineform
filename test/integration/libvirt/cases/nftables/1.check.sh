assert_remote "nftables package and explicit world intent are present" \
  "apk info -e nftables && grep -qx nftables /etc/apk/world"
assert_remote "dedicated AlpineForm nftables service is enabled and started" \
  "test -e /etc/runlevels/default/alpineform-nftables && rc-service alpineform-nftables status 2>&1 | grep -q 'status: started'"
assert_remote "version-one table is active, persistent, observed, and armed" \
  "nft --stateless list table inet edge | grep -Fq 'alpineform-v1' && grep -Fq 'alpineform-v1' /etc/nftables.d/alpineform/inet-edge.nft && test -f /var/lib/alpineform/nftables/observed/inet-edge.digest && test -f /var/lib/alpineform/nftables/armed"
assert_remote "external table, service, and stock configuration remain untouched" \
  "nft --stateless list table inet external_guard | grep -Fq 'external-preserved' && rc-service external-nftables status 2>&1 | grep -q 'status: started' && grep -qx external-stock-sentinel /etc/nftables.nft"

if [[ "$APF_TEST_PHASE" == applied ]]; then
  before_active="$(ssh_vm "nft --stateless list table inet edge | sha256sum | cut -d ' ' -f1")"
  before_persistent="$(ssh_vm "sha256sum /etc/nftables.d/alpineform/inet-edge.nft | cut -d ' ' -f1")"
  before_state="$(ssh_vm "sha256sum /var/lib/alpineform/state.json | cut -d ' ' -f1")"

  if apf apply -f "$CASE_DIR/invalid.apf.hcl" --auto-approve --allow-network-disruption --color never >"$LOG_DIR/nftables-invalid.log" 2>&1; then
    fail "nftables accepted an invalid remote candidate"
  fi
  assert_local "invalid candidate reports pre-activation failure" \
    grep -Fq 'rollback status: not required' "$LOG_DIR/nftables-invalid.log"
  assert_remote "invalid candidate changed no active, persistent, external, or state data" \
    "test \"\$(nft --stateless list table inet edge | sha256sum | cut -d ' ' -f1)\" = '$before_active' && test \"\$(sha256sum /etc/nftables.d/alpineform/inet-edge.nft | cut -d ' ' -f1)\" = '$before_persistent' && test \"\$(sha256sum /var/lib/alpineform/state.json | cut -d ' ' -f1)\" = '$before_state' && nft list table inet external_guard >/dev/null"

  if apf apply -f "$CASE_DIR/blocked.apf.hcl" --auto-approve --color never >"$LOG_DIR/nftables-missing-approval.log" 2>&1; then
    fail "auto-approve unexpectedly authorized network disruption"
  fi
  assert_local "auto-approve alone is rejected before mutation" \
    grep -Fq -- '--allow-network-disruption' "$LOG_DIR/nftables-missing-approval.log"
  assert_remote "approval rejection preserved active rules and state" \
    "test \"\$(nft --stateless list table inet edge | sha256sum | cut -d ' ' -f1)\" = '$before_active' && test \"\$(sha256sum /var/lib/alpineform/state.json | cut -d ' ' -f1)\" = '$before_state'"

  log "ACTION: apply SSH-blocking rules and kill the local apf process"
  HOME="$APF_HOME" APF_SSH_CONFIG="$APF_HOME/.ssh/config" \
    "$APF_BIN" apply -f "$CASE_DIR/blocked.apf.hcl" --auto-approve --allow-network-disruption --color never \
    >"$LOG_DIR/nftables-killed-apply.log" 2>&1 &
  apply_pid=$!
  disrupted=false
  deadline=$((SECONDS + 30))
  while (( SECONDS < deadline )); do
    if ! kill -0 "$apply_pid" 2>/dev/null; then break; fi
    if ! ssh_vm true >/dev/null 2>&1; then disrupted=true; break; fi
    sleep 1
  done
  assert_local "blocking candidate disrupted the management path" test "$disrupted" = true
  kill -9 "$apply_pid" 2>/dev/null || true
  wait "$apply_pid" 2>/dev/null || true

  recovered=false
  deadline=$((SECONDS + 45))
  while (( SECONDS < deadline )); do
    if ssh_vm true >/dev/null 2>&1; then recovered=true; break; fi
    sleep 1
  done
  assert_local "detached watchdog restored SSH without apf" test "$recovered" = true
  assert_remote "killed apply rolled back active, persistent, marker, state, and external rules" \
    "test \"\$(nft --stateless list table inet edge | sha256sum | cut -d ' ' -f1)\" = '$before_active' && test \"\$(sha256sum /etc/nftables.d/alpineform/inet-edge.nft | cut -d ' ' -f1)\" = '$before_persistent' && test \"\$(sha256sum /var/lib/alpineform/state.json | cut -d ' ' -f1)\" = '$before_state' && nft list table inet external_guard >/dev/null && test \"\$(sed -n '2p' /var/lib/alpineform/nftables/recovery/inet-edge.status)\" = rollback_confirmed"
fi

if [[ "$APF_TEST_PHASE" == repaired ]]; then
  assert_remote "repair reaped stale completed artifacts" \
    "test ! -d /run/alpineform/nftables || test -z \"\$(find /run/alpineform/nftables -mindepth 1 -maxdepth 1 -print -quit)\""
fi

if [[ "$APF_TEST_PHASE" == rebooted ]]; then
  before_state="$(ssh_vm "sha256sum /var/lib/alpineform/state.json | cut -d ' ' -f1")"
  if apf apply -f "$CASE_DIR/blocked.apf.hcl" --auto-approve --allow-network-disruption --color never >"$LOG_DIR/nftables-rollback-outcome.log" 2>&1; then
    fail "SSH-blocking apply unexpectedly succeeded"
  fi
  assert_local "bounded reconnect reports confirmed rollback" \
    grep -Fq 'rollback status: confirmed' "$LOG_DIR/nftables-rollback-outcome.log"
  assert_remote "confirmed rollback is root-only, clean, and state preserving" \
    "test \"\$(sed -n '2p' /var/lib/alpineform/nftables/recovery/inet-edge.status)\" = rollback_confirmed && test \"\$(stat -c '%U:%G:%a' /var/lib/alpineform/nftables/recovery/inet-edge.status)\" = root:root:600 && test \"\$(sha256sum /var/lib/alpineform/state.json | cut -d ' ' -f1)\" = '$before_state' && { test ! -d /run/alpineform/nftables || test -z \"\$(find /run/alpineform/nftables -mindepth 1 -maxdepth 1 -print -quit)\"; }"
fi

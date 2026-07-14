assert_remote "explicit absence removed only the recorded owned table" \
  "! nft list table inet edge >/dev/null 2>&1 && test ! -e /etc/nftables.d/alpineform/inet-edge.nft && test ! -e /var/lib/alpineform/nftables/observed/inet-edge.digest"
assert_remote "delete retained package, dedicated service, arming, and external ownership" \
  "apk info -e nftables && grep -qx nftables /etc/apk/world && rc-service alpineform-nftables status 2>&1 | grep -q 'status: started' && test -f /var/lib/alpineform/nftables/armed && nft --stateless list table inet external_guard | grep -Fq 'external-preserved' && grep -qx external-stock-sentinel /etc/nftables.nft"
assert_remote "delete left no runtime token artifacts or nftables table state record" \
  "{ test ! -d /run/alpineform/nftables || test -z \"\$(find /run/alpineform/nftables -mindepth 1 -maxdepth 1 -print -quit)\"; } && ! grep -Fq 'nftables.table' /var/lib/alpineform/state.json"

if [[ "$APF_TEST_PHASE" == rebooted ]]; then
  collect_diagnostics
  if grep -R -E 'tcp dport 22 (accept|drop)|(active|persistent|marker|arming)\.snapshot|/run/alpineform/nftables/[0-9a-f]{64}|(^|[^0-9a-f])[0-9a-f]{64}([^0-9a-f]|$)|ssh-(ed25519|rsa|ecdsa) [A-Za-z0-9+/=]{32,}|BEGIN OPENSSH PRIVATE KEY|"schema_version"|alpineform-ci-secret-sentinel' "$ARTIFACT_DIR" >/dev/null 2>&1; then
    fail "nftables diagnostics exposed protected rules, transaction artifacts, key material, state, or secrets"
  fi
  rm -rf "$ARTIFACT_DIR"
  assert_local "nftables plan and collected failure diagnostics are scrubbed" test true
fi

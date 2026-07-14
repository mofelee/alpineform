assert_remote "version-two table is active and persistent" \
  "nft --stateless list table inet edge | grep -Fq 'alpineform-v2' && grep -Fq 'alpineform-v2' /etc/nftables.d/alpineform/inet-edge.nft && test -f /var/lib/alpineform/nftables/observed/inet-edge.digest"
assert_remote "safe update and repair preserve external ownership" \
  "nft --stateless list table inet external_guard | grep -Fq 'external-preserved' && grep -qx external-stock-sentinel /etc/nftables.nft && rc-service external-nftables status 2>&1 | grep -q 'status: started'"
assert_remote "successful update has no runtime token artifacts" \
  "test ! -d /run/alpineform/nftables || test -z \"\$(find /run/alpineform/nftables -mindepth 1 -maxdepth 1 -print -quit)\""

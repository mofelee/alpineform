run_remote "remove the owned active table and corrupt its persistence marker" \
  "nft delete table inet edge; printf 'table inet edge {}\n' > /etc/nftables.d/alpineform/inet-edge.nft; rm -f /var/lib/alpineform/nftables/observed/inet-edge.digest"

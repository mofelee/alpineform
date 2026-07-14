run_remote "install nftables tooling without leaving the desired world intent" \
  "apk add --no-cache nftables >/dev/null && sed -i '/^nftables$/d' /etc/apk/world"

ssh_vm 'cat > /etc/external-guard.nft' <<'EOF'
table inet external_guard {
	chain preserved {
		counter comment "external-preserved"
	}
}
EOF

ssh_vm 'cat > /etc/init.d/external-nftables' <<'EOF'
#!/sbin/openrc-run

description="Load the external nftables table used by AlpineForm integration"

depend() {
	need localmount
	before net
}

start() {
	ebegin "Loading external nftables table"
	if ! nft list table inet external_guard >/dev/null 2>&1; then
		nft -c -f /etc/external-guard.nft && nft -f /etc/external-guard.nft
	fi
	eend $?
}

stop() {
	ebegin "Leaving external nftables table active"
	eend 0
}
EOF

run_remote "start independently owned external nftables service" \
  "chmod 0755 /etc/init.d/external-nftables && rc-update add external-nftables default >/dev/null && rc-service external-nftables start >/dev/null && printf 'external-stock-sentinel\n' > /etc/nftables.nft"

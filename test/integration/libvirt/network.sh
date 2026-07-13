#!/usr/bin/env bash

apf_integration_candidate_subnets() {
  local octet
  if [[ -n "${APF_INTEGRATION_SUBNET_OCTET:-}" ]]; then
    if [[ ! "$APF_INTEGRATION_SUBNET_OCTET" =~ ^[0-9]+$ ]] ||
      (( APF_INTEGRATION_SUBNET_OCTET < 1 || APF_INTEGRATION_SUBNET_OCTET > 254 )); then
      fail "APF_INTEGRATION_SUBNET_OCTET must be between 1 and 254"
    fi
    printf '%s\n' "$APF_INTEGRATION_SUBNET_OCTET"
    return
  fi
  for ((octet = 200; octet <= 254; octet++)); do
    printf '%s\n' "$octet"
  done
  for ((octet = 100; octet <= 199; octet++)); do
    printf '%s\n' "$octet"
  done
}

apf_integration_write_network_xml() {
  local network_xml=$1
  cat >"$network_xml" <<EOF
<network>
  <name>$NETWORK_NAME</name>
  <forward mode='nat'/>
  <bridge name='$BRIDGE_NAME' stp='on' delay='0'/>
  <ip address='192.168.$SUBNET_OCTET.1' netmask='255.255.255.0'>
    <dhcp>
      <range start='192.168.$SUBNET_OCTET.10' end='192.168.$SUBNET_OCTET.250'/>
    </dhcp>
  </ip>
</network>
EOF
}

apf_integration_start_network() {
  local network_xml=$1
  local define_log="$CASE_WORK/network-define.log"
  local start_log="$CASE_WORK/network-start.log"
  local tried=0

  while IFS= read -r SUBNET_OCTET; do
    tried=$((tried + 1))
    BRIDGE_NAME="virbr-apf-$SUBNET_OCTET"
    apf_integration_write_network_xml "$network_xml"
    if ! virsh_system net-define "$network_xml" >"$define_log" 2>&1; then
      cat "$define_log" >&2
      fail "failed to define libvirt network $NETWORK_NAME"
    fi
    NETWORK_DEFINED=1
    if virsh_system net-start "$NETWORK_NAME" >"$start_log" 2>&1; then
      log "network $NETWORK_NAME uses 192.168.$SUBNET_OCTET.0/24"
      return
    fi
    virsh_system net-destroy "$NETWORK_NAME" >/dev/null 2>&1 || true
    virsh_system net-undefine "$NETWORK_NAME" >/dev/null 2>&1 || true
    NETWORK_DEFINED=0
    if [[ -n "${APF_INTEGRATION_SUBNET_OCTET:-}" ]] ||
      ! grep -Eiq 'already in use|overlap|conflict|address.*in use|file exists' "$start_log"; then
      cat "$start_log" >&2
      fail "failed to start libvirt network $NETWORK_NAME"
    fi
  done < <(apf_integration_candidate_subnets)
  fail "no available libvirt subnet after $tried attempts"
}

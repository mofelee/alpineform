#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../../.." && pwd)"
source "$SCRIPT_DIR/alpine-target.sh"

CASE_SOURCE="${1:?usage: run-case.sh CASE_DIR}"
CASE_NAME="$(basename "$CASE_SOURCE")"
APF_BIN="${APF_INTEGRATION_APF_BIN:?missing APF_INTEGRATION_APF_BIN}"
BASE_IMAGE="${APF_INTEGRATION_BASE_IMAGE:?missing APF_INTEGRATION_BASE_IMAGE}"
CASE_WORK="${APF_INTEGRATION_CASE_WORK:?missing APF_INTEGRATION_CASE_WORK}"
ARTIFACT_DIR="${APF_INTEGRATION_CASE_ARTIFACTS:?missing APF_INTEGRATION_CASE_ARTIFACTS}"
CASE_DIR="$CASE_WORK/scenario"
LOG_DIR="$CASE_WORK/logs"
APF_HOME="$CASE_WORK/home"
SSH_KEY="$CASE_WORK/id_ed25519"
VM_DISK="$CASE_WORK/vm.qcow2"
SEED_IMAGE="$CASE_WORK/seed.img"
CONSOLE_LOG="$CASE_WORK/console.log"
DOMAIN_XML="$CASE_WORK/domain.xml"
LIBVIRT_URI="${APF_LIBVIRT_URI:-${VIRSH_DEFAULT_CONNECT_URI:-${LIBVIRT_DEFAULT_URI:-}}}"
REMOTE_HYPERVISOR=""
REMOTE_TMP_DIR=""
REMOTE_POOL="${APF_INTEGRATION_POOL:-vm}"
REMOTE_BASE_IMAGE="${APF_INTEGRATION_REMOTE_BASE_IMAGE:-}"

RUN_SUFFIX="${GITHUB_RUN_ID:-$$}-${GITHUB_RUN_ATTEMPT:-1}-${RANDOM}"
SAFE_CASE_NAME="$(tr -c 'a-zA-Z0-9' '-' <<<"$CASE_NAME" | sed 's/-*$//')"
VM_NAME="dbf-test-alpineform-${SAFE_CASE_NAME}-${RUN_SUFFIX}"
NETWORK_NAME="$VM_NAME-net"
BRIDGE_NAME=""
SUBNET_OCTET=""
printf -v MAC_ADDRESS '52:54:00:%02x:%02x:%02x' \
  "$((RANDOM % 256))" "$((RANDOM % 256))" "$((RANDOM % 256))"

VM_IP=""
VIRT_TYPE="qemu"
VM_DEFINED=0
NETWORK_DEFINED=0
INTERRUPTED=0
ASSERTION_COUNT=0
CURRENT_STEP=""
APF_TEST_PHASE=""

log() {
  printf '[integration:%s] %s\n' "$CASE_NAME" "$*"
}

fail() {
  printf '[integration:%s] ERROR: %s\n' "$CASE_NAME" "$*" >&2
  return 1
}

virsh_system() {
  if [[ -n "$LIBVIRT_URI" ]]; then
    virsh --connect "$LIBVIRT_URI" "$@"
  else
    sudo virsh --connect qemu:///system "$@"
  fi
}

infer_remote_hypervisor() {
  case "$LIBVIRT_URI" in
    qemu+ssh://*|qemu+libssh://*|qemu+libssh2://*)
      local rest="${LIBVIRT_URI#*://}"
      printf '%s\n' "${rest%%/*}"
      ;;
  esac
}

is_remote_libvirt() {
  [[ -n "$REMOTE_HYPERVISOR" ]]
}

remote_exec() {
  if is_remote_libvirt; then
    ssh "$REMOTE_HYPERVISOR" "$@"
  else
    "$@"
  fi
}

remote_write_file() {
  local path=$1
  if is_remote_libvirt; then
    ssh "$REMOTE_HYPERVISOR" "cat > '$path'"
  else
    cat >"$path"
  fi
}

remote_read_file() {
  local path=$1
  if is_remote_libvirt; then
    ssh "$REMOTE_HYPERVISOR" "cat '$path'"
  else
    cat "$path"
  fi
}

pool_path() {
  virsh_system pool-dumpxml "$REMOTE_POOL" | python3 -c '
import sys
import xml.etree.ElementTree as ET
root = ET.fromstring(sys.stdin.read())
target = root.find("target")
path = target.findtext("path") if target is not None else None
if not path:
    raise SystemExit("pool target path not found")
print(path)
'
}

emulator_path() {
  if [[ -n "${APF_INTEGRATION_EMULATOR:-}" ]]; then
    printf '%s\n' "$APF_INTEGRATION_EMULATOR"
  elif remote_exec command -v qemu-system-x86_64 >/dev/null 2>&1; then
    remote_exec command -v qemu-system-x86_64
  else
    printf '/usr/bin/qemu-system-x86_64\n'
  fi
}

prepare_vm_paths() {
  if ! is_remote_libvirt; then
    return
  fi
  local pool_dir remote_sha partial
  pool_dir="$(pool_path)"
  VM_DISK="$pool_dir/$VM_NAME.qcow2"
  SEED_IMAGE="$pool_dir/$VM_NAME-seed.img"
  CONSOLE_LOG="$pool_dir/$VM_NAME-console.log"
  REMOTE_TMP_DIR="$pool_dir/.$VM_NAME"
  [[ -n "$REMOTE_BASE_IMAGE" ]] || REMOTE_BASE_IMAGE="$pool_dir/$APF_INTEGRATION_CLOUD_IMAGE"
  log "remote libvirt: $LIBVIRT_URI, pool $REMOTE_POOL ($pool_dir)"
  remote_exec mkdir -p "$REMOTE_TMP_DIR"
  remote_sha="$(remote_exec sha512sum "$REMOTE_BASE_IMAGE" 2>/dev/null | awk '{print $1}')" || true
  if [[ "$remote_sha" != "$APF_INTEGRATION_CLOUD_IMAGE_SHA512" ]]; then
    log "copying verified Alpine base image to $REMOTE_HYPERVISOR"
    partial="$REMOTE_TMP_DIR/base-image.partial"
    scp -q "$BASE_IMAGE" "$REMOTE_HYPERVISOR:$partial"
    remote_exec chmod 0644 "$partial"
    remote_exec mv -f "$partial" "$REMOTE_BASE_IMAGE"
  fi
}

ssh_vm() {
  ssh -F "$APF_HOME/.ssh/config" -o BatchMode=yes -o ConnectTimeout=5 cihost "$@"
}

copy_to_vm() {
  scp -q -F "$APF_HOME/.ssh/config" "$1" "cihost:$2"
}

apf() {
	HOME="$APF_HOME" APF_SSH_CONFIG="$APF_HOME/.ssh/config" "$APF_BIN" "$@"
}

run_remote() {
  local description=$1 command=$2
  log "ACTION: $description"
  ssh_vm "$command"
}

assert_remote() {
  local description=$1 command=$2
  ASSERTION_COUNT=$((ASSERTION_COUNT + 1))
  log "ASSERT $ASSERTION_COUNT: $description"
  ssh_vm "$command" || fail "$description"
}

assert_local() {
  local description=$1
  shift
  ASSERTION_COUNT=$((ASSERTION_COUNT + 1))
  log "ASSERT $ASSERTION_COUNT: $description"
  "$@" || fail "$description"
}

collect_diagnostics() {
  mkdir -p "$ARTIFACT_DIR"
  virsh_system list --all >"$ARTIFACT_DIR/virsh-list.txt" 2>&1 || true
  virsh_system dumpxml "$VM_NAME" >"$ARTIFACT_DIR/domain.xml" 2>&1 || true
  if [[ -n "$VM_IP" && -f "$APF_HOME/.ssh/config" ]]; then
    ssh_vm 'set +e; cat /etc/os-release; uname -a; rc-status -a; ps; dmesg | tail -200' \
      >"$ARTIFACT_DIR/guest.txt" 2>&1 || true
  fi
  if is_remote_libvirt; then
    remote_read_file "$CONSOLE_LOG" >"$ARTIFACT_DIR/console.log" 2>&1 || true
  else
    sudo sh -c "cat '$CONSOLE_LOG'" >"$ARTIFACT_DIR/console.log" 2>&1 || true
  fi
  cp -a "$LOG_DIR" "$ARTIFACT_DIR/logs" 2>/dev/null || true
  find "$ARTIFACT_DIR" -type f -exec sed -i -E \
    -e 's/(ssh-(ed25519|rsa|ecdsa)[[:space:]]+)[A-Za-z0-9+\/=]+/\1<redacted>/g' \
    -e 's/("key_blob"[[:space:]]*:[[:space:]]*")[^"]+"/\1<redacted>"/g' \
    -e 's/alpineform-ci-secret-sentinel/<redacted-sensitive>/g' \
    {} + 2>/dev/null || true
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if (( status != 0 && VM_DEFINED == 1 && INTERRUPTED == 0 )); then
    log "case failed; collecting redacted diagnostics in $ARTIFACT_DIR"
    collect_diagnostics
  fi
  virsh_system destroy "$VM_NAME" >/dev/null 2>&1 || true
  virsh_system undefine "$VM_NAME" --nvram >/dev/null 2>&1 ||
    virsh_system undefine "$VM_NAME" >/dev/null 2>&1 || true
  virsh_system net-destroy "$NETWORK_NAME" >/dev/null 2>&1 || true
  virsh_system net-undefine "$NETWORK_NAME" >/dev/null 2>&1 || true
  if is_remote_libvirt && [[ -n "$REMOTE_TMP_DIR" ]]; then
    remote_exec rm -rf "$VM_DISK" "$SEED_IMAGE" "$CONSOLE_LOG" "$REMOTE_TMP_DIR" >/dev/null 2>&1 || true
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'INTERRUPTED=1; exit 130' INT TERM

wait_for_vm_ip() {
  local deadline=$((SECONDS + 240))
  while (( SECONDS < deadline )); do
    VM_IP="$(virsh_system domifaddr "$VM_NAME" --source lease 2>/dev/null |
      awk '$3 == "ipv4" { split($4, value, "/"); print value[1]; exit }')" || VM_IP=""
    [[ -z "$VM_IP" ]] || return 0
    sleep 2
  done
  return 1
}

wait_for_ssh() {
  local deadline=$((SECONDS + 300))
  while (( SECONDS < deadline )); do
    if ssh_vm true >/dev/null 2>&1; then
      return 0
    fi
    sleep 3
  done
  return 1
}

wait_for_cloud_init() {
  local deadline=$((SECONDS + 180))
  while (( SECONDS < deadline )); do
    if ssh_vm 'test -e /run/alpineform-cloud-init-ready' >/dev/null 2>&1; then
      return 0
    fi
    sleep 3
  done
  return 1
}

reboot_and_wait() {
  local previous deadline current
  previous="$(ssh_vm 'cat /proc/sys/kernel/random/boot_id')"
  virsh_system reboot "$VM_NAME" >/dev/null
  deadline=$((SECONDS + 240))
  while (( SECONDS < deadline )); do
    current="$(ssh_vm 'cat /proc/sys/kernel/random/boot_id' 2>/dev/null || true)"
    if [[ -n "$current" && "$current" != "$previous" ]]; then
      return
    fi
    sleep 3
  done
  return 1
}

run_hook() {
  local hook=$1
  log "running $(basename "$hook") for phase $APF_TEST_PHASE"
  source "$hook"
}

source "$SCRIPT_DIR/network.sh"

mkdir -p "$CASE_WORK" "$CASE_DIR" "$LOG_DIR" "$APF_HOME/.ssh"
cp -a "$CASE_SOURCE/." "$CASE_DIR/"
chmod 0700 "$APF_HOME" "$APF_HOME/.ssh"
REMOTE_HYPERVISOR="${APF_INTEGRATION_HYPERVISOR:-$(infer_remote_hypervisor)}"
prepare_vm_paths
if [[ "${APF_INTEGRATION_DISABLE_KVM:-0}" != 1 ]] && remote_exec test -r /dev/kvm && remote_exec test -w /dev/kvm; then
  VIRT_TYPE="kvm"
fi

ssh-keygen -q -t ed25519 -N '' -f "$SSH_KEY"
cp "$SSH_KEY" "$CASE_DIR/id_ed25519"
chmod 0600 "$CASE_DIR/id_ed25519"
PUBLIC_KEY="$(cat "$SSH_KEY.pub")"

USER_DATA="$CASE_WORK/user-data"
META_DATA="$CASE_WORK/meta-data"
NETWORK_CONFIG="$CASE_WORK/network-config"
if is_remote_libvirt; then
  USER_DATA="$REMOTE_TMP_DIR/user-data"
  META_DATA="$REMOTE_TMP_DIR/meta-data"
  NETWORK_CONFIG="$REMOTE_TMP_DIR/network-config"
fi
remote_write_file "$USER_DATA" <<EOF
#cloud-config
disable_root: false
ssh_pwauth: false
users:
  - default
  - name: root
    lock_passwd: false
    ssh_authorized_keys:
      - $PUBLIC_KEY
runcmd:
  - [sh, -c, "mkdir -p /root/.ssh; chmod 0700 /root/.ssh; printf '%s\\n' '$PUBLIC_KEY' > /root/.ssh/authorized_keys; chmod 0600 /root/.ssh/authorized_keys; passwd -d root; touch /run/alpineform-cloud-init-ready"]
EOF
remote_write_file "$META_DATA" <<EOF
instance-id: $VM_NAME
local-hostname: alpineform-ci
EOF
remote_write_file "$NETWORK_CONFIG" <<EOF
version: 2
ethernets:
  primary:
    match:
      macaddress: "$MAC_ADDRESS"
    dhcp4: true
EOF

remote_exec cloud-localds --network-config="$NETWORK_CONFIG" "$SEED_IMAGE" "$USER_DATA" "$META_DATA"
remote_exec qemu-img create -q -f qcow2 -F qcow2 -b "${REMOTE_BASE_IMAGE:-$BASE_IMAGE}" "$VM_DISK" 4G
if is_remote_libvirt; then
  remote_exec chmod 0644 "$SEED_IMAGE"
  remote_exec chmod 0666 "$VM_DISK"
else
  chmod 0755 "$CASE_WORK"
  chmod 0644 "$SEED_IMAGE"
  chmod 0666 "$VM_DISK"
  touch "$CONSOLE_LOG"
  chmod 0666 "$CONSOLE_LOG"
fi

CPU_XML=""
[[ "$VIRT_TYPE" != kvm ]] || CPU_XML="<cpu mode='host-passthrough' check='none'/>"
EMULATOR_PATH="$(emulator_path)"
cat >"$DOMAIN_XML" <<EOF
<domain type='$VIRT_TYPE'>
  <name>$VM_NAME</name>
  <memory unit='MiB'>1024</memory>
  <vcpu>2</vcpu>
  <os firmware='efi'>
    <type arch='x86_64' machine='q35'>hvm</type>
    <firmware>
      <feature enabled='no' name='enrolled-keys'/>
      <feature enabled='no' name='secure-boot'/>
    </firmware>
    <boot dev='hd'/>
  </os>
  <features><acpi/><apic/></features>
  $CPU_XML
  <clock offset='utc'/>
  <on_poweroff>destroy</on_poweroff>
  <on_reboot>restart</on_reboot>
  <on_crash>destroy</on_crash>
  <devices>
    <emulator>$EMULATOR_PATH</emulator>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2' discard='unmap'/>
      <source file='$VM_DISK'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    <disk type='file' device='disk'>
      <driver name='qemu' type='raw'/>
      <source file='$SEED_IMAGE'/>
      <target dev='vdb' bus='virtio'/>
      <readonly/>
    </disk>
    <interface type='network'>
      <mac address='$MAC_ADDRESS'/>
      <source network='$NETWORK_NAME'/>
      <model type='virtio'/>
    </interface>
    <serial type='pty'><log file='$CONSOLE_LOG' append='off'/><target port='0'/></serial>
    <console type='pty'><target type='serial' port='0'/></console>
  </devices>
</domain>
EOF

log "starting fresh Alpine VM using $VIRT_TYPE"
apf_integration_start_network "$CASE_WORK/network.xml"
virsh_system define "$DOMAIN_XML" >/dev/null
VM_DEFINED=1
virsh_system start "$VM_NAME" >/dev/null
wait_for_vm_ip
log "VM acquired address $VM_IP"

cat >"$APF_HOME/.ssh/config" <<EOF
Host *
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null

Host cihost
  HostName $VM_IP
  User root
  IdentityFile $SSH_KEY
$(if is_remote_libvirt; then printf '  ProxyCommand ssh -W %%h:%%p %s\n' "$REMOTE_HYPERVISOR"; fi)
EOF
chmod 0600 "$APF_HOME/.ssh/config"
wait_for_ssh
wait_for_cloud_init
ssh_vm ". /etc/os-release; test \"\$ID\" = alpine; test \"\$VERSION_ID\" = '$APF_INTEGRATION_ALPINE_VERSION'; test \"\$(apk --print-arch)\" = '$APF_INTEGRATION_ALPINE_ARCHITECTURE'; test \"\$(uname -m)\" = '$APF_INTEGRATION_ALPINE_ARCHITECTURE'"

APF_CONFIG_HOST="$VM_IP"
[[ -z "$REMOTE_HYPERVISOR" ]] || APF_CONFIG_HOST=cihost
while IFS= read -r config; do
  sed -i \
    -e "s/__APF_VM_HOST__/$APF_CONFIG_HOST/g" \
    -e "s|__APF_AUTHORIZED_KEY__|$PUBLIC_KEY|g" \
    "$config"
done < <(find "$CASE_DIR" -type f -name '*.apf.hcl')

if [[ -f "$CASE_DIR/prepare.sh" ]]; then
  APF_TEST_PHASE=prepare
  run_hook "$CASE_DIR/prepare.sh"
fi
if [[ -f "$CASE_DIR/negative.sh" ]]; then
  APF_TEST_PHASE=negative
  run_hook "$CASE_DIR/negative.sh"
fi

declare -a CONFIGS=()
next_step=1
while [[ -f "$CASE_DIR/$next_step.apf.hcl" ]]; do
  CONFIGS+=("$CASE_DIR/$next_step.apf.hcl")
  next_step=$((next_step + 1))
done
config_count="$(find "$CASE_DIR" -maxdepth 1 -type f -name '[0-9]*.apf.hcl' | wc -l | tr -d '[:space:]')"
(( config_count == ${#CONFIGS[@]} && config_count > 0 )) || fail "numbered configs must start at 1 and be contiguous"

for config in "${CONFIGS[@]}"; do
  filename="$(basename "$config")"
  CURRENT_STEP="${filename%%.apf.hcl}"
  check_hook="$CASE_DIR/$CURRENT_STEP.check.sh"
  drift_hook="$CASE_DIR/$CURRENT_STEP.drift.sh"
  [[ -f "$check_hook" ]] || fail "missing post-apply checks for step $CURRENT_STEP"

  log "step $CURRENT_STEP: validate and offline plan"
  apf validate -f "$config" | tee "$LOG_DIR/$CURRENT_STEP.validate.log"
  apf plan --offline -f "$config" --format json | tee "$LOG_DIR/$CURRENT_STEP.offline-plan.json"
  log "step $CURRENT_STEP: online plan and apply"
  apf plan -f "$config" --format json | tee "$LOG_DIR/$CURRENT_STEP.pre-apply-plan.json"
  apf apply -f "$config" --auto-approve --color never | tee "$LOG_DIR/$CURRENT_STEP.apply.log"
  apf plan -f "$config" --format json | tee "$LOG_DIR/$CURRENT_STEP.noop-plan.json"
  python3 "$SCRIPT_DIR/assert-noop-plan.py" "$LOG_DIR/$CURRENT_STEP.noop-plan.json"
  apf check -f "$config" --color never | tee "$LOG_DIR/$CURRENT_STEP.check.log"
  assert_remote "runtime lock is released after apply" 'test ! -e /run/lock/alpineform/lock'
  APF_TEST_PHASE=applied
  run_hook "$check_hook"

  if [[ -f "$drift_hook" ]]; then
    APF_TEST_PHASE=drift
    run_hook "$drift_hook"
    if apf check -f "$config" >"$LOG_DIR/$CURRENT_STEP.drift-check.log" 2>&1; then
      fail "check unexpectedly accepted drift for step $CURRENT_STEP"
    fi
    cat "$LOG_DIR/$CURRENT_STEP.drift-check.log"
    apf apply -f "$config" --auto-approve --color never | tee "$LOG_DIR/$CURRENT_STEP.repair.log"
    apf plan -f "$config" --format json | tee "$LOG_DIR/$CURRENT_STEP.repair-noop-plan.json"
    python3 "$SCRIPT_DIR/assert-noop-plan.py" "$LOG_DIR/$CURRENT_STEP.repair-noop-plan.json"
    apf check -f "$config" --color never | tee "$LOG_DIR/$CURRENT_STEP.repair-check.log"
    APF_TEST_PHASE=repaired
    run_hook "$check_hook"
  fi

  log "step $CURRENT_STEP: reboot and verify persistence"
  reboot_and_wait
  apf check -f "$config" --color never | tee "$LOG_DIR/$CURRENT_STEP.reboot-check.log"
  APF_TEST_PHASE=rebooted
  run_hook "$check_hook"
done

if grep -R -F 'alpineform-ci-secret-sentinel' "$LOG_DIR" >/dev/null 2>&1; then
  fail "sensitive sentinel leaked into integration logs"
fi
(( ASSERTION_COUNT > 0 )) || fail "case must run at least one explicit assertion"
log "case passed with $ASSERTION_COUNT explicit assertions"

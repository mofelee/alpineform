package provider

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

const (
	nftablesPersistenceDirectory = "/etc/nftables.d/alpineform"
	nftablesOpenRCName           = "alpineform-nftables"
	nftablesOpenRCInitPath       = "/etc/init.d/alpineform-nftables"
	nftablesRuntimeDirectory     = "/run/alpineform/nftables"
	nftablesObservedDirectory    = "/var/lib/alpineform/nftables/observed"
	nftablesRecoveryDirectory    = "/var/lib/alpineform/nftables/recovery"
)

var providerNftablesIdentityPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)
var providerNftablesTokenPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

const (
	nftablesConfirmationAttemptTimeout = 2 * time.Second
	nftablesRecoveryGrace              = 10 * time.Second
	nftablesInitialRetryBackoff        = 250 * time.Millisecond
	nftablesMaximumRetryBackoff        = 2 * time.Second
)

type nftablesTransactionRuntime struct {
	NewToken       func() (string, error)
	Now            func() time.Time
	Wait           func(context.Context, time.Duration) error
	AttemptTimeout time.Duration
	RecoveryGrace  time.Duration
}

func (runtime nftablesTransactionRuntime) normalized() nftablesTransactionRuntime {
	if runtime.Now == nil {
		runtime.Now = time.Now
	}
	if runtime.Wait == nil {
		runtime.Wait = func(ctx context.Context, duration time.Duration) error {
			timer := time.NewTimer(duration)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	if runtime.AttemptTimeout <= 0 {
		runtime.AttemptTimeout = nftablesConfirmationAttemptTimeout
	}
	if runtime.RecoveryGrace <= 0 {
		runtime.RecoveryGrace = nftablesRecoveryGrace
	}
	return runtime
}

const nftablesPersistenceInspectScript = `set -eu
directory=$1
path=$2
if [ -L "$directory" ]; then
  echo directory_symlink
  exit 0
fi
if [ -e "$directory" ] && [ ! -d "$directory" ]; then
  echo directory_other
  exit 0
fi
if [ ! -e "$path" ]; then
  echo missing
  exit 0
fi
if [ -L "$path" ]; then
  echo symlink
  exit 0
fi
if [ ! -f "$path" ]; then
  echo other
  exit 0
fi
echo file
stat -c '%U' "$path"
stat -c '%G' "$path"
stat -c '%a' "$path"
stat -c '%s' "$path"
sha256sum "$path" | awk '{print $1}'
`

const nftablesActiveInspectScript = `set -eu
family=$1
name=$2
marker=$3
active_state=missing
active_sha=-
if nft list table "$family" "$name" >/dev/null 2>&1; then
  active_state=present
  active_sha=$(nft --stateless list table "$family" "$name" | sha256sum | awk '{print $1}')
fi
marker_state=missing
marker_fingerprint=-
marker_active_sha=-
marker_ensure=-
if [ -L "$marker" ]; then
  marker_state=symlink
elif [ -e "$marker" ] && [ ! -f "$marker" ]; then
  marker_state=other
elif [ -f "$marker" ]; then
  marker_state=file
  marker_fingerprint=$(sed -n '1p' "$marker")
  marker_active_sha=$(sed -n '2p' "$marker")
  marker_ensure=$(sed -n '3p' "$marker")
fi
echo "$active_state"
echo "$active_sha"
echo "$marker_state"
echo "$marker_fingerprint"
echo "$marker_active_sha"
echo "$marker_ensure"
`

const nftablesTransactionPrepareScript = `set -eu
token=$1
family=$2
name=$3
persistence=$4
marker=$5
fingerprint=$6
ensure=$7
timeout=$8
runtime_root=/run/alpineform/nftables
runtime_parent=/run/alpineform
observed_root=/var/lib/alpineform/nftables/observed
recovery_root=/var/lib/alpineform/nftables/recovery
observed_parent=/var/lib/alpineform/nftables
state_parent=/var/lib/alpineform
persistence_base=/etc/nftables.d
persistence_directory=/etc/nftables.d/alpineform
arming=/var/lib/alpineform/nftables/armed
recovery=$recovery_root/$family-$name.status
transaction=$runtime_root/$token
candidate=$transaction/candidate.nft
activation=$transaction/activation.nft
active_snapshot=$transaction/active.snapshot.nft
persistent_snapshot=$transaction/persistent.snapshot
marker_snapshot=$transaction/marker.snapshot
arming_snapshot=$transaction/arming.snapshot
watchdog=$transaction/watchdog.sh
active_before=missing
persistent_before=missing
marker_before=missing
arming_before=missing
watchdog_started=false
prepare_complete=false
umask 077

safe_directory() {
  path=$1
  if [ -L "$path" ] || { [ -e "$path" ] && [ ! -d "$path" ]; }; then
    return 1
  fi
  return 0
}

build_activation() {
  : >"$activation"
  if nft list table "$family" "$name" >/dev/null 2>&1; then
    printf 'delete table %s %s\n' "$family" "$name" >>"$activation"
  fi
  if [ "$ensure" = present ]; then
    cat "$candidate" >>"$activation"
  fi
}

finish_prepare() {
  status=$?
  trap - EXIT HUP INT TERM
  if [ "$prepare_complete" = true ]; then
    exit "$status"
  fi
  if [ "$watchdog_started" = true ]; then
    : >"$transaction/abort" 2>/dev/null || true
  else
    rm -rf "$transaction"
    if [ -n "${token_digest:-}" ] && [ -n "${recovery:-}" ] && [ ! -L "$recovery" ]; then
      printf '%s\n%s\n' "$token_digest" activation_failed >"$recovery" 2>/dev/null || true
      chown 0:0 "$recovery" 2>/dev/null || true
      chmod 0600 "$recovery" 2>/dev/null || true
    fi
  fi
  exit "$status"
}
trap finish_prepare EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

safe_directory "$runtime_parent"
mkdir -p "$runtime_parent"
safe_directory "$runtime_root"
mkdir -p "$runtime_root"
chown 0:0 "$runtime_root"
chmod 0700 "$runtime_root"
for existing in "$runtime_root"/*; do
  if [ ! -e "$existing" ] && [ ! -L "$existing" ]; then
    continue
  fi
  existing_name=${existing##*/}
  if [ ${#existing_name} -ne 64 ]; then
    echo 'refusing unsafe nftables runtime artifact' >&2
    exit 1
  fi
  case "$existing_name" in
    *[!0-9a-f]*) echo 'refusing unsafe nftables runtime artifact' >&2; exit 1 ;;
  esac
  if [ -L "$existing" ] || [ ! -d "$existing" ]; then
    echo 'refusing unsafe nftables runtime artifact' >&2
    exit 1
  fi
  existing_status=$(sed -n '1p' "$existing/status" 2>/dev/null || true)
  case "$existing_status" in
    confirmed|rollback_complete) rm -rf "$existing" ;;
    rollback_failed)
      echo 'a recoverable nftables rollback failure blocks new activation' >&2
      exit 1
      ;;
    *)
      echo 'an active or recoverable nftables transaction blocks new activation' >&2
      exit 1
      ;;
  esac
done
if [ -e "$transaction" ] || [ -L "$transaction" ]; then
  echo 'refusing reused nftables transaction token' >&2
  exit 1
fi
mkdir "$transaction"
chmod 0700 "$transaction"

if [ "$ensure" = present ]; then
  cat >"$candidate"
  chmod 0600 "$candidate"
else
  : >"$candidate"
  chmod 0600 "$candidate"
fi

build_activation
nft -c -f "$activation"

if nft --stateless list table "$family" "$name" >"$active_snapshot" 2>/dev/null; then
  active_before=present
  chmod 0600 "$active_snapshot"
else
  : >"$active_snapshot"
  chmod 0600 "$active_snapshot"
fi

safe_directory "$persistence_base"
safe_directory "$persistence_directory"
mkdir -p "$persistence_base" "$persistence_directory"
chown 0:0 "$persistence_directory"
chmod 0700 "$persistence_directory"
if [ -L "$persistence" ] || { [ -e "$persistence" ] && [ ! -f "$persistence" ]; }; then
  echo 'refusing unsafe nftables persistence snapshot target' >&2
  exit 1
fi
if [ -f "$persistence" ]; then
  cp "$persistence" "$persistent_snapshot"
  chmod 0600 "$persistent_snapshot"
  persistent_before=present
else
  : >"$persistent_snapshot"
  chmod 0600 "$persistent_snapshot"
fi

safe_directory "$state_parent"
mkdir -p "$state_parent"
safe_directory "$observed_parent"
mkdir -p "$observed_parent"
safe_directory "$observed_root"
mkdir -p "$observed_root"
chown 0:0 "$observed_root"
chmod 0700 "$observed_root"
safe_directory "$recovery_root"
mkdir -p "$recovery_root"
chown 0:0 "$recovery_root"
chmod 0700 "$recovery_root"
if [ -L "$marker" ] || { [ -e "$marker" ] && [ ! -f "$marker" ]; }; then
  echo 'refusing unsafe nftables observed marker target' >&2
  exit 1
fi
if [ -f "$marker" ]; then
  cp "$marker" "$marker_snapshot"
  chmod 0600 "$marker_snapshot"
  marker_before=present
else
  : >"$marker_snapshot"
  chmod 0600 "$marker_snapshot"
fi

if [ -L "$arming" ] || { [ -e "$arming" ] && [ ! -f "$arming" ]; }; then
  echo 'refusing unsafe nftables arming marker target' >&2
  exit 1
fi
if [ -f "$arming" ]; then
  cp "$arming" "$arming_snapshot"
  chmod 0600 "$arming_snapshot"
  arming_before=present
else
  : >"$arming_snapshot"
  chmod 0600 "$arming_snapshot"
fi

if [ "$active_before" = present ]; then
  restore_check=$transaction/restore.check.nft
  printf 'delete table %s %s\n' "$family" "$name" >"$restore_check"
  cat "$active_snapshot" >>"$restore_check"
  chmod 0600 "$restore_check"
  nft -c -f "$restore_check"
fi

build_activation
nft -c -f "$activation"

printf '%s\n%s\n%s\n' "$family" "$name" "$ensure" >"$transaction/identity"
printf '%s\n%s\n%s\n%s\n' "$active_before" "$persistent_before" "$marker_before" "$arming_before" >"$transaction/snapshot.state"
printf '%s\n' "$timeout" >"$transaction/timeout"
printf '%s\n' "$fingerprint" >"$transaction/fingerprint"
printf '%s\n' preparing >"$transaction/status"
chmod 0600 "$transaction/identity" "$transaction/snapshot.state" "$transaction/timeout" "$transaction/fingerprint" "$transaction/status"
if [ -L "$recovery" ] || { [ -e "$recovery" ] && [ ! -f "$recovery" ]; }; then
  echo 'refusing unsafe nftables recovery status target' >&2
  exit 1
fi
token_digest=$(printf '%s' "$token" | sha256sum | awk '{print $1}')
recovery_tmp=$(mktemp "$recovery_root/.alpineform-nftables-recovery.XXXXXX")
printf '%s\n%s\n' "$token_digest" pending >"$recovery_tmp"
chown 0:0 "$recovery_tmp"
chmod 0600 "$recovery_tmp"
mv -f "$recovery_tmp" "$recovery"

cat >"$watchdog" <<'ALPINEFORM_NFTABLES_WATCHDOG'
#!/bin/sh
set -u
runtime_root=/run/alpineform/nftables
transaction=$(CDPATH= cd "${0%/*}" 2>/dev/null && pwd -P) || exit 70
token=${transaction##*/}
status=$transaction/status
action_lock=$transaction/action.lock
recovery_root=/var/lib/alpineform/nftables/recovery
family=
name=

write_status() {
  printf '%s\n' "$1" >"$status" 2>/dev/null || true
  chmod 0600 "$status" 2>/dev/null || true
}

fail_rollback() {
  write_status rollback_failed
  write_recovery rollback_failed || true
  exit 70
}

write_recovery() {
  outcome=$1
  [ -n "$family" ] && [ -n "$name" ] || return 1
  if [ -L "$recovery_root" ] || { [ -e "$recovery_root" ] && [ ! -d "$recovery_root" ]; }; then
    return 1
  fi
  mkdir -p "$recovery_root" || return 1
  chown 0:0 "$recovery_root" || return 1
  chmod 0700 "$recovery_root" || return 1
  recovery=$recovery_root/$family-$name.status
  if [ -L "$recovery" ] || { [ -e "$recovery" ] && [ ! -f "$recovery" ]; }; then
    return 1
  fi
  token_digest=$(printf '%s' "$token" | sha256sum | awk '{print $1}') || return 1
  tmp=$(mktemp "$recovery_root/.alpineform-nftables-recovery.XXXXXX") || return 1
  printf '%s\n%s\n' "$token_digest" "$outcome" >"$tmp" || return 1
  chown 0:0 "$tmp" && chmod 0600 "$tmp" && mv -f "$tmp" "$recovery"
}

safe_token_path() {
  [ "${transaction%/*}" = "$runtime_root" ] || return 1
  [ ${#token} -eq 64 ] || return 1
  case "$token" in *[!0-9a-f]*) return 1 ;; esac
  [ ! -L "$transaction" ] && [ -d "$transaction" ]
}

regular_file() {
  [ ! -L "$1" ] && [ -f "$1" ]
}

atomic_restore() {
  snapshot=$1
  target=$2
  directory=${target%/*}
  regular_file "$snapshot" || return 1
  if [ -L "$target" ] || { [ -e "$target" ] && [ ! -f "$target" ]; }; then
    return 1
  fi
  tmp=$(mktemp "$directory/.alpineform-nftables-restore.XXXXXX") || return 1
  cp "$snapshot" "$tmp" && chown 0:0 "$tmp" && chmod 0600 "$tmp" && mv -f "$tmp" "$target"
}

cleanup_confirmed() {
  write_status confirmed
  cd "$runtime_root" || exit 70
  rm -rf "$transaction"
  exit 0
}

acquire_rollback() {
  if mkdir "$action_lock" 2>/dev/null; then
    return 0
  fi
  attempts=0
  while [ "$attempts" -lt 5 ]; do
    [ -f "$transaction/confirmed" ] && cleanup_confirmed
    if [ ! -d "$action_lock" ]; then
      mkdir "$action_lock" 2>/dev/null && return 0
    fi
    sleep 1
    attempts=$((attempts + 1))
  done
  pid=$(sed -n '1p' "$action_lock/pid" 2>/dev/null || true)
  start=$(sed -n '1p' "$action_lock/start" 2>/dev/null || true)
  case "$pid" in ''|*[!0-9]*) pid= ;; esac
  case "$start" in ''|*[!0-9]*) start= ;; esac
  if [ -n "$pid" ] && [ -n "$start" ] && [ -r "/proc/$pid/stat" ]; then
    current_start=$(awk '{print $22}' "/proc/$pid/stat" 2>/dev/null || true)
    if [ "$current_start" = "$start" ]; then
      kill "$pid" 2>/dev/null || true
      attempts=0
      while [ "$attempts" -lt 5 ] && kill -0 "$pid" 2>/dev/null; do
        sleep 1
        attempts=$((attempts + 1))
      done
      kill -9 "$pid" 2>/dev/null || true
    fi
  fi
  rm -rf "$action_lock"
  mkdir "$action_lock" 2>/dev/null
}

safe_token_path || exit 70
regular_file "$transaction/identity" || fail_rollback
regular_file "$transaction/snapshot.state" || fail_rollback
regular_file "$transaction/timeout" || fail_rollback
family=$(sed -n '1p' "$transaction/identity")
name=$(sed -n '2p' "$transaction/identity")
active_before=$(sed -n '1p' "$transaction/snapshot.state")
persistent_before=$(sed -n '2p' "$transaction/snapshot.state")
marker_before=$(sed -n '3p' "$transaction/snapshot.state")
arming_before=$(sed -n '4p' "$transaction/snapshot.state")
timeout=$(sed -n '1p' "$transaction/timeout")
case "$family" in arp|bridge|inet|ip|ip6|netdev) ;; *) fail_rollback ;; esac
case "$name" in ''|*[!A-Za-z0-9_-]*|[!A-Za-z_]*) fail_rollback ;; esac
[ ${#name} -le 64 ] || fail_rollback
case "$active_before:$persistent_before:$marker_before:$arming_before" in
  present:present:present:present|present:present:present:missing|present:present:missing:present|present:present:missing:missing|present:missing:present:present|present:missing:present:missing|present:missing:missing:present|present:missing:missing:missing|missing:present:present:present|missing:present:present:missing|missing:present:missing:present|missing:present:missing:missing|missing:missing:present:present|missing:missing:present:missing|missing:missing:missing:present|missing:missing:missing:missing) ;;
  *) fail_rollback ;;
esac
case "$timeout" in ''|*[!0-9]*) fail_rollback ;; esac
[ "$timeout" -ge 10 ] && [ "$timeout" -le 300 ] || fail_rollback

printf '%s\n' "$$" >"$transaction/watchdog.pid" || fail_rollback
awk '{print $22}' "/proc/$$/stat" >"$transaction/watchdog.start" || fail_rollback
: >"$transaction/watchdog.ready" || fail_rollback
chmod 0600 "$transaction/watchdog.pid" "$transaction/watchdog.start" "$transaction/watchdog.ready" || fail_rollback

interrupted=false
trap 'interrupted=true' HUP INT TERM
remaining=$timeout
while [ "$remaining" -gt 0 ]; do
  [ -f "$transaction/confirmed" ] && cleanup_confirmed
  if [ -f "$transaction/abort" ] || [ "$interrupted" = true ]; then
    break
  fi
  sleep 1 || true
  remaining=$((remaining - 1))
done
[ -f "$transaction/confirmed" ] && cleanup_confirmed
acquire_rollback || fail_rollback
[ -f "$transaction/confirmed" ] && cleanup_confirmed
write_status rollback_pending
write_recovery rollback_pending || fail_rollback

persistence=/etc/nftables.d/alpineform/$family-$name.nft
marker=/var/lib/alpineform/nftables/observed/$family-$name.digest
arming=/var/lib/alpineform/nftables/armed
rollback=$transaction/rollback.nft
: >"$rollback" || fail_rollback
if nft list table "$family" "$name" >/dev/null 2>&1; then
  printf 'delete table %s %s\n' "$family" "$name" >>"$rollback" || fail_rollback
fi
if [ "$active_before" = present ]; then
  regular_file "$transaction/active.snapshot.nft" || fail_rollback
  cat "$transaction/active.snapshot.nft" >>"$rollback" || fail_rollback
fi
if [ -s "$rollback" ]; then
  nft -c -f "$rollback" >/dev/null 2>&1 && nft -f "$rollback" >/dev/null 2>&1 || fail_rollback
fi

if [ "$persistent_before" = present ]; then
  atomic_restore "$transaction/persistent.snapshot" "$persistence" || fail_rollback
else
  [ ! -L "$persistence" ] || fail_rollback
  rm -f "$persistence" || fail_rollback
fi
if [ "$marker_before" = present ]; then
  atomic_restore "$transaction/marker.snapshot" "$marker" || fail_rollback
else
  [ ! -L "$marker" ] || fail_rollback
  rm -f "$marker" || fail_rollback
fi
if [ "$arming_before" = present ]; then
  atomic_restore "$transaction/arming.snapshot" "$arming" || fail_rollback
else
  [ ! -L "$arming" ] || fail_rollback
  rm -f "$arming" || fail_rollback
fi

write_status rollback_complete
write_recovery rollback_confirmed || fail_rollback
cd "$runtime_root" || fail_rollback
rm -rf "$transaction"
exit 0
ALPINEFORM_NFTABLES_WATCHDOG
chmod 0700 "$watchdog"

command -v nohup >/dev/null 2>&1
command -v setsid >/dev/null 2>&1

(
  cd "$transaction"
  exec nohup setsid sh ./watchdog.sh </dev/null >/dev/null 2>&1
) &
launcher_pid=$!
ready=false
attempt=0
while [ "$attempt" -lt 50 ]; do
  if [ -f "$transaction/watchdog.ready" ]; then
    watchdog_pid=$(sed -n '1p' "$transaction/watchdog.pid" 2>/dev/null || true)
    case "$watchdog_pid" in ''|*[!0-9]*) watchdog_pid= ;; esac
    if [ -n "$watchdog_pid" ] && kill -0 "$watchdog_pid" 2>/dev/null; then
      ready=true
      break
    fi
  fi
  if ! kill -0 "$launcher_pid" 2>/dev/null; then
    break
  fi
  sleep 0.1
  attempt=$((attempt + 1))
done
if [ "$ready" != true ]; then
  echo 'failed to start independent nftables rollback watchdog' >&2
  exit 1
fi
watchdog_started=true

build_activation
nft -c -f "$activation"
nft -f "$activation"

active_sha=absent
if [ "$ensure" = present ]; then
  nft list table "$family" "$name" >/dev/null
  active_sha=$(nft --stateless list table "$family" "$name" | sha256sum | awk '{print $1}')
else
  if nft list table "$family" "$name" >/dev/null 2>&1; then
    echo 'nftables table remained active after delete transaction' >&2
    exit 1
  fi
fi
printf '%s\n' "$active_sha" >"$transaction/expected.active.sha"
printf '%s\n' awaiting_confirmation >"$transaction/status"
chmod 0600 "$transaction/expected.active.sha" "$transaction/status"
prepare_complete=true
echo activation_pending_confirmation
`

const nftablesTransactionConfirmScript = `set -eu
token=$1
family=$2
name=$3
persistence=$4
marker=$5
fingerprint=$6
ensure=$7
runtime_root=/run/alpineform/nftables
transaction=$runtime_root/$token
candidate=$transaction/candidate.nft
expected_file=$transaction/expected.active.sha
action_lock=$transaction/action.lock
observed_root=/var/lib/alpineform/nftables/observed
recovery_root=/var/lib/alpineform/nftables/recovery
state_root=/var/lib/alpineform/nftables
arming=$state_root/armed
service=/etc/init.d/alpineform-nftables
runlevel_link=/etc/runlevels/default/alpineform-nftables
confirmation_complete=false
umask 077

finish_confirmation() {
  result=$?
  trap - EXIT HUP INT TERM
  if [ "$confirmation_complete" != true ] && [ -d "$transaction" ] && [ ! -L "$transaction" ]; then
    : >"$transaction/abort" 2>/dev/null || true
    rm -rf "$action_lock" 2>/dev/null || true
  fi
  exit "$result"
}
trap finish_confirmation EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

regular_file() {
  [ ! -L "$1" ] && [ -f "$1" ]
}

atomic_copy() {
  source=$1
  target=$2
  directory=${target%/*}
  regular_file "$source" || return 1
  if [ -L "$target" ] || { [ -e "$target" ] && [ ! -f "$target" ]; }; then
    return 1
  fi
  tmp=$(mktemp "$directory/.alpineform-nftables-confirm.XXXXXX")
  cp "$source" "$tmp"
  chown 0:0 "$tmp"
  chmod 0600 "$tmp"
  mv -f "$tmp" "$target"
}

write_recovery() {
  if [ -L "$recovery_root" ] || { [ -e "$recovery_root" ] && [ ! -d "$recovery_root" ]; }; then
    return 1
  fi
  mkdir -p "$recovery_root"
  chown 0:0 "$recovery_root"
  chmod 0700 "$recovery_root"
  recovery=$recovery_root/$family-$name.status
  if [ -L "$recovery" ] || { [ -e "$recovery" ] && [ ! -f "$recovery" ]; }; then
    return 1
  fi
  token_digest=$(printf '%s' "$token" | sha256sum | awk '{print $1}')
  tmp=$(mktemp "$recovery_root/.alpineform-nftables-recovery.XXXXXX")
  printf '%s\n%s\n' "$token_digest" "$1" >"$tmp"
  chown 0:0 "$tmp"
  chmod 0600 "$tmp"
  mv -f "$tmp" "$recovery"
}

[ "${transaction%/*}" = "$runtime_root" ]
[ ${#token} -eq 64 ]
case "$token" in *[!0-9a-f]*) exit 1 ;; esac
case "$family" in arp|bridge|inet|ip|ip6|netdev) ;; *) exit 1 ;; esac
case "$name" in ''|*[!A-Za-z0-9_-]*|[!A-Za-z_]*) exit 1 ;; esac
[ ${#name} -le 64 ]
case "$fingerprint" in *[!0-9a-f]*) exit 1 ;; esac
[ ${#fingerprint} -eq 64 ]
case "$ensure" in present|absent) ;; *) exit 1 ;; esac
[ "$persistence" = "/etc/nftables.d/alpineform/$family-$name.nft" ]
[ "$marker" = "/var/lib/alpineform/nftables/observed/$family-$name.digest" ]
[ ! -L "$transaction" ] && [ -d "$transaction" ]
regular_file "$transaction/identity"
regular_file "$transaction/fingerprint"
regular_file "$transaction/status"
regular_file "$expected_file"
[ "$(sed -n '1p' "$transaction/identity")" = "$family" ]
[ "$(sed -n '2p' "$transaction/identity")" = "$name" ]
[ "$(sed -n '3p' "$transaction/identity")" = "$ensure" ]
[ "$(sed -n '1p' "$transaction/fingerprint")" = "$fingerprint" ]
[ "$(sed -n '1p' "$transaction/status")" = awaiting_confirmation ]
[ ! -L "$service" ] && [ -f "$service" ]
[ -e "$runlevel_link" ]
service_status=$(rc-service alpineform-nftables status 2>&1 || true)
case "$service_status" in *"status: started"*) ;; *) exit 1 ;; esac
mkdir "$action_lock"
printf '%s\n' "$$" >"$action_lock/pid"
awk '{print $22}' "/proc/$$/stat" >"$action_lock/start"
chmod 0600 "$action_lock/pid" "$action_lock/start"
printf '%s\n' confirming >"$transaction/status"
chmod 0600 "$transaction/status"

expected=$(sed -n '1p' "$expected_file")
if [ "$ensure" = present ]; then
  regular_file "$candidate"
  nft list table "$family" "$name" >/dev/null
  active_sha=$(nft --stateless list table "$family" "$name" | sha256sum | awk '{print $1}')
  [ "$expected" = "$active_sha" ]
else
  [ "$expected" = absent ]
  if nft list table "$family" "$name" >/dev/null 2>&1; then
    exit 1
  fi
fi

if [ -L "$state_root" ] || { [ -e "$state_root" ] && [ ! -d "$state_root" ]; }; then
  exit 1
fi
mkdir -p "$state_root"
chown 0:0 "$state_root"
chmod 0700 "$state_root"
if [ -L "$observed_root" ] || { [ -e "$observed_root" ] && [ ! -d "$observed_root" ]; }; then
  exit 1
fi
mkdir -p "$observed_root"
chown 0:0 "$observed_root"
chmod 0700 "$observed_root"

if [ "$ensure" = present ]; then
	atomic_copy "$candidate" "$persistence"
	cmp -s "$candidate" "$persistence"
	marker_source=$transaction/confirmed.marker
  printf '%s\n%s\n%s\n' "$fingerprint" "$expected" "$ensure" >"$marker_source"
  chmod 0600 "$marker_source"
	atomic_copy "$marker_source" "$marker"
	cmp -s "$marker_source" "$marker"
  if [ -L "$arming" ] || { [ -e "$arming" ] && [ ! -f "$arming" ]; }; then
    exit 1
  fi
  arming_source=$transaction/confirmed.arming
  printf '%s\n' confirmed >"$arming_source"
  chmod 0600 "$arming_source"
	atomic_copy "$arming_source" "$arming"
	cmp -s "$arming_source" "$arming"
else
  if [ -L "$persistence" ] || [ -L "$marker" ]; then
    exit 1
  fi
	rm -f "$persistence" "$marker"
	[ ! -e "$persistence" ] && [ ! -e "$marker" ]
fi

write_recovery confirmed
: >"$transaction/confirmed"
chmod 0600 "$transaction/confirmed"
confirmation_complete=true
printf '%s\n' confirmed >"$transaction/status" 2>/dev/null || true
chmod 0600 "$transaction/status" 2>/dev/null || true

attempt=0
while [ "$attempt" -lt 50 ] && [ -d "$transaction" ]; do
  sleep 0.1
  attempt=$((attempt + 1))
done
echo confirmation_complete
`

const nftablesTransactionOutcomeScript = `set -eu
token=$1
family=$2
name=$3
runtime_root=/run/alpineform/nftables
recovery_root=/var/lib/alpineform/nftables/recovery
transaction=$runtime_root/$token
recovery=$recovery_root/$family-$name.status

[ ${#token} -eq 64 ]
case "$token" in *[!0-9a-f]*) exit 1 ;; esac
case "$family" in arp|bridge|inet|ip|ip6|netdev) ;; *) exit 1 ;; esac
case "$name" in ''|*[!A-Za-z0-9_-]*|[!A-Za-z_]*) exit 1 ;; esac
[ ${#name} -le 64 ]

outcome=missing
if [ ! -L "$transaction" ] && [ -d "$transaction" ]; then
  if [ -L "$transaction/status" ] || [ ! -f "$transaction/status" ]; then
    outcome=rollback_failed
  else
    status=$(sed -n '1p' "$transaction/status")
    case "$status" in
      preparing|awaiting_confirmation|confirming) outcome=pending ;;
      rollback_pending) outcome=rollback_pending ;;
      rollback_failed) outcome=rollback_failed ;;
      confirmed) outcome=confirmed ;;
      *) outcome=rollback_failed ;;
    esac
  fi
elif [ ! -L "$recovery" ] && [ -f "$recovery" ]; then
  token_digest=$(printf '%s' "$token" | sha256sum | awk '{print $1}')
  recorded_digest=$(sed -n '1p' "$recovery")
  recorded_outcome=$(sed -n '2p' "$recovery")
  if [ "$recorded_digest" = "$token_digest" ]; then
    case "$recorded_outcome" in
      activation_failed|pending|confirmed|rollback_pending|rollback_confirmed|rollback_failed) outcome=$recorded_outcome ;;
      *) outcome=rollback_failed ;;
    esac
  fi
fi
printf '%s\n' "$outcome"
`

const nftablesServiceInspectScript = `set -eu
directory=$1
init=$2
name=$3
runlevel=$4
path_state() {
  path=$1
  kind=$2
  if [ -L "$path" ]; then echo symlink; return; fi
  if [ ! -e "$path" ]; then echo missing; return; fi
  if [ "$kind" = directory ] && [ -d "$path" ]; then echo directory; return; fi
  if [ "$kind" = file ] && [ -f "$path" ]; then echo file; return; fi
  echo other
}
directory_state=$(path_state "$directory" directory)
init_state=$(path_state "$init" file)
directory_uid=-
directory_gid=-
directory_mode=-
init_uid=-
init_gid=-
init_mode=-
init_sha=-
if [ "$directory_state" = directory ]; then
  directory_uid=$(stat -c '%u' "$directory")
  directory_gid=$(stat -c '%g' "$directory")
  directory_mode=$(stat -c '%a' "$directory")
fi
if [ "$init_state" = file ]; then
  init_uid=$(stat -c '%u' "$init")
  init_gid=$(stat -c '%g' "$init")
  init_mode=$(stat -c '%a' "$init")
  init_sha=$(sha256sum "$init" | awk '{print $1}')
fi
if [ "$directory_state" = missing ] && [ "$init_state" = missing ]; then
  echo missing
  exit 0
fi
enabled=false
if [ -e "/etc/runlevels/$runlevel/$name" ]; then enabled=true; fi
runtime=inactive
if [ "$init_state" = file ]; then
  status_output=$(rc-service "$name" status 2>&1 || true)
  case "$status_output" in
    *"status: started"*) runtime=started ;;
    *"status: stopped"*) runtime=stopped ;;
    *"status: crashed"*) runtime=crashed ;;
  esac
fi
echo service
echo "$directory_state"
echo "$directory_uid"
echo "$directory_gid"
echo "$directory_mode"
echo "$init_state"
echo "$init_uid"
echo "$init_gid"
echo "$init_mode"
echo "$init_sha"
echo "$enabled"
echo "$runtime"
`

const nftablesServiceApplyScript = `set -eu
base=$1
directory=$2
init=$3
name=$4
runlevel=$5
if [ -L "$base" ] || { [ -e "$base" ] && [ ! -d "$base" ]; }; then
  echo 'refusing unsafe nftables base directory' >&2
  exit 1
fi
mkdir -p "$base"
if [ -L "$directory" ] || { [ -e "$directory" ] && [ ! -d "$directory" ]; }; then
  echo 'refusing unsafe AlpineForm nftables persistence directory' >&2
  exit 1
fi
mkdir -p "$directory"
chown 0:0 "$directory"
chmod 0700 "$directory"
if [ -L "$init" ]; then
  echo 'refusing symbolic-link AlpineForm nftables init target' >&2
  exit 1
fi
if [ -e "$init" ] && [ ! -f "$init" ]; then
  echo 'refusing non-regular AlpineForm nftables init target' >&2
  exit 1
fi
tmp=$(mktemp /etc/init.d/.alpineform-nftables.XXXXXX)
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
cat >"$tmp"
chown 0:0 "$tmp"
chmod 0755 "$tmp"
mv -f "$tmp" "$init"
trap - EXIT HUP INT TERM
if [ ! -e "/etc/runlevels/$runlevel/$name" ]; then
  rc-update add "$name" "$runlevel" >/dev/null
fi
status_output=$(rc-service "$name" status 2>&1 || true)
case "$status_output" in
  *"status: started"*) ;;
  *) rc-service "$name" start >/dev/null ;;
esac
`

func inspectNftablesPersistenceFile(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	_, _, path, err := desiredNftablesPersistenceIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{
		Name: "inspect.nftables_persistence", Script: nftablesPersistenceInspectScript,
		Arguments: []string{nftablesPersistenceDirectory, path}, RedactOutput: true,
	})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "" || lines[0] == "missing" {
		return engine.ObservedResource{}, nil
	}
	if lines[0] != "file" {
		if len(lines) != 1 {
			return engine.ObservedResource{}, fmt.Errorf("inspect protected nftables persistence returned an invalid response")
		}
		observed := cloneDesired(node.Desired)
		observed["persistence_state"] = lines[0]
		return engine.ObservedResource{Exists: true, Values: observed, Protected: true}, nil
	}
	if len(lines) != 6 {
		return engine.ObservedResource{}, fmt.Errorf("inspect protected nftables persistence returned an invalid response")
	}
	mode := lines[3]
	if len(mode) == 3 {
		mode = "0" + mode
	}
	size, err := strconv.ParseInt(lines[4], 10, 64)
	if err != nil {
		return engine.ObservedResource{}, fmt.Errorf("inspect protected nftables persistence returned invalid metadata")
	}
	observed := cloneDesired(node.Desired)
	observed["persistence_state"] = "file"
	observed["persistence_owner"] = lines[1]
	observed["persistence_group"] = lines[2]
	observed["persistence_mode"] = mode
	if !boolValue(node.Desired, "content_write_only") {
		observed["persistence_bytes"] = size
		observed["persistence_sha256"] = strings.ToLower(lines[5])
	}
	digest := ""
	delete(observed, "persistence_state")
	if corestate.Digest(observed) == corestate.Digest(node.Desired) {
		digest = corestate.Digest(node.Desired)
	}
	return engine.ObservedResource{Exists: true, Values: observed, Digest: digest, Protected: true}, nil
}

func inspectNftablesPersistence(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	family, name, _, marker, fingerprint, err := desiredNftablesTransactionIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	persistent, err := inspectNftablesPersistenceFile(ctx, runner, node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{
		Name: "inspect.nftables_active", Script: nftablesActiveInspectScript,
		Arguments: []string{family, name, marker}, RedactOutput: true,
	})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) != 6 || (lines[0] != "present" && lines[0] != "missing") {
		return engine.ObservedResource{}, fmt.Errorf("inspect protected nftables active table returned an invalid response")
	}
	markerState := lines[2]
	if markerState != "missing" && markerState != "file" && markerState != "symlink" && markerState != "other" {
		return engine.ObservedResource{}, fmt.Errorf("inspect protected nftables observed marker returned an invalid response")
	}
	observed := cloneDesired(node.Desired)
	if persistent.Exists {
		for key, value := range persistent.Values {
			observed[key] = value
		}
	} else {
		observed["persistence_state"] = "missing"
	}
	observed["active_state"] = lines[0]
	observed["active_sha256"] = lines[1]
	observed["observed_marker_state"] = markerState
	observed["observed_marker_fingerprint"] = lines[3]
	observed["observed_marker_active_sha256"] = lines[4]
	observed["observed_marker_ensure"] = lines[5]
	digest := ""
	managed := markerState == "file" && providerNftablesTokenPattern.MatchString(lines[3]) &&
		(providerNftablesTokenPattern.MatchString(lines[4]) || lines[4] == "absent") &&
		(lines[5] == "present" || lines[5] == "absent")
	if stringValue(node.Desired, "ensure") == "present" &&
		persistent.Digest == corestate.Digest(node.Desired) && lines[0] == "present" && markerState == "file" &&
		lines[3] == fingerprint && lines[4] == lines[1] && lines[5] == "present" {
		digest = corestate.Digest(node.Desired)
		observed = cloneDesired(node.Desired)
	}
	exists := persistent.Exists || lines[0] == "present" || markerState != "missing"
	return engine.ObservedResource{Exists: exists, Values: observed, Digest: digest, Protected: true, Managed: managed}, nil
}

func applyNftablesTransaction(ctx context.Context, runner backend.Runner, freshRunner func() (backend.Runner, error), step engine.Step, runtime nftablesTransactionRuntime) (engine.ObservedResource, error) {
	if stringValue(step.Node.Desired, "ensure") != "present" {
		return engine.ObservedResource{}, fmt.Errorf("nftables apply transaction requires ensure = \"present\"")
	}
	if step.Prior == nil && step.Observed.Exists && !step.Observed.Managed && !boolValue(step.Node.Desired, "adopt_existing") {
		return engine.ObservedResource{}, fmt.Errorf("refusing to replace an untracked nftables table; set adopt_existing = true to take ownership")
	}
	content, ok := step.Node.Payload["persistence_content"].(string)
	if !ok || content == "" {
		return engine.ObservedResource{}, fmt.Errorf("nftables transaction has no protected candidate payload")
	}
	confirmationRunner, err := runNftablesTransaction(ctx, runner, freshRunner, step.Node, content, runtime)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectNftablesPersistence(ctx, confirmationRunner, step.Node)
}

func deleteNftablesTransaction(ctx context.Context, runner backend.Runner, freshRunner func() (backend.Runner, error), step engine.Step, runtime nftablesTransactionRuntime) error {
	if step.Action != engine.ActionDelete || step.Prior == nil || step.Prior.Kind != "nftables_table" || step.Prior.Ownership != "managed" {
		return fmt.Errorf("nftables deletion requires a recorded AlpineForm-owned table")
	}
	deletion := step.Prior.Delete
	if desired, ok := step.Node.Desired["delete"].(map[string]any); ok {
		deletion = desired
	}
	family, _ := deletion["family"].(string)
	name, _ := deletion["name"].(string)
	persistence, _ := deletion["persistence_path"].(string)
	marker, _ := deletion["observed_marker_path"].(string)
	fingerprint, _ := deletion["activation_fingerprint"].(string)
	timeout, _ := deletion["rollback_timeout_seconds"].(int64)
	if timeout == 0 {
		if value, ok := deletion["rollback_timeout_seconds"].(float64); ok {
			timeout = int64(value)
		}
	}
	node := graph.Node{Kind: "nftables_table", Sensitive: true, Desired: map[string]any{
		"family": family, "name": name, "ensure": "absent", "persistence_path": persistence,
		"persistence_owner": "root", "persistence_group": "root", "persistence_mode": "0600",
		"observed_marker_path": marker, "activation_fingerprint": fingerprint,
		"rollback_timeout_seconds": timeout,
	}}
	_, err := runNftablesTransaction(ctx, runner, freshRunner, node, "", runtime)
	return err
}

func runNftablesTransaction(ctx context.Context, runner backend.Runner, freshRunner func() (backend.Runner, error), node graph.Node, candidate string, runtime nftablesTransactionRuntime) (backend.Runner, error) {
	runtime = runtime.normalized()
	family, name, persistence, marker, fingerprint, err := desiredNftablesTransactionIdentity(node)
	if err != nil {
		return nil, err
	}
	ensure := stringValue(node.Desired, "ensure")
	if ensure != "present" && ensure != "absent" {
		return nil, fmt.Errorf("nftables transaction has an unsupported desired state")
	}
	timeout := int64Value(node.Desired, "rollback_timeout_seconds")
	if timeout < 10 || timeout > 300 {
		return nil, fmt.Errorf("nftables transaction has an invalid rollback timeout")
	}
	if ensure == "present" {
		if candidate == "" {
			return nil, fmt.Errorf("nftables transaction candidate is empty")
		}
		if !boolValue(node.Desired, "content_write_only") {
			sum := sha256.Sum256([]byte(candidate))
			if fmt.Sprintf("%x", sum[:]) != stringValue(node.Desired, "persistence_sha256") {
				return nil, fmt.Errorf("nftables transaction candidate does not match its protected digest")
			}
		}
	}
	token, err := createNftablesToken(runtime.NewToken)
	if err != nil {
		return nil, err
	}
	_, prepareErr := runner.Run(ctx, backend.Command{
		Name: "apply.nftables_transaction_prepare", Script: nftablesTransactionPrepareScript,
		Arguments: []string{token, family, name, persistence, marker, fingerprint, ensure, strconv.FormatInt(timeout, 10)},
		Stdin:     []byte(candidate), RedactStdin: true, RedactOutput: true,
	})
	if freshRunner == nil {
		return nil, nftablesOutcomeError("rollback_pending")
	}
	return confirmOrRecoverNftablesTransaction(ctx, freshRunner, token, family, name, persistence, marker, fingerprint, ensure, timeout, prepareErr == nil, runtime)
}

func confirmOrRecoverNftablesTransaction(
	ctx context.Context,
	freshRunner func() (backend.Runner, error),
	token, family, name, persistence, marker, fingerprint, ensure string,
	timeout int64,
	prepared bool,
	runtime nftablesTransactionRuntime,
) (backend.Runner, error) {
	started := runtime.Now()
	confirmationDeadline := started.Add(time.Duration(timeout) * time.Second)
	recoveryDeadline := confirmationDeadline.Add(runtime.RecoveryGrace)
	backoff := nftablesInitialRetryBackoff
	lastOutcome := "pending"
	for {
		if ctx.Err() != nil {
			return nil, nftablesOutcomeError("rollback_pending")
		}
		now := runtime.Now()
		if prepared && now.Before(confirmationDeadline) {
			confirmationRunner, err := freshRunner()
			if err == nil && confirmationRunner != nil {
				attemptContext, cancel := nftablesAttemptContext(ctx, runtime.AttemptTimeout, confirmationDeadline.Sub(now))
				_, confirmErr := confirmationRunner.Run(attemptContext, backend.Command{
					Name: "apply.nftables_transaction_confirm", Script: nftablesTransactionConfirmScript,
					Arguments:    []string{token, family, name, persistence, marker, fingerprint, ensure},
					RedactOutput: true,
				})
				cancel()
				if confirmErr == nil {
					return confirmationRunner, nil
				}
			}
		}

		outcomeRunner, outcome, outcomeErr := inspectNftablesTransactionOutcome(ctx, freshRunner, token, family, name, runtime, recoveryDeadline.Sub(now))
		if outcomeErr == nil {
			lastOutcome = outcome
			switch outcome {
			case "confirmed":
				return outcomeRunner, nil
			case "rollback_confirmed":
				return nil, nftablesOutcomeError(outcome)
			case "rollback_failed":
				return nil, nftablesOutcomeError(outcome)
			case "activation_failed":
				return nil, nftablesOutcomeError(outcome)
			case "missing":
				if !prepared {
					return nil, nftablesOutcomeError("activation_failed")
				}
			}
		}
		if !runtime.Now().Before(recoveryDeadline) {
			if lastOutcome == "rollback_failed" {
				return nil, nftablesOutcomeError("rollback_failed")
			}
			return nil, nftablesOutcomeError("rollback_pending")
		}
		wait := backoff
		if remaining := recoveryDeadline.Sub(runtime.Now()); wait > remaining {
			wait = remaining
		}
		if err := runtime.Wait(ctx, wait); err != nil {
			return nil, nftablesOutcomeError("rollback_pending")
		}
		if backoff < nftablesMaximumRetryBackoff {
			backoff *= 2
			if backoff > nftablesMaximumRetryBackoff {
				backoff = nftablesMaximumRetryBackoff
			}
		}
	}
}

func inspectNftablesTransactionOutcome(
	ctx context.Context,
	freshRunner func() (backend.Runner, error),
	token, family, name string,
	runtime nftablesTransactionRuntime,
	remaining time.Duration,
) (backend.Runner, string, error) {
	runner, err := freshRunner()
	if err != nil {
		return nil, "", err
	}
	if runner == nil {
		return nil, "", fmt.Errorf("fresh nftables outcome runner is nil")
	}
	attemptContext, cancel := nftablesAttemptContext(ctx, runtime.AttemptTimeout, remaining)
	defer cancel()
	output, err := runner.Run(attemptContext, backend.Command{
		Name: "inspect.nftables_transaction_outcome", Script: nftablesTransactionOutcomeScript,
		Arguments: []string{token, family, name}, RedactOutput: true,
	})
	if err != nil {
		return nil, "", err
	}
	outcome := strings.TrimSpace(string(output))
	switch outcome {
	case "activation_failed", "missing", "pending", "confirmed", "rollback_pending", "rollback_confirmed", "rollback_failed":
		return runner, outcome, nil
	default:
		return nil, "", fmt.Errorf("inspect protected nftables transaction returned an invalid outcome")
	}
}

func nftablesAttemptContext(ctx context.Context, maximum, remaining time.Duration) (context.Context, context.CancelFunc) {
	if remaining > 0 && maximum > remaining {
		maximum = remaining
	}
	if maximum <= 0 {
		maximum = time.Millisecond
	}
	return context.WithTimeout(ctx, maximum)
}

func nftablesOutcomeError(outcome string) error {
	message := "nftables activation failed; rollback status: pending"
	switch outcome {
	case "activation_failed":
		message = "nftables activation failed before confirmation; rollback status: not required"
	case "rollback_confirmed":
		message = "nftables management-path confirmation failed; rollback status: confirmed"
	case "rollback_failed":
		message = "nftables management-path confirmation failed; rollback status: failed; target-side recovery is required"
	case "rollback_pending":
		message = "nftables management-path confirmation failed; rollback status: pending; the remote watchdog remains armed"
	}
	return engine.NewSafeOperationError(message)
}

func createNftablesToken(factory func() (string, error)) (string, error) {
	if factory != nil {
		token, err := factory()
		if err != nil {
			return "", fmt.Errorf("create protected nftables transaction token: %w", err)
		}
		if !providerNftablesTokenPattern.MatchString(token) {
			return "", fmt.Errorf("protected nftables transaction token factory returned an invalid token")
		}
		return token, nil
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("create protected nftables transaction token: %w", err)
	}
	return hex.EncodeToString(random), nil
}

func desiredNftablesPersistenceIdentity(node graph.Node) (string, string, string, error) {
	family := stringValue(node.Desired, "family")
	name := stringValue(node.Desired, "name")
	path := stringValue(node.Desired, "persistence_path")
	if !validNftablesIdentity(family, name) || path != nftablesPersistencePath(family, name) {
		return "", "", "", fmt.Errorf("invalid protected nftables persistence identity")
	}
	if stringValue(node.Desired, "persistence_owner") != "root" || stringValue(node.Desired, "persistence_group") != "root" || stringValue(node.Desired, "persistence_mode") != "0600" {
		return "", "", "", fmt.Errorf("invalid protected nftables persistence metadata")
	}
	return family, name, path, nil
}

func desiredNftablesTransactionIdentity(node graph.Node) (string, string, string, string, string, error) {
	family, name, persistence, err := desiredNftablesPersistenceIdentity(node)
	if err != nil {
		return "", "", "", "", "", err
	}
	marker := stringValue(node.Desired, "observed_marker_path")
	fingerprint := stringValue(node.Desired, "activation_fingerprint")
	if marker != nftablesObservedMarkerPath(family, name) || !providerNftablesTokenPattern.MatchString(fingerprint) {
		return "", "", "", "", "", fmt.Errorf("invalid protected nftables transaction identity")
	}
	return family, name, persistence, marker, fingerprint, nil
}

func validNftablesIdentity(family, name string) bool {
	switch family {
	case "arp", "bridge", "inet", "ip", "ip6", "netdev":
	default:
		return false
	}
	return providerNftablesIdentityPattern.MatchString(name)
}

func nftablesPersistencePath(family, name string) string {
	return nftablesPersistenceDirectory + "/" + family + "-" + name + ".nft"
}

func nftablesObservedMarkerPath(family, name string) string {
	return nftablesObservedDirectory + "/" + family + "-" + name + ".digest"
}

func int64Value(values map[string]any, name string) int64 {
	switch value := values[name].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func inspectNftablesService(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	if err := validateNftablesServiceNode(node); err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{
		Name: "inspect.nftables_service", Script: nftablesServiceInspectScript,
		Arguments: []string{nftablesPersistenceDirectory, nftablesOpenRCInitPath, nftablesOpenRCName, "default"},
	})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "" || lines[0] == "missing" {
		return engine.ObservedResource{}, nil
	}
	if len(lines) != 12 || lines[0] != "service" || (lines[10] != "true" && lines[10] != "false") {
		return engine.ObservedResource{}, fmt.Errorf("inspect AlpineForm nftables service returned an invalid response")
	}
	directoryMode := lines[4]
	if len(directoryMode) == 3 {
		directoryMode = "0" + directoryMode
	}
	initMode := lines[8]
	if len(initMode) == 3 {
		initMode = "0" + initMode
	}
	observed := cloneDesired(node.Desired)
	observed["persistence_directory_state"] = lines[1]
	observed["persistence_directory_owner"] = lines[2]
	observed["persistence_directory_group"] = lines[3]
	observed["persistence_directory_mode"] = directoryMode
	observed["init_state"] = lines[5]
	observed["init_owner"] = lines[6]
	observed["init_group"] = lines[7]
	observed["init_mode"] = initMode
	observed["init_sha256"] = lines[9]
	observed["enabled"] = lines[10] == "true"
	state := "stopped"
	if lines[11] == "started" {
		state = "running"
	} else if lines[11] == "crashed" {
		state = "crashed"
	}
	observed["state"] = state
	observed["runtime_status"] = lines[11]
	digest := ""
	if lines[1] == "directory" && lines[2] == "0" && lines[3] == "0" && directoryMode == "0700" &&
		lines[5] == "file" && lines[6] == "0" && lines[7] == "0" && initMode == "0755" &&
		lines[9] == stringValue(node.Desired, "init_sha256") && lines[10] == "true" && lines[11] == "started" {
		digest = corestate.Digest(node.Desired)
		observed = cloneDesired(node.Desired)
	}
	return engine.ObservedResource{Exists: true, Values: observed, Digest: digest}, nil
}

func applyNftablesService(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	if err := validateNftablesServiceNode(node); err != nil {
		return engine.ObservedResource{}, err
	}
	initScript, ok := node.Payload["init_script"].(string)
	if !ok || initScript == "" || strings.Contains(initScript, "flush ruleset") {
		return engine.ObservedResource{}, fmt.Errorf("AlpineForm nftables service has an unsafe init payload")
	}
	sum := sha256.Sum256([]byte(initScript))
	if fmt.Sprintf("%x", sum[:]) != stringValue(node.Desired, "init_sha256") {
		return engine.ObservedResource{}, fmt.Errorf("AlpineForm nftables init payload does not match its digest")
	}
	_, err := runner.Run(ctx, backend.Command{
		Name: "apply.nftables_service", Script: nftablesServiceApplyScript,
		Arguments: []string{"/etc/nftables.d", nftablesPersistenceDirectory, nftablesOpenRCInitPath, nftablesOpenRCName, "default"},
		Stdin:     []byte(initScript), RedactStdin: true,
	})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectNftablesService(ctx, runner, node)
}

func validateNftablesServiceNode(node graph.Node) error {
	if stringValue(node.Desired, "name") != nftablesOpenRCName || stringValue(node.Desired, "runlevel") != "default" ||
		stringValue(node.Desired, "init_path") != nftablesOpenRCInitPath || stringValue(node.Desired, "init_mode") != "0755" ||
		stringValue(node.Desired, "persistence_directory") != nftablesPersistenceDirectory || stringValue(node.Desired, "persistence_directory_mode") != "0700" ||
		!boolValue(node.Desired, "enabled") || stringValue(node.Desired, "state") != "running" || stringValue(node.Desired, "ensure") != "present" {
		return fmt.Errorf("invalid AlpineForm nftables service contract")
	}
	return nil
}

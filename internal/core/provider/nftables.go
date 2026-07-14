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
)

var providerNftablesIdentityPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)
var providerNftablesTokenPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

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

const nftablesTransactionScript = `set -eu
token=$1
family=$2
name=$3
persistence=$4
marker=$5
fingerprint=$6
ensure=$7
runtime_root=/run/alpineform/nftables
runtime_parent=/run/alpineform
observed_root=/var/lib/alpineform/nftables/observed
observed_parent=/var/lib/alpineform/nftables
state_parent=/var/lib/alpineform
persistence_base=/etc/nftables.d
persistence_directory=/etc/nftables.d/alpineform
transaction=$runtime_root/$token
candidate=$transaction/candidate.nft
activation=$transaction/activation.nft
active_snapshot=$transaction/active.snapshot.nft
persistent_snapshot=$transaction/persistent.snapshot
marker_snapshot=$transaction/marker.snapshot
active_before=missing
persistent_before=missing
marker_before=missing
activated=false
success=false
umask 077

safe_directory() {
  path=$1
  if [ -L "$path" ] || { [ -e "$path" ] && [ ! -d "$path" ]; }; then
    return 1
  fi
  return 0
}

atomic_restore() {
  snapshot=$1
  target=$2
  directory=${target%/*}
  if [ -L "$target" ] || { [ -e "$target" ] && [ ! -f "$target" ]; }; then
    return 1
  fi
  tmp=$(mktemp "$directory/.alpineform-nftables-restore.XXXXXX")
  cp "$snapshot" "$tmp"
  chown 0:0 "$tmp"
  chmod 0600 "$tmp"
  mv -f "$tmp" "$target"
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

restore_transaction() {
  rollback=$transaction/rollback.nft
  : >"$rollback"
  if nft list table "$family" "$name" >/dev/null 2>&1; then
    printf 'delete table %s %s\n' "$family" "$name" >>"$rollback"
  fi
  if [ "$active_before" = present ]; then
    cat "$active_snapshot" >>"$rollback"
  fi
  if [ -s "$rollback" ]; then
    nft -c -f "$rollback" && nft -f "$rollback" || return 1
  fi

  if [ "$persistent_before" = present ]; then
    atomic_restore "$persistent_snapshot" "$persistence" || return 1
  else
    rm -f "$persistence" || return 1
  fi
  if [ "$marker_before" = present ]; then
    atomic_restore "$marker_snapshot" "$marker" || return 1
  else
    rm -f "$marker" || return 1
  fi
}

finish_transaction() {
  status=$?
  trap - EXIT HUP INT TERM
  if [ "$success" = true ]; then
    exit "$status"
  fi
  if [ "$activated" = true ]; then
    if restore_transaction; then
      rm -rf "$transaction"
    else
      printf '%s\n' rollback_failed >"$transaction/status"
      chmod 0600 "$transaction/status"
      echo 'nftables transaction failed and rollback requires recovery' >&2
      exit 70
    fi
  else
    rm -rf "$transaction"
  fi
  exit "$status"
}
trap finish_transaction EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

safe_directory "$runtime_parent"
mkdir -p "$runtime_parent"
safe_directory "$runtime_root"
mkdir -p "$runtime_root"
chown 0:0 "$runtime_root"
chmod 0700 "$runtime_root"
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

if [ "$active_before" = present ]; then
  restore_check=$transaction/restore.check.nft
  printf 'delete table %s %s\n' "$family" "$name" >"$restore_check"
  cat "$active_snapshot" >>"$restore_check"
  chmod 0600 "$restore_check"
  nft -c -f "$restore_check"
fi

build_activation
nft -c -f "$activation"
nft -f "$activation"
activated=true

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

if [ "$ensure" = present ]; then
  if [ -L "$persistence" ] || { [ -e "$persistence" ] && [ ! -f "$persistence" ]; }; then
    echo 'refusing changed nftables persistence target' >&2
    exit 1
  fi
  tmp=$(mktemp "$persistence_directory/.alpineform-nftables.XXXXXX")
  cp "$candidate" "$tmp"
  chown 0:0 "$tmp"
  chmod 0600 "$tmp"
  mv -f "$tmp" "$persistence"
  if [ -L "$marker" ] || { [ -e "$marker" ] && [ ! -f "$marker" ]; }; then
    echo 'refusing changed nftables observed marker target' >&2
    exit 1
  fi
  marker_tmp=$(mktemp "$observed_root/.alpineform-nftables.XXXXXX")
  printf '%s\n%s\n%s\n' "$fingerprint" "$active_sha" "$ensure" >"$marker_tmp"
  chown 0:0 "$marker_tmp"
  chmod 0600 "$marker_tmp"
  mv -f "$marker_tmp" "$marker"
else
  if [ -L "$persistence" ] || [ -L "$marker" ]; then
    echo 'refusing changed nftables delete target' >&2
    exit 1
  fi
  rm -f "$persistence" "$marker"
fi

rm -rf "$transaction"
success=true
echo transaction_complete
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
	if stringValue(node.Desired, "ensure") == "present" &&
		persistent.Digest == corestate.Digest(node.Desired) && lines[0] == "present" && markerState == "file" &&
		lines[3] == fingerprint && lines[4] == lines[1] && lines[5] == "present" {
		digest = corestate.Digest(node.Desired)
		observed = cloneDesired(node.Desired)
	}
	exists := persistent.Exists || lines[0] == "present" || markerState != "missing"
	return engine.ObservedResource{Exists: exists, Values: observed, Digest: digest, Protected: true}, nil
}

func applyNftablesTransaction(ctx context.Context, runner backend.Runner, step engine.Step, newToken func() (string, error)) (engine.ObservedResource, error) {
	if stringValue(step.Node.Desired, "ensure") != "present" {
		return engine.ObservedResource{}, fmt.Errorf("nftables apply transaction requires ensure = \"present\"")
	}
	if step.Prior == nil && step.Observed.Exists && !boolValue(step.Node.Desired, "adopt_existing") {
		return engine.ObservedResource{}, fmt.Errorf("refusing to replace an untracked nftables table; set adopt_existing = true to take ownership")
	}
	content, ok := step.Node.Payload["persistence_content"].(string)
	if !ok || content == "" {
		return engine.ObservedResource{}, fmt.Errorf("nftables transaction has no protected candidate payload")
	}
	if err := runNftablesTransaction(ctx, runner, step.Node, content, newToken); err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectNftablesPersistence(ctx, runner, step.Node)
}

func deleteNftablesTransaction(ctx context.Context, runner backend.Runner, step engine.Step, newToken func() (string, error)) error {
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
	return runNftablesTransaction(ctx, runner, node, "", newToken)
}

func runNftablesTransaction(ctx context.Context, runner backend.Runner, node graph.Node, candidate string, newToken func() (string, error)) error {
	family, name, persistence, marker, fingerprint, err := desiredNftablesTransactionIdentity(node)
	if err != nil {
		return err
	}
	ensure := stringValue(node.Desired, "ensure")
	if ensure != "present" && ensure != "absent" {
		return fmt.Errorf("nftables transaction has an unsupported desired state")
	}
	timeout := int64Value(node.Desired, "rollback_timeout_seconds")
	if timeout < 10 || timeout > 300 {
		return fmt.Errorf("nftables transaction has an invalid rollback timeout")
	}
	if ensure == "present" {
		if candidate == "" {
			return fmt.Errorf("nftables transaction candidate is empty")
		}
		if !boolValue(node.Desired, "content_write_only") {
			sum := sha256.Sum256([]byte(candidate))
			if fmt.Sprintf("%x", sum[:]) != stringValue(node.Desired, "persistence_sha256") {
				return fmt.Errorf("nftables transaction candidate does not match its protected digest")
			}
		}
	}
	token, err := createNftablesToken(newToken)
	if err != nil {
		return err
	}
	_, err = runner.Run(ctx, backend.Command{
		Name: "apply.nftables_transaction", Script: nftablesTransactionScript,
		Arguments: []string{token, family, name, persistence, marker, fingerprint, ensure},
		Stdin:     []byte(candidate), RedactStdin: true, RedactOutput: true,
	})
	return err
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

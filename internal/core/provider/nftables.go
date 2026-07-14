package provider

import (
	"context"
	"crypto/sha256"
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
)

var providerNftablesIdentityPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)

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

const nftablesPersistenceWriteScript = `set -eu
base=$1
directory=$2
path=$3
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
if [ -L "$path" ]; then
  echo 'refusing symbolic-link nftables persistence target' >&2
  exit 1
fi
if [ -e "$path" ] && [ ! -f "$path" ]; then
  echo 'refusing non-regular nftables persistence target' >&2
  exit 1
fi
tmp=$(mktemp "$directory/.alpineform-nftables.XXXXXX")
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
cat >"$tmp"
chown 0:0 "$tmp"
chmod 0600 "$tmp"
mv -f "$tmp" "$path"
trap - EXIT HUP INT TERM
`

const nftablesPersistenceDeleteScript = `set -eu
directory=$1
path=$2
if [ -L "$directory" ] || { [ -e "$directory" ] && [ ! -d "$directory" ]; }; then
  echo 'refusing unsafe AlpineForm nftables persistence directory' >&2
  exit 1
fi
if [ -L "$path" ]; then
  echo 'refusing symbolic-link nftables persistence target' >&2
  exit 1
fi
if [ -e "$path" ] && [ ! -f "$path" ]; then
  echo 'refusing non-regular nftables persistence target' >&2
  exit 1
fi
rm -f "$path"
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

func inspectNftablesPersistence(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
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

func applyNftablesPersistence(ctx context.Context, runner backend.Runner, step engine.Step) (engine.ObservedResource, error) {
	_, _, path, err := desiredNftablesPersistenceIdentity(step.Node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if stringValue(step.Node.Desired, "ensure") != "present" {
		return engine.ObservedResource{}, fmt.Errorf("nftables persistence apply requires ensure = \"present\"")
	}
	if step.Prior == nil && step.Observed.Exists && !boolValue(step.Node.Desired, "adopt_existing") {
		return engine.ObservedResource{}, fmt.Errorf("refusing to overwrite an untracked nftables table; set adopt_existing = true to take ownership")
	}
	content, ok := step.Node.Payload["persistence_content"].(string)
	if !ok || content == "" {
		return engine.ObservedResource{}, fmt.Errorf("nftables persistence has no protected provider payload")
	}
	if !boolValue(step.Node.Desired, "content_write_only") {
		sum := sha256.Sum256([]byte(content))
		if fmt.Sprintf("%x", sum[:]) != stringValue(step.Node.Desired, "persistence_sha256") {
			return engine.ObservedResource{}, fmt.Errorf("nftables persistence payload does not match its protected digest")
		}
	}
	_, err = runner.Run(ctx, backend.Command{
		Name: "apply.nftables_persistence", Script: nftablesPersistenceWriteScript,
		Arguments: []string{"/etc/nftables.d", nftablesPersistenceDirectory, path},
		Stdin:     []byte(content), RedactStdin: true, RedactOutput: true,
	})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectNftablesPersistence(ctx, runner, step.Node)
}

func deleteNftablesPersistence(ctx context.Context, runner backend.Runner, step engine.Step) error {
	if step.Action != engine.ActionDelete || step.Prior == nil || step.Prior.Kind != "nftables_table" || step.Prior.Ownership != "managed" {
		return fmt.Errorf("nftables deletion requires a recorded AlpineForm-owned table")
	}
	family, name, path, err := nftablesDeleteIdentity(step)
	if err != nil {
		return err
	}
	if path != nftablesPersistencePath(family, name) {
		return fmt.Errorf("recorded nftables persistence path does not match the owned table identity")
	}
	_, err = runner.Run(ctx, backend.Command{
		Name: "delete.nftables_persistence", Script: nftablesPersistenceDeleteScript,
		Arguments: []string{nftablesPersistenceDirectory, path}, RedactOutput: true,
	})
	return err
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

func nftablesDeleteIdentity(step engine.Step) (string, string, string, error) {
	deletion := map[string]any(nil)
	if step.Node.Desired != nil {
		deletion, _ = step.Node.Desired["delete"].(map[string]any)
	}
	if deletion == nil && step.Prior != nil {
		deletion = step.Prior.Delete
	}
	family, _ := deletion["family"].(string)
	name, _ := deletion["name"].(string)
	path, _ := deletion["persistence_path"].(string)
	if !validNftablesIdentity(family, name) {
		return "", "", "", fmt.Errorf("invalid recorded nftables table identity")
	}
	return family, name, path, nil
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

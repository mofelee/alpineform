package provider

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

var (
	providerKernelModulePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	providerSysctlKeyPattern    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,255}$`)
)

const kernelModuleInspectScript = `set -eu
name=$1
path=$2
kname=$(printf '%s' "$name" | tr '-' '_')
class=missing
if awk -v module="$kname" '$1 == module { found=1 } END { exit !found }' /proc/modules; then
  class=loaded
elif [ -d "/sys/module/$kname" ]; then
  class=builtin
elif modprobe -n "$name" >/dev/null 2>&1; then
  class=available
fi
persisted=false
if [ -L "$path" ]; then echo 'refusing symbolic-link kernel module persistence file' >&2; exit 1; fi
if [ -e "$path" ]; then
  if [ ! -f "$path" ]; then echo 'kernel module persistence path is not a regular file' >&2; exit 1; fi
  [ "$(cat "$path")" = "$name" ] && persisted=true
fi
echo module
echo "$class"
echo "$persisted"
`

const kernelModuleApplyScript = `set -eu
name=$1
path=$2
if ! modprobe "$name"; then echo "failed to load kernel module $name" >&2; exit 1; fi
if [ -L "$path" ]; then echo 'refusing symbolic-link kernel module persistence file' >&2; exit 1; fi
mkdir -p /etc/modules-load.d
tmp=$(mktemp /etc/modules-load.d/.alpineform-module.XXXXXX)
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
printf '%s\n' "$name" >"$tmp"
chmod 0644 "$tmp"
chown root:root "$tmp"
mv -f "$tmp" "$path"
trap - EXIT HUP INT TERM
`

const sysctlInspectScript = `set -eu
key=$1
value=$2
path=$3
apply_runtime=$4
runtime_matches=true
runtime_value=
if [ "$apply_runtime" = true ]; then
  runtime_value=$(sysctl -n "$key" 2>/dev/null || true)
  [ "$runtime_value" = "$value" ] || runtime_matches=false
fi
persisted=false
path_exists=false
if [ -L "$path" ]; then echo 'refusing symbolic-link sysctl persistence file' >&2; exit 1; fi
if [ -e "$path" ]; then
  path_exists=true
  if [ ! -f "$path" ]; then echo 'sysctl persistence path is not a regular file' >&2; exit 1; fi
  [ "$(cat "$path")" = "$key = $value" ] && persisted=true
fi
echo sysctl
echo "$runtime_matches"
echo "$persisted"
echo "$runtime_value"
echo "$path_exists"
`

const sysctlPersistScript = `set -eu
key=$1
value=$2
path=$3
if [ -L "$path" ]; then echo 'refusing symbolic-link sysctl persistence file' >&2; exit 1; fi
mkdir -p /etc/sysctl.d
tmp=$(mktemp /etc/sysctl.d/.alpineform-sysctl.XXXXXX)
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
printf '%s = %s\n' "$key" "$value" >"$tmp"
chmod 0644 "$tmp"
chown root:root "$tmp"
mv -f "$tmp" "$path"
trap - EXIT HUP INT TERM
`

const sysctlRuntimeApplyScript = `set -eu
while [ "$#" -gt 0 ]; do
  if [ "$#" -lt 2 ]; then echo 'invalid sysctl runtime argument pairs' >&2; exit 1; fi
  key=$1
  value=$2
  shift 2
  sysctl -w "$key=$value" >/dev/null
done
`

const sysctlDeleteScript = `set -eu
path=$1
if [ -L "$path" ]; then echo 'refusing symbolic-link sysctl persistence file' >&2; exit 1; fi
if [ -e "$path" ] && [ ! -f "$path" ]; then echo 'sysctl persistence path is not a regular file' >&2; exit 1; fi
rm -f "$path"
`

func inspectKernelModule(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	name, path, err := desiredKernelModule(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.kernel_module", Script: kernelModuleInspectScript, Arguments: []string{name, path}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) != 3 || lines[0] != "module" || (lines[2] != "true" && lines[2] != "false") {
		return engine.ObservedResource{}, fmt.Errorf("inspect kernel module %q returned an invalid response", name)
	}
	if lines[1] != "loaded" && lines[1] != "builtin" && lines[1] != "available" && lines[1] != "missing" {
		return engine.ObservedResource{}, fmt.Errorf("inspect kernel module %q returned unknown class %q", name, lines[1])
	}
	observed := cloneDesired(node.Desired)
	observed["class"] = lines[1]
	observed["persisted"] = lines[2] == "true"
	digest := ""
	if (lines[1] == "loaded" || lines[1] == "builtin") && lines[2] == "true" {
		digest = corestate.Digest(node.Desired)
	}
	exists := lines[1] != "missing" || lines[2] == "true"
	return engine.ObservedResource{Exists: exists, Values: observed, Digest: digest}, nil
}

func applyKernelModule(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	name, path, err := desiredKernelModule(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if _, err := runner.Run(ctx, backend.Command{Name: "apply.kernel_module", Script: kernelModuleApplyScript, Arguments: []string{name, path}}); err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectKernelModule(ctx, runner, node)
}

func inspectSysctl(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	key, value, path, applyRuntime, err := desiredSysctl(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.sysctl", Script: sysctlInspectScript, Arguments: []string{key, value, path, fmt.Sprintf("%t", applyRuntime)}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(lines) != 5 || lines[0] != "sysctl" || (lines[1] != "true" && lines[1] != "false") || (lines[2] != "true" && lines[2] != "false") || (lines[4] != "true" && lines[4] != "false") {
		return engine.ObservedResource{}, fmt.Errorf("inspect sysctl %q returned an invalid response", key)
	}
	observed := cloneDesired(node.Desired)
	observed["runtime_matches"] = lines[1] == "true"
	observed["persisted"] = lines[2] == "true"
	observed["runtime_value"] = lines[3]
	digest := ""
	if lines[1] == "true" && lines[2] == "true" {
		digest = corestate.Digest(node.Desired)
	}
	return engine.ObservedResource{Exists: lines[4] == "true", Values: observed, Digest: digest}, nil
}

func applySysctl(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	_, _, path, _, err := desiredSysctl(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	key := stringValue(node.Desired, "key")
	value := stringValue(node.Desired, "value")
	if _, err := runner.Run(ctx, backend.Command{Name: "apply.sysctl", Script: sysctlPersistScript, Arguments: []string{key, value, path}}); err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectSysctl(ctx, runner, node)
}

func inspectSysctlRuntime(node graph.Node) (engine.ObservedResource, error) {
	entries, err := desiredSysctlRuntimeEntries(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	_ = entries
	return engine.ObservedResource{Exists: true, Values: cloneDesired(node.Desired), Digest: corestate.Digest(node.Desired)}, nil
}

func applySysctlRuntime(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	entries, err := desiredSysctlRuntimeEntries(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if _, err := runner.Run(ctx, backend.Command{Name: "apply.sysctl_runtime", Script: sysctlRuntimeApplyScript, Arguments: entries}); err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectSysctlRuntime(node)
}

func deleteSysctl(ctx context.Context, runner backend.Runner, step engine.Step) error {
	key := ""
	if step.Node.Desired != nil {
		key = stringValue(step.Node.Desired, "key")
	}
	if key == "" && step.Prior != nil {
		key, _ = step.Prior.Delete["key"].(string)
	}
	if !validProviderSysctlKey(key) {
		return fmt.Errorf("invalid sysctl key %q", key)
	}
	_, err := runner.Run(ctx, backend.Command{Name: "delete.sysctl", Script: sysctlDeleteScript, Arguments: []string{sysctlPersistencePath(key)}})
	return err
}

func desiredKernelModule(node graph.Node) (string, string, error) {
	name := stringValue(node.Desired, "name")
	if !providerKernelModulePattern.MatchString(name) {
		return "", "", fmt.Errorf("invalid kernel module name %q", name)
	}
	return name, kernelModulePersistencePath(name), nil
}

func desiredSysctl(node graph.Node) (string, string, string, bool, error) {
	key := stringValue(node.Desired, "key")
	value := stringValue(node.Desired, "value")
	if !validProviderSysctlKey(key) || value == "" || len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
		return "", "", "", false, fmt.Errorf("invalid sysctl key or value")
	}
	return key, value, sysctlPersistencePath(key), boolValue(node.Desired, "apply_runtime"), nil
}

func desiredSysctlRuntimeEntries(node graph.Node) ([]string, error) {
	entries, ok := node.Desired["entries"].([]string)
	if !ok || len(entries) == 0 || len(entries)%2 != 0 {
		return nil, fmt.Errorf("invalid sysctl runtime entries")
	}
	for index := 0; index < len(entries); index += 2 {
		if !validProviderSysctlKey(entries[index]) || entries[index+1] == "" || strings.ContainsAny(entries[index+1], "\x00\r\n") {
			return nil, fmt.Errorf("invalid sysctl runtime entry")
		}
	}
	return append([]string(nil), entries...), nil
}

func validProviderSysctlKey(key string) bool {
	return providerSysctlKeyPattern.MatchString(key) && !strings.Contains(key, "..") && !strings.HasSuffix(key, ".")
}

func kernelModulePersistencePath(name string) string {
	return "/etc/modules-load.d/alpineform-" + name + ".conf"
}

func sysctlPersistencePath(key string) string {
	sum := sha256.Sum256([]byte(key))
	safe := strings.NewReplacer(".", "_", "-", "_").Replace(key)
	if len(safe) > 96 {
		safe = safe[:96]
	}
	return fmt.Sprintf("/etc/sysctl.d/99-alpineform-%s-%x.conf", safe, sum[:4])
}

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
)

const componentScriptInspectOutputsScript = `set -eu
marker=$1
want_script=$2
shift 2
if [ ! -f "$marker" ]; then
  echo drift
  exit 0
fi
expected=$(($# + 1))
actual=$(wc -l <"$marker" | tr -d ' ')
if [ "$actual" != "$expected" ]; then
  echo drift
  exit 0
fi
if ! grep -Fqx "script  $want_script" "$marker"; then
  echo drift
  exit 0
fi
for path do
  if [ ! -f "$path" ] || [ -L "$path" ]; then
    echo drift
    exit 0
  fi
  digest=$(sha256sum "$path" | awk '{print $1}')
  if ! grep -Fqx "$digest  $path" "$marker"; then
    echo drift
    exit 0
  fi
done
echo clean
`

const componentScriptExecuteContentScript = `set -eu
name=$1
addresses=$2
paths=$3
shift 3
export APF_SCRIPT_NAME=$name
export APF_TRIGGER_ADDRESS=$(printf '%s\n' "$addresses" | sed -n '1p')
export APF_TRIGGER_PATH=$(printf '%s\n' "$paths" | sed -n '1p')
export APF_TRIGGER_ADDRESSES=$addresses
export APF_TRIGGER_PATHS=$paths
exec "$@"
`

const componentScriptExecuteCommandScript = `set -eu
name=$1
addresses=$2
paths=$3
shift 3
export APF_SCRIPT_NAME=$name
export APF_TRIGGER_ADDRESS=$(printf '%s\n' "$addresses" | sed -n '1p')
export APF_TRIGGER_PATH=$(printf '%s\n' "$paths" | sed -n '1p')
export APF_TRIGGER_ADDRESSES=$addresses
export APF_TRIGGER_PATHS=$paths
exec "$@"
`

const componentScriptRecordOutputsScript = `set -eu
marker=$1
script_digest=$2
shift 2
parent=${marker%/*}
[ -n "$parent" ] || parent=/
mkdir -p "$parent"
tmp=$(mktemp "$parent/.alpineform-script-output.XXXXXX")
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
printf 'script  %s\n' "$script_digest" >"$tmp"
for path do
  if [ ! -f "$path" ] || [ -L "$path" ]; then
    echo "script output is missing or not a regular file: $path" >&2
    exit 1
  fi
  digest=$(sha256sum "$path" | awk '{print $1}')
  printf '%s  %s\n' "$digest" "$path" >>"$tmp"
done
chmod 0600 "$tmp"
mv -f "$tmp" "$marker"
trap - EXIT HUP INT TERM
`

func inspectComponentScript(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	observed := cloneDesired(node.Desired)
	outputs, err := scriptPayloadStringList(node, "outputs")
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if len(outputs) == 0 {
		return engine.ObservedResource{Exists: true, Values: observed, Protected: node.Sensitive}, nil
	}
	marker := stringValue(node.Desired, "marker_path")
	if err := validateRemoteFilePath(marker); err != nil {
		return engine.ObservedResource{}, fmt.Errorf("component script output marker: %w", err)
	}
	scriptDigest := stringValue(node.Desired, "script_digest")
	arguments := append([]string{marker, scriptDigest}, outputs...)
	output, err := runner.Run(ctx, backend.Command{
		Name: "inspect.component_script", Script: componentScriptInspectOutputsScript,
		Arguments: arguments, RedactOutput: node.Sensitive,
	})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if strings.TrimSpace(string(output)) != "clean" {
		observed["outputs_integrity"] = "drift"
	}
	return engine.ObservedResource{Exists: true, Values: observed, Protected: node.Sensitive}, nil
}

func applyComponentScript(ctx context.Context, runner backend.Runner, step engine.Step) (engine.ObservedResource, error) {
	node := step.Node
	name := stringValue(node.Desired, "name")
	if name == "" {
		return engine.ObservedResource{}, fmt.Errorf("component script has no name")
	}
	addresses := append([]string(nil), step.TriggeredBy...)
	paths := make([]string, 0, len(addresses))
	triggerPaths, _ := node.Payload["trigger_paths"].(map[string]string)
	for _, address := range addresses {
		paths = append(paths, triggerPaths[address])
	}
	environment := []string{name, strings.Join(addresses, "\n"), strings.Join(paths, "\n")}
	commands, commandsOK := node.Payload["commands"].([][]string)
	content, contentOK := node.Payload["content"].(string)
	interpreter, interpreterOK := node.Payload["interpreter"].([]string)
	if len(commands) > 0 {
		for _, command := range commands {
			if len(command) == 0 {
				return engine.ObservedResource{}, fmt.Errorf("component script %q contains an empty command", name)
			}
			_, err := runner.Run(ctx, backend.Command{
				Name: "apply.component_script.command", Script: componentScriptExecuteCommandScript,
				Arguments: append(append([]string(nil), environment...), command...), RedactOutput: node.Sensitive,
			})
			if err != nil {
				return engine.ObservedResource{}, err
			}
		}
	} else {
		if !contentOK || !interpreterOK || len(interpreter) == 0 || commandsOK && len(commands) != 0 {
			return engine.ObservedResource{}, fmt.Errorf("component script %q has no executable commands or interpreter content", name)
		}
		_, err := runner.Run(ctx, backend.Command{
			Name: "apply.component_script.content", Script: componentScriptExecuteContentScript,
			Arguments: append(append([]string(nil), environment...), interpreter...),
			Stdin:     []byte(content), RedactStdin: true, RedactOutput: node.Sensitive,
		})
		if err != nil {
			return engine.ObservedResource{}, err
		}
	}
	outputs, err := scriptPayloadStringList(node, "outputs")
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if len(outputs) > 0 {
		marker := stringValue(node.Desired, "marker_path")
		if err := validateRemoteFilePath(marker); err != nil {
			return engine.ObservedResource{}, fmt.Errorf("component script output marker: %w", err)
		}
		scriptDigest := stringValue(node.Desired, "script_digest")
		_, err := runner.Run(ctx, backend.Command{
			Name: "apply.component_script.outputs", Script: componentScriptRecordOutputsScript,
			Arguments: append([]string{marker, scriptDigest}, outputs...), RedactOutput: node.Sensitive,
		})
		if err != nil {
			return engine.ObservedResource{}, err
		}
	}
	return inspectComponentScript(ctx, runner, node)
}

func scriptPayloadStringList(node graph.Node, name string) ([]string, error) {
	values, ok := node.Payload[name].([]string)
	if !ok {
		return nil, fmt.Errorf("component script payload has invalid %s", name)
	}
	return append([]string(nil), values...), nil
}

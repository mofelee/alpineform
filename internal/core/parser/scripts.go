package parser

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/mofelee/alpineform/internal/core/ir"
)

type EvaluatedScript struct {
	Name        string
	Description string
	Interpreter []string
	Outputs     []string
	Commands    [][]string
	Content     string
	Sensitive   bool
	Executable  bool
	Source      ir.SourceRef
}

func EvaluateScript(script Script, ctx EvalContext) (EvaluatedScript, error) {
	out := EvaluatedScript{Name: script.Name, Description: script.Description, Sensitive: script.Sensitive, Source: script.Source}
	commandsAttribute, hasCommands := script.Attributes["commands"]
	contentAttribute, hasContent := script.Attributes["content"]
	if hasCommands && hasContent {
		return EvaluatedScript{}, scriptEvaluationError(script.Source, "commands and content are mutually exclusive")
	}
	if !hasCommands && !hasContent {
		if _, exists := script.Attributes["interpreter"]; exists {
			return EvaluatedScript{}, scriptEvaluationError(script.Source, "interpreter requires content")
		}
		if _, exists := script.Attributes["outputs"]; exists {
			return EvaluatedScript{}, scriptEvaluationError(script.Source, "outputs require commands or content")
		}
		return out, nil
	}
	out.Executable = true
	if hasCommands {
		value, err := EvaluateExpression(commandsAttribute.Expression, ctx, commandsAttribute.Source)
		if err != nil {
			return EvaluatedScript{}, scriptEvaluationError(commandsAttribute.Source, "evaluate commands: %v", err)
		}
		if value.ContainsEphemeral() {
			return EvaluatedScript{}, scriptEvaluationError(commandsAttribute.Source, "commands must not use ephemeral values")
		}
		out.Sensitive = out.Sensitive || value.ContainsSensitive()
		commands, err := scriptCommandMatrix(value)
		if err != nil {
			return EvaluatedScript{}, scriptEvaluationError(commandsAttribute.Source, "%v", err)
		}
		out.Commands = commands
	}
	if hasContent {
		value, err := EvaluateExpression(contentAttribute.Expression, ctx, contentAttribute.Source)
		if err != nil {
			return EvaluatedScript{}, scriptEvaluationError(contentAttribute.Source, "evaluate content: %v", err)
		}
		if value.Kind != KindString {
			return EvaluatedScript{}, scriptEvaluationError(contentAttribute.Source, "content must evaluate to a string")
		}
		if value.ContainsEphemeral() {
			return EvaluatedScript{}, scriptEvaluationError(contentAttribute.Source, "content must not use ephemeral values")
		}
		out.Content = value.String
		out.Sensitive = out.Sensitive || value.ContainsSensitive()
		out.Interpreter = []string{"/bin/sh", "-eu"}
		if attribute, exists := script.Attributes["interpreter"]; exists {
			value, err := EvaluateExpression(attribute.Expression, ctx, attribute.Source)
			if err != nil {
				return EvaluatedScript{}, scriptEvaluationError(attribute.Source, "evaluate interpreter: %v", err)
			}
			out.Interpreter, err = scriptStringList(value, "interpreter", false, false)
			if err != nil {
				return EvaluatedScript{}, scriptEvaluationError(attribute.Source, "%v", err)
			}
		}
	}
	if attribute, exists := script.Attributes["outputs"]; exists {
		value, err := EvaluateExpression(attribute.Expression, ctx, attribute.Source)
		if err != nil {
			return EvaluatedScript{}, scriptEvaluationError(attribute.Source, "evaluate outputs: %v", err)
		}
		out.Outputs, err = scriptStringList(value, "outputs", true, false)
		if err != nil {
			return EvaluatedScript{}, scriptEvaluationError(attribute.Source, "%v", err)
		}
		seen := map[string]bool{}
		for _, path := range out.Outputs {
			if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || strings.ContainsAny(path, "\x00\r\n\t") {
				return EvaluatedScript{}, scriptEvaluationError(attribute.Source, "output path %q must be a clean absolute non-root path", path)
			}
			if seen[path] {
				return EvaluatedScript{}, scriptEvaluationError(attribute.Source, "duplicate output path %q", path)
			}
			seen[path] = true
		}
	}
	return out, nil
}

func scriptCommandMatrix(value Value) ([][]string, error) {
	if value.Kind != KindList || value.ContainsEphemeral() || len(value.List) == 0 {
		return nil, fmt.Errorf("commands must be a non-empty list of command arrays")
	}
	out := make([][]string, 0, len(value.List))
	for index, commandValue := range value.List {
		command, err := scriptStringList(commandValue, fmt.Sprintf("commands[%d]", index), false, true)
		if err != nil {
			return nil, err
		}
		out = append(out, command)
	}
	return out, nil
}

func scriptStringList(value Value, name string, allowEmpty, allowSensitive bool) ([]string, error) {
	if value.Kind != KindList || value.ContainsEphemeral() || (value.ContainsSensitive() && !allowSensitive) {
		return nil, fmt.Errorf("%s must be a non-protected string list", name)
	}
	if !allowEmpty && len(value.List) == 0 {
		return nil, fmt.Errorf("%s must contain at least one string", name)
	}
	out := make([]string, 0, len(value.List))
	for index, item := range value.List {
		if item.Kind != KindString || item.String == "" {
			return nil, fmt.Errorf("%s[%d] must be a non-empty string", name, index)
		}
		out = append(out, item.String)
	}
	return out, nil
}

func scriptEvaluationError(source ir.SourceRef, format string, args ...any) error {
	return fmt.Errorf("%s:%d:%s: %s", source.File, source.Line, source.Path, fmt.Sprintf(format, args...))
}

func parseScriptReference(file, path string, expression hcl.Expression) (ScriptReference, error) {
	traversal, diagnostics := hcl.AbsTraversalForExpr(expression)
	if diagnostics.HasErrors() {
		return ScriptReference{}, fmt.Errorf("%s:%d:%s: on_change must reference script.<name> or global.script.<name>", file, expression.Range().Start.Line, path)
	}
	reference := ScriptReference{Scope: ScriptReferenceAuto, Source: ir.SourceRef{File: file, Line: expression.Range().Start.Line, Path: path}}
	if len(traversal) == 2 {
		root, rootOK := traversal[0].(hcl.TraverseRoot)
		name, nameOK := traversal[1].(hcl.TraverseAttr)
		if rootOK && root.Name == "script" && nameOK {
			reference.Name = name.Name
			return reference, nil
		}
	}
	if len(traversal) == 3 {
		root, rootOK := traversal[0].(hcl.TraverseRoot)
		script, scriptOK := traversal[1].(hcl.TraverseAttr)
		name, nameOK := traversal[2].(hcl.TraverseAttr)
		if rootOK && root.Name == "global" && scriptOK && script.Name == "script" && nameOK {
			reference.Name = name.Name
			reference.Scope = ScriptReferenceGlobal
			return reference, nil
		}
	}
	return ScriptReference{}, fmt.Errorf("%s:%d:%s: on_change must reference script.<name> or global.script.<name>", file, expression.Range().Start.Line, path)
}

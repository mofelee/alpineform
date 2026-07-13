package merge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
)

func compileScriptMetadata(scripts map[string]parser.Script, parent string) map[string]ir.ScriptSpec {
	out := make(map[string]ir.ScriptSpec, len(scripts))
	for _, name := range sortedScriptNames(scripts) {
		script := scripts[name]
		declarationID := "script[" + strconvQuote(name) + "]"
		if parent != "" {
			declarationID = parent + ".script[" + strconvQuote(name) + "]"
		}
		out[name] = ir.ScriptSpec{Name: name, Description: script.Description, DeclarationID: declarationID, Sensitive: script.Sensitive, Source: script.Source}
	}
	return out
}

func compileEvaluatedScripts(scripts map[string]parser.Script, ctx parser.EvalContext, declarationID func(string) string) (map[string]ir.ScriptSpec, error) {
	out := make(map[string]ir.ScriptSpec, len(scripts))
	for _, name := range sortedScriptNames(scripts) {
		script := scripts[name]
		scriptContext := ctx
		scriptContext.ModuleDir = filepath.Dir(script.Source.File)
		evaluated, err := parser.EvaluateScript(script, scriptContext)
		if err != nil {
			return nil, err
		}
		spec := ir.ScriptSpec{
			Name: evaluated.Name, Description: evaluated.Description, DeclarationID: declarationID(name),
			Interpreter: append([]string(nil), evaluated.Interpreter...), Outputs: append([]string(nil), evaluated.Outputs...),
			Commands: cloneScriptCommands(evaluated.Commands), Content: evaluated.Content, Sensitive: evaluated.Sensitive,
			Executable: evaluated.Executable, Source: evaluated.Source,
		}
		if spec.Executable {
			spec.ScriptDigest = scriptSpecDigest(spec)
		}
		out[name] = spec
	}
	return out, nil
}

func resolveScriptReference(reference ir.ScriptReferenceSpec, local, root map[string]ir.ScriptSpec) (ir.ScriptReferenceSpec, error) {
	if reference.Scope != string(parser.ScriptReferenceGlobal) {
		if script, exists := local[reference.Name]; exists {
			if !script.Executable {
				return ir.ScriptReferenceSpec{}, fmt.Errorf("%s:%d:%s: referenced component script.%s has no commands or content", reference.Source.File, reference.Source.Line, reference.Source.Path, reference.Name)
			}
			reference.Scope = "component"
			reference.DeclarationID = script.DeclarationID
			return reference, nil
		}
	}
	if script, exists := root[reference.Name]; exists {
		if !script.Executable {
			return ir.ScriptReferenceSpec{}, fmt.Errorf("%s:%d:%s: referenced top-level script.%s has no commands or content", reference.Source.File, reference.Source.Line, reference.Source.Path, reference.Name)
		}
		reference.Scope = "root"
		reference.DeclarationID = script.DeclarationID
		return reference, nil
	}
	name := "script." + reference.Name
	if reference.Scope == string(parser.ScriptReferenceGlobal) {
		name = "global.script." + reference.Name
	}
	return ir.ScriptReferenceSpec{}, fmt.Errorf("%s:%d:%s: on_change references unknown %s", reference.Source.File, reference.Source.Line, reference.Source.Path, name)
}

func resolveFileScriptReferences(files []ir.ManagedFileSpec, local, root map[string]ir.ScriptSpec) ([]ir.ManagedFileSpec, error) {
	out := append([]ir.ManagedFileSpec(nil), files...)
	for index := range out {
		if out[index].OnChange == nil {
			continue
		}
		resolved, err := resolveScriptReference(*out[index].OnChange, local, root)
		if err != nil {
			return nil, err
		}
		out[index].OnChange = &resolved
	}
	return out, nil
}

func scriptSpecDigest(script ir.ScriptSpec) string {
	data, _ := json.Marshal(struct {
		Interpreter []string
		Outputs     []string
		Commands    [][]string
		Content     string
	}{script.Interpreter, script.Outputs, script.Commands, script.Content})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func cloneScriptCommands(commands [][]string) [][]string {
	out := make([][]string, 0, len(commands))
	for _, command := range commands {
		out = append(out, append([]string(nil), command...))
	}
	return out
}

func strconvQuote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

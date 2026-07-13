package parser

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/mofelee/alpineform/internal/core/ir"
)

type Config struct {
	Files                  []string
	Locals                 map[string]Value
	localDefinitions       map[string]localDefinition
	Variables              map[string]Variable
	VariableValues         map[string]Value
	ExplicitVariableValues map[string]bool
	Profiles               map[string]Profile
	Components             map[string]Component
	Hosts                  map[string]Host
	Scripts                map[string]Script
	Asserts                []Assert
}

type Variable struct {
	Name        string
	Type        string
	TypeExpr    string
	TypeSpec    ComponentInputTypeSpec
	Description string
	Default     *Value
	Sensitive   bool
	Nullable    bool
	Ephemeral   bool
	Deprecated  string
	Validations []VariableValidation
	Source      ir.SourceRef
}

type VariableValidation struct {
	Source          ir.SourceRef
	Condition       hcl.Expression
	ConditionSource ir.SourceRef
	Message         string
	MessageSource   ir.SourceRef
}

type ParseOptions struct {
	VariableValues        []ExternalVariableValue
	AllowMissingVariables bool
}

type ExternalVariableValue struct {
	Name          string
	Value         string
	ParsedValue   *Value
	Source        ir.SourceRef
	IgnoreUnknown bool
}

func ParseFiles(files []string) (*Config, error) {
	return ParseFilesWithOptions(files, ParseOptions{})
}

func ParseFilesWithOptions(files []string, opts ParseOptions) (*Config, error) {
	cfg := &Config{
		Files:                  append([]string(nil), files...),
		Locals:                 map[string]Value{},
		localDefinitions:       map[string]localDefinition{},
		Variables:              map[string]Variable{},
		VariableValues:         map[string]Value{},
		ExplicitVariableValues: map[string]bool{},
		Profiles:               map[string]Profile{},
		Components:             map[string]Component{},
		Hosts:                  map[string]Host{},
		Scripts:                map[string]Script{},
	}
	type parsedFile struct {
		name string
		body *hclsyntax.Body
	}
	parsed := make([]parsedFile, 0, len(files))
	parser := hclparse.NewParser()
	for _, file := range files {
		hclFile, diags := parser.ParseHCLFile(file)
		if diags.HasErrors() {
			return nil, fmt.Errorf("%s", diags.Error())
		}
		body, ok := hclFile.Body.(*hclsyntax.Body)
		if !ok {
			return nil, fmt.Errorf("%s: unsupported HCL body type %T", file, hclFile.Body)
		}
		if len(body.Attributes) != 0 {
			name := sortedAttrNames(body.Attributes)[0]
			attr := body.Attributes[name]
			return nil, fmt.Errorf("%s:%d: unsupported top-level attribute %q", file, attr.NameRange.Start.Line, name)
		}
		parsed = append(parsed, parsedFile{name: file, body: body})
	}

	for _, file := range parsed {
		if err := collectLocals(cfg, file.name, file.body); err != nil {
			return nil, err
		}
	}
	if err := evaluateStaticLocals(cfg); err != nil {
		return nil, err
	}
	for _, file := range parsed {
		if err := parseVariables(cfg, file.name, file.body); err != nil {
			return nil, err
		}
	}
	if err := resolveVariableValues(cfg, opts.VariableValues, opts.AllowMissingVariables); err != nil {
		return nil, err
	}
	if err := validateVariableValues(cfg); err != nil {
		return nil, err
	}
	if err := evaluateLocals(cfg); err != nil {
		return nil, err
	}
	for _, file := range parsed {
		if err := parseModelBlocks(cfg, file.name, file.body); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func collectLocals(cfg *Config, file string, body *hclsyntax.Body) error {
	for _, block := range body.Blocks {
		if block.Type != "locals" {
			continue
		}
		if len(block.Labels) != 0 {
			return fmt.Errorf("%s:%d: locals block must not have labels", file, block.TypeRange.Start.Line)
		}
		if len(block.Body.Blocks) != 0 {
			return fmt.Errorf("%s:%d: locals block does not support nested blocks", file, block.Body.Blocks[0].TypeRange.Start.Line)
		}
		for _, name := range sortedAttrNames(block.Body.Attributes) {
			attr := block.Body.Attributes[name]
			source := ir.SourceRef{File: file, Line: attr.NameRange.Start.Line, Path: "local." + name}
			if previous, exists := cfg.localDefinitions[name]; exists {
				return fmt.Errorf("%s:%d: duplicate local %q; first defined at %s:%d", file, source.Line, name, previous.Source.File, previous.Source.Line)
			}
			cfg.localDefinitions[name] = localDefinition{Name: name, Expr: attr.Expr, Source: source, ModuleDir: filepath.Dir(file)}
		}
	}
	return nil
}

func parseVariables(cfg *Config, file string, body *hclsyntax.Body) error {
	ctx := EvalContext{ModuleDir: filepath.Dir(file), Locals: cfg.Locals}
	for _, block := range body.Blocks {
		if block.Type != "variable" {
			continue
		}
		variable, err := parseVariable(file, block, ctx)
		if err != nil {
			return err
		}
		if previous, exists := cfg.Variables[variable.Name]; exists {
			return fmt.Errorf("%s:%d: duplicate variable %q; first defined at %s:%d", file, variable.Source.Line, variable.Name, previous.Source.File, previous.Source.Line)
		}
		cfg.Variables[variable.Name] = variable
	}
	return nil
}

func parseVariable(file string, block *hclsyntax.Block, ctx EvalContext) (Variable, error) {
	if len(block.Labels) != 1 {
		return Variable{}, fmt.Errorf("%s:%d: variable block requires exactly one label", file, block.TypeRange.Start.Line)
	}
	name := block.Labels[0]
	if !hclsyntax.ValidIdentifier(name) {
		return Variable{}, fmt.Errorf("%s:%d: variable label %q is not a valid identifier", file, block.TypeRange.Start.Line, name)
	}
	path := fmt.Sprintf("variable[%s]", strconv.Quote(name))
	for attrName, attr := range block.Body.Attributes {
		switch attrName {
		case "type", "default", "description", "nullable", "sensitive", "ephemeral", "deprecated":
		default:
			return Variable{}, fmt.Errorf("%s:%d: unsupported attribute %s.%s", file, attr.NameRange.Start.Line, path, attrName)
		}
	}
	for _, child := range block.Body.Blocks {
		if child.Type != "validation" {
			return Variable{}, fmt.Errorf("%s:%d: unsupported block %s.%s", file, child.TypeRange.Start.Line, path, child.Type)
		}
	}
	typeAttr, ok := block.Body.Attributes["type"]
	if !ok {
		return Variable{}, fmt.Errorf("%s:%d: %s.type is required", file, block.TypeRange.Start.Line, path)
	}
	typeCtx := ctx
	typeCtx.RestrictVariableDefaultReferences = true
	typeSpec, typeName, err := parseComponentInputType(typeAttr.Expr, typeCtx, path+".type")
	if err != nil {
		return Variable{}, fmt.Errorf("%s:%d: %s.type: %w", file, typeAttr.NameRange.Start.Line, path, err)
	}
	variable := Variable{Name: name, Type: typeName, TypeExpr: typeName, TypeSpec: typeSpec, Nullable: true, Source: ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path}}

	if attr, exists := block.Body.Attributes["description"]; exists {
		value, err := evalStringAttribute(file, path, "description", attr, ctx, false)
		if err != nil {
			return Variable{}, err
		}
		variable.Description = value
	}
	if attr, exists := block.Body.Attributes["deprecated"]; exists {
		value, err := evalStringAttribute(file, path, "deprecated", attr, ctx, true)
		if err != nil {
			return Variable{}, err
		}
		variable.Deprecated = value
	}
	if attr, exists := block.Body.Attributes["default"]; exists {
		source := ir.SourceRef{File: file, Line: attr.NameRange.Start.Line, Path: path + ".default"}
		if err := validateVariableDefaultReferences(attr.Expr, source); err != nil {
			return Variable{}, err
		}
		value, err := evalValue(attr.Expr, ctx, source)
		if err != nil {
			return Variable{}, fmt.Errorf("%s:%d:%s: variable default: %w", file, source.Line, source.Path, err)
		}
		variable.Default = &value
	}
	for _, item := range []struct {
		name string
		set  func(bool)
	}{
		{name: "nullable", set: func(value bool) { variable.Nullable = value }},
		{name: "sensitive", set: func(value bool) { variable.Sensitive = value }},
		{name: "ephemeral", set: func(value bool) { variable.Ephemeral = value }},
	} {
		if attr, exists := block.Body.Attributes[item.name]; exists {
			value, err := evalBoolAttribute(file, path, item.name, attr, ctx)
			if err != nil {
				return Variable{}, err
			}
			item.set(value)
		}
	}
	for i, child := range block.Body.Blocks {
		validation, err := parseVariableValidationBlock(file, fmt.Sprintf("%s.validation[%d]", path, i), child, ctx)
		if err != nil {
			return Variable{}, err
		}
		variable.Validations = append(variable.Validations, validation)
	}
	return variable, nil
}

func resolveVariableValues(cfg *Config, external []ExternalVariableValue, allowMissing bool) error {
	explicit := map[string]Value{}
	for i, item := range external {
		variable, exists := cfg.Variables[item.Name]
		if !exists {
			if item.IgnoreUnknown {
				continue
			}
			source := item.Source
			if source.Path == "" {
				source.Path = fmt.Sprintf("cli.var[%d]", i)
			}
			return fmt.Errorf("%s:%d:%s: unknown variable %q", source.File, source.Line, source.Path, item.Name)
		}
		value := Value{}
		if item.ParsedValue != nil {
			value = *item.ParsedValue
		} else {
			parsed, err := parseExternalVariableValue(variable, item)
			if err != nil {
				return err
			}
			value = parsed
		}
		normalized, err := NormalizeVariableValue(variable, value)
		if err != nil {
			return err
		}
		explicit[item.Name] = normalized
		cfg.ExplicitVariableValues[item.Name] = true
	}
	for _, name := range sortedVariableNames(cfg.Variables) {
		variable := cfg.Variables[name]
		if value, exists := explicit[name]; exists {
			cfg.VariableValues[name] = value
			continue
		}
		if variable.Default == nil {
			if allowMissing {
				continue
			}
			return fmt.Errorf("%s:%d:%s: variable %q is required", variable.Source.File, variable.Source.Line, variable.Source.Path, name)
		}
		normalized, err := NormalizeVariableValue(variable, *variable.Default)
		if err != nil {
			return err
		}
		cfg.VariableValues[name] = normalized
	}
	return nil
}

func parseVariableValidationBlock(file, path string, block *hclsyntax.Block, ctx EvalContext) (VariableValidation, error) {
	if len(block.Labels) != 0 {
		return VariableValidation{}, fmt.Errorf("%s:%d: %s block must not have labels", file, block.TypeRange.Start.Line, path)
	}
	if len(block.Body.Blocks) != 0 {
		return VariableValidation{}, fmt.Errorf("%s:%d: %s does not support nested blocks", file, block.Body.Blocks[0].TypeRange.Start.Line, path)
	}
	for name, attr := range block.Body.Attributes {
		if name != "condition" && name != "error_message" {
			return VariableValidation{}, fmt.Errorf("%s:%d: unsupported attribute %s.%s", file, attr.NameRange.Start.Line, path, name)
		}
	}
	condition, ok := block.Body.Attributes["condition"]
	if !ok {
		return VariableValidation{}, fmt.Errorf("%s:%d: %s.condition is required", file, block.TypeRange.Start.Line, path)
	}
	message, ok := block.Body.Attributes["error_message"]
	if !ok {
		return VariableValidation{}, fmt.Errorf("%s:%d: %s.error_message is required", file, block.TypeRange.Start.Line, path)
	}
	messageSource := ir.SourceRef{File: file, Line: message.NameRange.Start.Line, Path: path + ".error_message"}
	messageValue, err := evalValue(message.Expr, ctx, messageSource)
	if err != nil {
		return VariableValidation{}, err
	}
	if messageValue.Kind != KindString || messageValue.String == "" {
		return VariableValidation{}, fmt.Errorf("%s:%d:%s: error_message must be a non-empty string", file, messageSource.Line, messageSource.Path)
	}
	return VariableValidation{
		Source:          ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path},
		Condition:       condition.Expr,
		ConditionSource: ir.SourceRef{File: file, Line: condition.NameRange.Start.Line, Path: path + ".condition"},
		Message:         messageValue.String,
		MessageSource:   messageSource,
	}, nil
}

func evalStringAttribute(file, path, name string, attr *hclsyntax.Attribute, ctx EvalContext, nonEmpty bool) (string, error) {
	source := ir.SourceRef{File: file, Line: attr.NameRange.Start.Line, Path: path + "." + name}
	value, err := evalValue(attr.Expr, ctx, source)
	if err != nil {
		return "", err
	}
	if value.Kind != KindString {
		return "", fmt.Errorf("%s:%d:%s: %s must be a string", file, source.Line, source.Path, name)
	}
	if nonEmpty && value.String == "" {
		return "", fmt.Errorf("%s:%d:%s: %s must be a non-empty string", file, source.Line, source.Path, name)
	}
	return value.String, nil
}

func evalBoolAttribute(file, path, name string, attr *hclsyntax.Attribute, ctx EvalContext) (bool, error) {
	source := ir.SourceRef{File: file, Line: attr.NameRange.Start.Line, Path: path + "." + name}
	value, err := evalValue(attr.Expr, ctx, source)
	if err != nil {
		return false, err
	}
	if value.Kind != KindBool {
		return false, fmt.Errorf("%s:%d:%s: %s must be a boolean", file, source.Line, source.Path, name)
	}
	return value.Bool, nil
}

func validateVariableDefaultReferences(expr hcl.Expression, source ir.SourceRef) error {
	for _, traversal := range expr.Variables() {
		if len(traversal) == 0 {
			continue
		}
		root, ok := traversal[0].(hcl.TraverseRoot)
		if ok && root.Name != "local" {
			return fmt.Errorf("%s:%d:%s: variable default cannot reference %s", source.File, source.Line, source.Path, root.Name)
		}
	}
	return nil
}

func sortedVariableNames(values map[string]Variable) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedVariableValueNames(values map[string]Value) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedAttrNames(attributes hclsyntax.Attributes) []string {
	names := make([]string, 0, len(attributes))
	for name := range attributes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

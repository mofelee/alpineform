// Package merge resolves reusable declarations into provider-independent IR.
package merge

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
	"github.com/mofelee/alpineform/internal/product"
	"github.com/zclconf/go-cty/cty"
)

type resolvedProfile struct {
	Components map[string]parser.ComponentInstance
	Order      []string
	Asserts    []parser.Assert
}

type CompileOptions struct {
	HostFacts map[string]ir.HostFacts
}

// ConnectionTargets returns only the read-only transport identities needed for
// fact discovery. Full host assertions and component evaluation remain in the
// second compile phase after detected facts are available.
func ConnectionTargets(config *parser.Config) ([]ir.HostSpec, error) {
	if config == nil {
		return nil, fmt.Errorf("cannot build connection targets from a nil configuration")
	}
	targets := make([]ir.HostSpec, 0, len(config.Hosts))
	for _, hostName := range sortedHostNames(config.Hosts) {
		host := config.Hosts[hostName]
		targets = append(targets, ir.HostSpec{
			Name: host.Name,
			SSH: ir.SSHSpec{
				Host:         host.SSH.Host,
				Port:         host.SSH.Port,
				User:         host.SSH.User,
				IdentityFile: host.SSH.IdentityFile,
				Source:       host.SSH.Source,
			},
			State:  ir.StateSpec{Path: product.DefaultStatePath, LockPath: product.DefaultLockPath},
			Source: host.Source,
		})
	}
	return targets, nil
}

func Compile(config *parser.Config) (*ir.Program, error) {
	return CompileWithOptions(config, CompileOptions{})
}

func CompileWithOptions(config *parser.Config, options CompileOptions) (*ir.Program, error) {
	if err := validateComponentDefaults(config.Components); err != nil {
		return nil, err
	}
	baseContext, err := configEvalContext(config)
	if err != nil {
		return nil, err
	}
	for _, assertion := range config.Asserts {
		if err := evaluateAssert(assertion, baseContext, "configuration"); err != nil {
			return nil, err
		}
	}

	program := &ir.Program{
		Variables:  compileVariables(config.Variables),
		Components: compileComponentTemplates(config.Components),
		Scripts:    compileScripts(config.Scripts),
	}
	profiles, err := resolveProfiles(config)
	if err != nil {
		return nil, err
	}
	for hostName := range options.HostFacts {
		if _, exists := config.Hosts[hostName]; !exists {
			return nil, fmt.Errorf("detected facts were provided for unknown host %q", hostName)
		}
	}
	for _, hostName := range sortedHostNames(config.Hosts) {
		var facts *ir.HostFacts
		if detected, exists := options.HostFacts[hostName]; exists {
			facts = &detected
		}
		host, err := compileHost(config, profiles, config.Hosts[hostName], facts, baseContext)
		if err != nil {
			return nil, err
		}
		program.Hosts = append(program.Hosts, host)
	}
	return program, nil
}

func validateComponentDefaults(components map[string]parser.Component) error {
	for _, componentName := range sortedComponentNames(components) {
		component := components[componentName]
		for _, inputName := range sortedInputNames(component.Inputs) {
			input := component.Inputs[inputName]
			if err := validateInputValidationReferences(input); err != nil {
				return err
			}
			if input.Default == nil {
				continue
			}
			normalized, err := parser.NormalizeComponentInputValue(input, *input.Default)
			if err != nil {
				if input.Sensitive || input.Ephemeral {
					return fmt.Errorf("%s:%d:%s: invalid protected default for component.%s input %q", input.Source.File, input.Source.Line, input.Source.Path, componentName, inputName)
				}
				return err
			}
			context, err := inputEvalContext(parser.EvalContext{}, map[string]parser.Value{inputName: normalized})
			if err != nil {
				return err
			}
			if err := evaluateInputValidations(input, normalized, context, componentName); err != nil {
				return err
			}
		}
	}
	return nil
}

func compileVariables(variables map[string]parser.Variable) map[string]ir.VariableSpec {
	out := make(map[string]ir.VariableSpec, len(variables))
	for _, name := range sortedVariableNames(variables) {
		variable := variables[name]
		out[name] = ir.VariableSpec{
			Name:        name,
			Type:        variable.Type,
			Default:     protectedDefault(variable.Default, variable.Sensitive, variable.Ephemeral),
			Nullable:    variable.Nullable,
			Sensitive:   variable.Sensitive,
			Ephemeral:   variable.Ephemeral,
			Deprecated:  variable.Deprecated,
			Description: variable.Description,
			Source:      variable.Source,
		}
	}
	return out
}

func compileComponentTemplates(components map[string]parser.Component) map[string]ir.ComponentTemplateSpec {
	out := make(map[string]ir.ComponentTemplateSpec, len(components))
	for _, name := range sortedComponentNames(components) {
		component := components[name]
		inputs := make(map[string]ir.ComponentInputSpec, len(component.Inputs))
		for _, inputName := range sortedInputNames(component.Inputs) {
			input := component.Inputs[inputName]
			inputs[inputName] = ir.ComponentInputSpec{
				Name:        inputName,
				Type:        input.Type,
				Default:     protectedDefault(input.Default, input.Sensitive, input.Ephemeral),
				Nullable:    input.Nullable,
				Sensitive:   input.Sensitive,
				Ephemeral:   input.Ephemeral,
				Deprecated:  input.Deprecated,
				Description: input.Description,
				Source:      input.Source,
			}
		}
		out[name] = ir.ComponentTemplateSpec{Name: name, Description: component.Description, Inputs: inputs, Source: component.Source}
	}
	return out
}

func compileScripts(scripts map[string]parser.Script) map[string]ir.ScriptSpec {
	out := make(map[string]ir.ScriptSpec, len(scripts))
	for _, name := range sortedScriptNames(scripts) {
		script := scripts[name]
		out[name] = ir.ScriptSpec{Name: name, Description: script.Description, Source: script.Source}
	}
	return out
}

func resolveProfiles(config *parser.Config) (map[string]resolvedProfile, error) {
	resolved := map[string]resolvedProfile{}
	visiting := map[string]int{}
	var stack []string
	var resolve func(string) (resolvedProfile, error)
	resolve = func(name string) (resolvedProfile, error) {
		if profile, exists := resolved[name]; exists {
			return profile, nil
		}
		profile, exists := config.Profiles[name]
		if !exists {
			return resolvedProfile{}, fmt.Errorf("unknown profile.%s", name)
		}
		if start, active := visiting[name]; active {
			cycle := append(append([]string{}, stack[start:]...), name)
			for i := range cycle {
				cycle[i] = "profile." + cycle[i]
			}
			return resolvedProfile{}, fmt.Errorf("%s:%d:%s: profile import cycle: %s", profile.Source.File, profile.Source.Line, profile.Source.Path, strings.Join(cycle, " -> "))
		}
		visiting[name] = len(stack)
		stack = append(stack, name)
		result := resolvedProfile{Components: map[string]parser.ComponentInstance{}}
		for _, importedName := range profile.Imports {
			if _, exists := config.Profiles[importedName]; !exists {
				return resolvedProfile{}, fmt.Errorf("%s:%d:%s: unknown profile.%s", profile.Source.File, profile.Source.Line, profile.Source.Path, importedName)
			}
			imported, err := resolve(importedName)
			if err != nil {
				return resolvedProfile{}, err
			}
			overlayInstances(&result, imported)
		}
		for _, instance := range profile.Components {
			overlayInstance(&result, instance)
		}
		result.Asserts = append(result.Asserts, profile.Asserts...)
		stack = stack[:len(stack)-1]
		delete(visiting, name)
		resolved[name] = result
		return result, nil
	}
	for _, name := range sortedProfileNames(config.Profiles) {
		if _, err := resolve(name); err != nil {
			return nil, err
		}
	}
	return resolved, nil
}

func compileHost(config *parser.Config, profiles map[string]resolvedProfile, host parser.Host, facts *ir.HostFacts, baseContext parser.EvalContext) (ir.HostSpec, error) {
	if facts != nil {
		if err := validateDetectedFacts(host, *facts); err != nil {
			return ir.HostSpec{}, err
		}
	}
	resolved := resolvedProfile{Components: map[string]parser.ComponentInstance{}}
	for _, profileName := range host.Imports {
		profile, exists := profiles[profileName]
		if !exists {
			return ir.HostSpec{}, fmt.Errorf("%s:%d:%s: unknown profile.%s", host.Source.File, host.Source.Line, host.Source.Path, profileName)
		}
		overlayInstances(&resolved, profile)
	}
	for _, instance := range host.Components {
		overlayInstance(&resolved, instance)
	}
	if err := validateInstanceDependencies(host, resolved); err != nil {
		return ir.HostSpec{}, err
	}

	hostContext, err := hostEvalContext(baseContext, host, facts)
	if err != nil {
		return ir.HostSpec{}, err
	}
	for _, assertion := range resolved.Asserts {
		if err := evaluateHostAssert(assertion, host, facts, hostContext, "profile imported by host "+host.Name); err != nil {
			return ir.HostSpec{}, err
		}
	}
	for _, assertion := range host.Asserts {
		if err := evaluateHostAssert(assertion, host, facts, hostContext, "host "+host.Name); err != nil {
			return ir.HostSpec{}, err
		}
	}

	out := ir.HostSpec{
		Name: host.Name,
		SSH: ir.SSHSpec{
			Host:         host.SSH.Host,
			Port:         host.SSH.Port,
			User:         host.SSH.User,
			IdentityFile: host.SSH.IdentityFile,
			Source:       host.SSH.Source,
		},
		State:  ir.StateSpec{Path: product.DefaultStatePath, LockPath: product.DefaultLockPath},
		Source: host.Source,
	}
	if facts != nil {
		copied := *facts
		out.Facts = &copied
	}
	if host.Platform != nil {
		out.Platform = &ir.PlatformSpec{
			Architecture:       host.Platform.Architecture,
			Version:            host.Platform.Version,
			Branch:             host.Platform.Branch,
			Libc:               host.Platform.Libc,
			NativeArchitecture: host.Platform.NativeArchitecture,
			Source:             host.Platform.Source,
		}
	}
	out.Files, out.Directories, err = compileHostPathResources(host, facts, hostContext)
	if err != nil {
		return ir.HostSpec{}, err
	}
	for _, name := range resolved.Order {
		instance := resolved.Components[name]
		compiled, err := compileComponentInstance(config, host, facts, instance, hostContext)
		if err != nil {
			return ir.HostSpec{}, err
		}
		out.Components = append(out.Components, compiled)
	}
	return out, nil
}

func compileComponentInstance(config *parser.Config, host parser.Host, facts *ir.HostFacts, instance parser.ComponentInstance, hostContext parser.EvalContext) (ir.ComponentInstanceSpec, error) {
	template, exists := config.Components[instance.Template]
	if !exists {
		return ir.ComponentInstanceSpec{}, fmt.Errorf("%s:%d:%s: unknown component.%s", instance.Source.File, instance.Source.Line, instance.Source.Path, instance.Template)
	}
	for name, value := range instance.Inputs {
		if _, exists := template.Inputs[name]; !exists {
			return ir.ComponentInstanceSpec{}, fmt.Errorf("%s:%d:%s.inputs: unknown input %q for component.%s", value.Source.File, value.Source.Line, instance.Source.Path, name, template.Name)
		}
	}
	values := map[string]parser.Value{}
	var protected []string
	for _, name := range sortedInputNames(template.Inputs) {
		input := template.Inputs[name]
		value, exists := instance.Inputs[name]
		if !exists {
			if input.Default == nil {
				return ir.ComponentInstanceSpec{}, fmt.Errorf("%s:%d:%s: component.%s input %q is required", instance.Source.File, instance.Source.Line, instance.Source.Path, template.Name, name)
			}
			value = *input.Default
		}
		normalized, err := parser.NormalizeComponentInputValue(input, value)
		if err != nil {
			if input.Sensitive || input.Ephemeral {
				return ir.ComponentInstanceSpec{}, fmt.Errorf("%s:%d:%s: invalid protected input %q for component.%s", instance.Source.File, instance.Source.Line, instance.Source.Path, name, template.Name)
			}
			return ir.ComponentInstanceSpec{}, err
		}
		values[name] = normalized
		if normalized.ContainsSensitive() || normalized.ContainsEphemeral() {
			protected = append(protected, name)
		}
	}
	inputContext, err := inputEvalContext(hostContext, values)
	if err != nil {
		return ir.ComponentInstanceSpec{}, err
	}
	for _, name := range sortedInputNames(template.Inputs) {
		input := template.Inputs[name]
		if err := requireExpressionPlatform(input.Validations, host, facts); err != nil {
			return ir.ComponentInstanceSpec{}, err
		}
		if err := evaluateInputValidations(input, values[name], inputContext, template.Name); err != nil {
			return ir.ComponentInstanceSpec{}, err
		}
	}
	for _, assertion := range template.Asserts {
		if err := evaluateHostAssert(assertion, host, facts, inputContext, fmt.Sprintf("component.%s instance %s on host %s", template.Name, instance.Name, host.Name)); err != nil {
			return ir.ComponentInstanceSpec{}, err
		}
	}
	inputNames := make([]string, 0, len(values))
	for name := range values {
		inputNames = append(inputNames, name)
	}
	sort.Strings(inputNames)
	sort.Strings(protected)
	dependencies := append([]string(nil), instance.DependsOn...)
	sort.Strings(dependencies)
	return ir.ComponentInstanceSpec{
		Name:            instance.Name,
		Template:        instance.Template,
		InputNames:      inputNames,
		ProtectedInputs: protected,
		DependsOn:       dependencies,
		Lifecycle:       ir.LifecycleSpec{PreventDestroy: instance.Lifecycle.PreventDestroy, Source: instance.Lifecycle.Source},
		Source:          instance.Source,
	}, nil
}

func evaluateInputValidations(input parser.ComponentInput, value parser.Value, ctx parser.EvalContext, componentName string) error {
	for i, validation := range input.Validations {
		result, err := parser.EvaluateExpression(validation.Condition, ctx, validation.ConditionSource)
		if err != nil {
			if input.Sensitive || input.Ephemeral || value.ContainsSensitive() || value.ContainsEphemeral() {
				return fmt.Errorf("%s:%d:%s: protected component input validation failed to evaluate", validation.ConditionSource.File, validation.ConditionSource.Line, validation.ConditionSource.Path)
			}
			return fmt.Errorf("%s:%d:%s: component input validation condition: %w", validation.ConditionSource.File, validation.ConditionSource.Line, validation.ConditionSource.Path, err)
		}
		if result.Kind != parser.KindBool {
			return fmt.Errorf("%s:%d:%s: component input validation condition must evaluate to a boolean", validation.ConditionSource.File, validation.ConditionSource.Line, validation.ConditionSource.Path)
		}
		if !result.Bool {
			return fmt.Errorf("%s:%d:%s.validation[%d]: validation failed for component.%s input %q: %s", validation.Source.File, validation.Source.Line, input.Source.Path, i, componentName, input.Name, validation.Message)
		}
	}
	return nil
}

func validateInputValidationReferences(input parser.ComponentInput) error {
	for _, validation := range input.Validations {
		for _, traversal := range validation.Condition.Variables() {
			if len(traversal) < 2 {
				return fmt.Errorf("%s:%d:%s: component input validation can only read input.%s", validation.ConditionSource.File, validation.ConditionSource.Line, validation.ConditionSource.Path, input.Name)
			}
			root, rootOK := traversal[0].(hcl.TraverseRoot)
			attribute, attributeOK := traversal[1].(hcl.TraverseAttr)
			if !rootOK || root.Name != "input" || !attributeOK || attribute.Name != input.Name {
				return fmt.Errorf("%s:%d:%s: component input validation can only read input.%s", validation.ConditionSource.File, validation.ConditionSource.Line, validation.ConditionSource.Path, input.Name)
			}
		}
	}
	return nil
}

func evaluateAssert(assertion parser.Assert, ctx parser.EvalContext, scope string) error {
	ctx.ModuleDir = filepath.Dir(assertion.Source.File)
	result, err := parser.EvaluateExpression(assertion.Condition, ctx, assertion.ConditionSource)
	if err != nil {
		return fmt.Errorf("%s:%d:%s: %s assertion condition: %w", assertion.ConditionSource.File, assertion.ConditionSource.Line, assertion.ConditionSource.Path, scope, err)
	}
	if result.Kind != parser.KindBool {
		return fmt.Errorf("%s:%d:%s: %s assertion condition must evaluate to a boolean", assertion.ConditionSource.File, assertion.ConditionSource.Line, assertion.ConditionSource.Path, scope)
	}
	if !result.Bool {
		return fmt.Errorf("%s:%d:%s: %s assertion failed: %s", assertion.Source.File, assertion.Source.Line, assertion.Source.Path, scope, assertion.Message)
	}
	return nil
}

func evaluateHostAssert(assertion parser.Assert, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext, scope string) error {
	if err := requirePlatformForExpression(assertion.Condition, assertion.ConditionSource, host, facts); err != nil {
		return err
	}
	return evaluateAssert(assertion, ctx, scope)
}

func requireExpressionPlatform(validations []parser.ComponentInputValidation, host parser.Host, facts *ir.HostFacts) error {
	for _, validation := range validations {
		if err := requirePlatformForExpression(validation.Condition, validation.ConditionSource, host, facts); err != nil {
			return err
		}
	}
	return nil
}

func requirePlatformForExpression(expr hcl.Expression, source ir.SourceRef, host parser.Host, facts *ir.HostFacts) error {
	architectureRequired := expressionReferencesPlatform(expr, "architecture") || expressionReferencesPlatform(expr, "native_architecture")
	versionRequired := expressionReferencesPlatform(expr, "version") || expressionReferencesPlatform(expr, "branch")
	architectureAvailable := facts != nil && facts.Architecture != ""
	versionAvailable := facts != nil && facts.Version != ""
	if architectureRequired && !architectureAvailable && (host.Platform == nil || host.Platform.Architecture == "") {
		return fmt.Errorf("%s:%d:%s: expression requires host %q to declare platform.architecture for offline evaluation", source.File, source.Line, source.Path, host.Name)
	}
	if versionRequired && !versionAvailable && (host.Platform == nil || host.Platform.Version == "") {
		return fmt.Errorf("%s:%d:%s: expression requires host %q to declare platform.version for offline evaluation", source.File, source.Line, source.Path, host.Name)
	}
	return nil
}

func configEvalContext(config *parser.Config) (parser.EvalContext, error) {
	variables, err := valuesObject(config.VariableValues)
	if err != nil {
		return parser.EvalContext{}, err
	}
	return parser.EvalContext{Locals: config.Locals, Variables: map[string]cty.Value{"var": variables}}, nil
}

func hostEvalContext(base parser.EvalContext, host parser.Host, facts *ir.HostFacts) (parser.EvalContext, error) {
	ctx := base
	ctx.Variables = cloneCtyMap(base.Variables)
	platform := map[string]cty.Value{
		"architecture":        cty.StringVal(""),
		"version":             cty.StringVal(""),
		"branch":              cty.StringVal(""),
		"libc":                cty.StringVal("musl"),
		"native_architecture": cty.StringVal(""),
	}
	if host.Platform != nil {
		platform["architecture"] = cty.StringVal(host.Platform.Architecture)
		platform["version"] = cty.StringVal(host.Platform.Version)
		platform["branch"] = cty.StringVal(host.Platform.Branch)
		platform["libc"] = cty.StringVal(host.Platform.Libc)
		platform["native_architecture"] = cty.StringVal(host.Platform.NativeArchitecture)
	}
	if facts != nil {
		platform["architecture"] = cty.StringVal(facts.Architecture)
		platform["version"] = cty.StringVal(facts.Version)
		platform["branch"] = cty.StringVal(strings.TrimPrefix(facts.Branch, "v"))
		platform["libc"] = cty.StringVal(facts.Libc)
		platform["native_architecture"] = cty.StringVal(facts.NativeArchitecture)
	}
	self := cty.ObjectVal(map[string]cty.Value{"platform": cty.ObjectVal(platform)})
	ctx.Variables["self"] = self
	ctx.Variables["target"] = self
	return ctx, nil
}

func validateDetectedFacts(host parser.Host, facts ir.HostFacts) error {
	if facts.OSID != product.TargetOSID || facts.Branch != product.SupportedBranch || facts.Libc != product.TargetLibc {
		return fmt.Errorf("detected facts for host %q are not a supported Alpine %s %s target", host.Name, product.SupportedBranch, product.TargetLibc)
	}
	if facts.Version != "3.24" && !strings.HasPrefix(facts.Version, "3.24.") {
		return fmt.Errorf("detected facts for host %q contain unsupported exact version %q", host.Name, facts.Version)
	}
	switch facts.Architecture {
	case "amd64":
		if facts.NativeArchitecture != "x86_64" {
			return fmt.Errorf("detected facts for host %q mismatch amd64 with native architecture %q", host.Name, facts.NativeArchitecture)
		}
	case "arm64":
		if facts.NativeArchitecture != "aarch64" {
			return fmt.Errorf("detected facts for host %q mismatch arm64 with native architecture %q", host.Name, facts.NativeArchitecture)
		}
	default:
		return fmt.Errorf("detected facts for host %q contain unsupported architecture %q", host.Name, facts.Architecture)
	}
	if host.Platform == nil {
		return nil
	}
	if host.Platform.Architecture != "" && host.Platform.Architecture != facts.Architecture {
		return fmt.Errorf("%s:%d:%s.architecture: host %q declares %q, but detected architecture is %q", host.Platform.Source.File, host.Platform.Source.Line, host.Platform.Source.Path, host.Name, host.Platform.Architecture, facts.Architecture)
	}
	if host.Platform.Version != "" && host.Platform.Version != facts.Version {
		return fmt.Errorf("%s:%d:%s.version: host %q declares %q, but detected exact version is %q", host.Platform.Source.File, host.Platform.Source.Line, host.Platform.Source.Path, host.Name, host.Platform.Version, facts.Version)
	}
	return nil
}

func inputEvalContext(base parser.EvalContext, values map[string]parser.Value) (parser.EvalContext, error) {
	inputs, err := valuesObject(values)
	if err != nil {
		return parser.EvalContext{}, err
	}
	ctx := base
	ctx.Variables = cloneCtyMap(base.Variables)
	ctx.Variables["input"] = inputs
	return ctx, nil
}

func valuesObject(values map[string]parser.Value) (cty.Value, error) {
	if len(values) == 0 {
		return cty.EmptyObjectVal, nil
	}
	out := make(map[string]cty.Value, len(values))
	for name, value := range values {
		converted, err := value.ToCty()
		if err != nil {
			return cty.NilVal, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = converted
	}
	return cty.ObjectVal(out), nil
}

func validateInstanceDependencies(host parser.Host, profile resolvedProfile) error {
	for _, name := range profile.Order {
		instance := profile.Components[name]
		for _, dependency := range instance.DependsOn {
			if _, exists := profile.Components[dependency]; !exists {
				return fmt.Errorf("%s:%d:%s.depends_on: unknown component.%s on host %s", instance.Source.File, instance.Source.Line, instance.Source.Path, dependency, host.Name)
			}
		}
	}
	state := map[string]int{}
	var stack []string
	var visit func(string) error
	visit = func(name string) error {
		if state[name] == 2 {
			return nil
		}
		if state[name] == 1 {
			start := 0
			for i, item := range stack {
				if item == name {
					start = i
					break
				}
			}
			cycle := append(append([]string{}, stack[start:]...), name)
			for i := range cycle {
				cycle[i] = "component." + cycle[i]
			}
			instance := profile.Components[name]
			return fmt.Errorf("%s:%d:%s: component dependency cycle on host %s: %s", instance.Source.File, instance.Source.Line, instance.Source.Path, host.Name, strings.Join(cycle, " -> "))
		}
		state[name] = 1
		stack = append(stack, name)
		for _, dependency := range profile.Components[name].DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = 2
		return nil
	}
	for _, name := range profile.Order {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}

func overlayInstances(target *resolvedProfile, source resolvedProfile) {
	for _, name := range source.Order {
		overlayInstance(target, source.Components[name])
	}
	target.Asserts = append(target.Asserts, source.Asserts...)
}

func overlayInstance(target *resolvedProfile, instance parser.ComponentInstance) {
	if _, exists := target.Components[instance.Name]; !exists {
		target.Order = append(target.Order, instance.Name)
	}
	target.Components[instance.Name] = instance
}

func protectedDefault(value *parser.Value, sensitive, ephemeral bool) any {
	if value == nil {
		return nil
	}
	if sensitive {
		return "<sensitive>"
	}
	if ephemeral {
		return "<ephemeral>"
	}
	return value.UnmarkedInterface()
}

func cloneCtyMap(input map[string]cty.Value) map[string]cty.Value {
	out := make(map[string]cty.Value, len(input)+2)
	for name, value := range input {
		out[name] = value
	}
	return out
}

func sortedProfileNames(values map[string]parser.Profile) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedHostNames(values map[string]parser.Host) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedVariableNames(values map[string]parser.Variable) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedComponentNames(values map[string]parser.Component) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedInputNames(values map[string]parser.ComponentInput) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedScriptNames(values map[string]parser.Script) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func expressionReferencesPlatform(expr hcl.Expression, field string) bool {
	for _, traversal := range expr.Variables() {
		if len(traversal) < 3 {
			continue
		}
		root, rootOK := traversal[0].(hcl.TraverseRoot)
		platform, platformOK := traversal[1].(hcl.TraverseAttr)
		attribute, attributeOK := traversal[2].(hcl.TraverseAttr)
		if rootOK && (root.Name == "self" || root.Name == "target") && platformOK && platform.Name == "platform" && attributeOK && attribute.Name == field {
			return true
		}
	}
	return false
}

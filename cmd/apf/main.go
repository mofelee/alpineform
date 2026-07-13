package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	coregraph "github.com/mofelee/alpineform/internal/core/graph"
	coremerge "github.com/mofelee/alpineform/internal/core/merge"
	coreparser "github.com/mofelee/alpineform/internal/core/parser"
	coreplan "github.com/mofelee/alpineform/internal/core/plan"
	"github.com/mofelee/alpineform/internal/product"
	"github.com/mofelee/alpineform/internal/version"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", product.CLIName, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}
	switch args[0] {
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	case "version", "--version":
		fmt.Fprintf(stdout, "%s %s\n", product.CLIName, version.Current().Short())
		return nil
	case "validate":
		workingDir, err := os.Getwd()
		if err != nil {
			return err
		}
		return runValidate(args[1:], stdout, workingDir, os.Environ())
	case "fmt":
		workingDir, err := os.Getwd()
		if err != nil {
			return err
		}
		return runFmt(args[1:], stdout, workingDir)
	case "variable":
		workingDir, err := os.Getwd()
		if err != nil {
			return err
		}
		return runVariable(args[1:], stdout, workingDir, os.Environ())
	case "component":
		workingDir, err := os.Getwd()
		if err != nil {
			return err
		}
		return runComponent(args[1:], stdout, workingDir)
	case "plan":
		workingDir, err := os.Getwd()
		if err != nil {
			return err
		}
		return runPlan(args[1:], stdout, workingDir, os.Environ())
	case "apply", "check":
		return fmt.Errorf("%s is not available in the bootstrap build; Alpine resource management is not implemented", strings.Join(args, " "))
	default:
		return fmt.Errorf("unknown command %q; run %s help", args[0], product.CLIName)
	}
}

func runPlan(args []string, stdout io.Writer, workingDir string, environ []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var sources repeatedFlag
	var variableFiles repeatedFlag
	var variables repeatedFlag
	fs.Var(&sources, "f", "configuration file or directory; may be repeated")
	fs.Var(&variableFiles, "var-file", "explicit .apfvars or .apfvars.json file; may be repeated")
	fs.Var(&variables, "var", "variable value as name=value; may be repeated")
	offline := fs.Bool("offline", false, "compile without target access")
	format := fs.String("format", "text", "output format: text or json")
	htmlPath := fs.String("html", "", "write a standalone HTML plan")
	colorMode := fs.String("color", "auto", "color mode: auto, always, or never")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("plan arguments: %w", err)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("plan does not accept positional arguments")
	}
	if !*offline {
		return fmt.Errorf("online planning is not implemented yet; use apf plan --offline")
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("unsupported plan format %q; use text or json", *format)
	}
	color, err := resolveColor(*colorMode, stdout, environ)
	if err != nil {
		return err
	}

	resolvedSources := make([]string, len(sources))
	for i, source := range sources {
		resolvedSources[i] = resolvePath(workingDir, source)
	}
	files, err := coreparser.DiscoverConfigFiles(discoveryWorkingDir(workingDir), resolvedSources)
	if err != nil {
		return err
	}
	resolvedVariableFiles := make([]string, len(variableFiles))
	for i, path := range variableFiles {
		resolvedVariableFiles[i] = resolvePath(workingDir, path)
	}
	external, err := coreparser.CollectExternalVariableValues(files, environ, resolvedVariableFiles, variables)
	if err != nil {
		return err
	}
	config, err := coreparser.ParseFilesWithOptions(files, coreparser.ParseOptions{VariableValues: external})
	if err != nil {
		return err
	}
	program, err := coremerge.Compile(config)
	if err != nil {
		return err
	}
	resourceGraph, err := coregraph.Compile(program)
	if err != nil {
		return err
	}
	hosts := make([]string, 0, len(program.Hosts))
	for _, host := range program.Hosts {
		hosts = append(hosts, host.Name)
	}
	document := coreplan.New(resourceGraph, coreplan.Options{Files: files, Hosts: hosts})
	if *htmlPath != "" {
		path := resolvePath(workingDir, *htmlPath)
		if err := ensureOutputDoesNotReplaceInput(path, files); err != nil {
			return err
		}
		if err := writePlanHTML(path, document); err != nil {
			return err
		}
	}
	if *format == "json" {
		return coreplan.PrintJSON(stdout, document)
	}
	coreplan.PrintText(stdout, document, coreplan.TextOptions{Color: color})
	return nil
}

func resolveColor(mode string, output io.Writer, environ []string) (bool, error) {
	switch mode {
	case "always":
		return true, nil
	case "never":
		return false, nil
	case "auto":
	default:
		return false, fmt.Errorf("unsupported color mode %q; use auto, always, or never", mode)
	}
	for _, item := range environ {
		if item == "NO_COLOR" || strings.HasPrefix(item, "NO_COLOR=") || item == "TERM=dumb" {
			return false, nil
		}
	}
	file, ok := output.(*os.File)
	if !ok {
		return false, nil
	}
	info, err := file.Stat()
	if err != nil {
		return false, nil
	}
	return info.Mode()&os.ModeCharDevice != 0, nil
}

func ensureOutputDoesNotReplaceInput(output string, inputs []string) error {
	outputPath, err := filepath.Abs(filepath.Clean(output))
	if err != nil {
		return err
	}
	for _, input := range inputs {
		inputPath, err := filepath.Abs(filepath.Clean(input))
		if err != nil {
			return err
		}
		if outputPath == inputPath {
			return fmt.Errorf("HTML plan output %s would overwrite configuration input", output)
		}
	}
	return nil
}

func writePlanHTML(path string, document coreplan.Document) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".apf-plan-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
	}()
	if err := temporary.Chmod(0644); err != nil {
		return err
	}
	if err := coreplan.PrintHTML(temporary, document); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return err
	}
	return nil
}

func runFmt(args []string, stdout io.Writer, workingDir string) error {
	fs := flag.NewFlagSet("fmt", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var sources repeatedFlag
	fs.Var(&sources, "f", "configuration file or directory; may be repeated")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("fmt arguments: %w", err)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("fmt does not accept positional arguments")
	}
	resolvedSources := make([]string, len(sources))
	for i, source := range sources {
		resolvedSources[i] = resolvePath(workingDir, source)
	}
	files, err := coreparser.DiscoverConfigFiles(discoveryWorkingDir(workingDir), resolvedSources)
	if err != nil {
		return err
	}
	config, err := coreparser.ParseFilesWithOptions(files, coreparser.ParseOptions{AllowMissingVariables: true})
	if err != nil {
		return err
	}
	if _, err := coremerge.Compile(config); err != nil {
		return err
	}
	changed := 0
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		formatted := hclwrite.Format(data)
		if bytes.Equal(data, formatted) {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, formatted, info.Mode().Perm()); err != nil {
			return err
		}
		changed++
	}
	fmt.Fprintf(stdout, "formatted %d file(s)\n", changed)
	return nil
}

func runComponent(args []string, stdout io.Writer, workingDir string) error {
	if len(args) == 0 {
		return fmt.Errorf("component subcommand is required")
	}
	if args[0] != "inspect" {
		return fmt.Errorf("unknown component subcommand %q", args[0])
	}
	return runComponentInspect(args[1:], stdout, workingDir)
}

type componentInspectOutput struct {
	Name        string                  `json:"name"`
	Description string                  `json:"description,omitempty"`
	Inputs      []componentInspectInput `json:"inputs"`
}

type componentInspectInput struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Default     any    `json:"default,omitempty"`
	Nullable    bool   `json:"nullable"`
	Sensitive   bool   `json:"sensitive"`
	Ephemeral   bool   `json:"ephemeral"`
	Deprecated  string `json:"deprecated,omitempty"`
	Description string `json:"description,omitempty"`
}

func runComponentInspect(args []string, stdout io.Writer, workingDir string) error {
	fs := flag.NewFlagSet("component inspect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var sources repeatedFlag
	fs.Var(&sources, "f", "configuration file or directory; may be repeated")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("component inspect arguments: %w", err)
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("component inspect requires exactly one component name")
	}
	resolvedSources := make([]string, len(sources))
	for i, source := range sources {
		resolvedSources[i] = resolvePath(workingDir, source)
	}
	files, err := coreparser.DiscoverConfigFiles(discoveryWorkingDir(workingDir), resolvedSources)
	if err != nil {
		return err
	}
	config, err := coreparser.ParseFilesWithOptions(files, coreparser.ParseOptions{AllowMissingVariables: true})
	if err != nil {
		return err
	}
	component, exists := config.Components[fs.Arg(0)]
	if !exists {
		return fmt.Errorf("unknown component.%s", fs.Arg(0))
	}
	output := componentInspectOutput{Name: component.Name, Description: component.Description, Inputs: make([]componentInspectInput, 0, len(component.Inputs))}
	for _, name := range sortedComponentInputNames(component.Inputs) {
		input := component.Inputs[name]
		output.Inputs = append(output.Inputs, componentInspectInput{
			Name:        input.Name,
			Type:        input.Type,
			Default:     inspectComponentDefault(input),
			Nullable:    input.Nullable,
			Sensitive:   input.Sensitive,
			Ephemeral:   input.Ephemeral,
			Deprecated:  input.Deprecated,
			Description: input.Description,
		})
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(output)
}

func inspectComponentDefault(input coreparser.ComponentInput) any {
	if input.Default == nil {
		return nil
	}
	if input.Sensitive {
		return "<sensitive>"
	}
	if input.Ephemeral {
		return "<ephemeral>"
	}
	return json.RawMessage(input.Default.CanonicalString())
}

func runVariable(args []string, stdout io.Writer, workingDir string, environ []string) error {
	if len(args) == 0 {
		return fmt.Errorf("variable subcommand is required")
	}
	if args[0] != "inspect" {
		return fmt.Errorf("unknown variable subcommand %q", args[0])
	}
	return runVariableInspect(args[1:], stdout, workingDir, environ)
}

type variableInspectOutput struct {
	Variables []variableInspectVariable `json:"variables"`
}

type variableInspectVariable struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Default     any    `json:"default,omitempty"`
	Nullable    bool   `json:"nullable"`
	Sensitive   bool   `json:"sensitive"`
	Ephemeral   bool   `json:"ephemeral"`
	Deprecated  string `json:"deprecated,omitempty"`
	Description string `json:"description,omitempty"`
}

func runVariableInspect(args []string, stdout io.Writer, workingDir string, environ []string) error {
	fs := flag.NewFlagSet("variable inspect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var sources repeatedFlag
	var variableFiles repeatedFlag
	var variables repeatedFlag
	fs.Var(&sources, "f", "configuration file or directory; may be repeated")
	fs.Var(&variableFiles, "var-file", "explicit .apfvars or .apfvars.json file; may be repeated")
	fs.Var(&variables, "var", "variable value as name=value; may be repeated")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("variable inspect arguments: %w", err)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("variable inspect does not accept positional arguments")
	}
	resolvedSources := make([]string, len(sources))
	for i, source := range sources {
		resolvedSources[i] = resolvePath(workingDir, source)
	}
	files, err := coreparser.DiscoverConfigFiles(discoveryWorkingDir(workingDir), resolvedSources)
	if err != nil {
		return err
	}
	resolvedVariableFiles := make([]string, len(variableFiles))
	for i, path := range variableFiles {
		resolvedVariableFiles[i] = resolvePath(workingDir, path)
	}
	external, err := coreparser.CollectExternalVariableValues(files, environ, resolvedVariableFiles, variables)
	if err != nil {
		return err
	}
	config, err := coreparser.ParseFilesWithOptions(files, coreparser.ParseOptions{
		VariableValues:        external,
		AllowMissingVariables: true,
	})
	if err != nil {
		return err
	}
	output := variableInspectOutput{Variables: make([]variableInspectVariable, 0, len(config.Variables))}
	for _, name := range sortedVariableNames(config.Variables) {
		variable := config.Variables[name]
		output.Variables = append(output.Variables, variableInspectVariable{
			Name:        variable.Name,
			Type:        variable.Type,
			Default:     inspectDefault(variable),
			Nullable:    variable.Nullable,
			Sensitive:   variable.Sensitive,
			Ephemeral:   variable.Ephemeral,
			Deprecated:  variable.Deprecated,
			Description: variable.Description,
		})
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(output)
}

func inspectDefault(variable coreparser.Variable) any {
	if variable.Default == nil {
		return nil
	}
	if variable.Sensitive {
		return "<sensitive>"
	}
	if variable.Ephemeral {
		return "<ephemeral>"
	}
	return json.RawMessage(variable.Default.CanonicalString())
}

type repeatedFlag []string

func (values *repeatedFlag) String() string {
	return strings.Join(*values, ",")
}

func (values *repeatedFlag) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func runValidate(args []string, stdout io.Writer, workingDir string, environ []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var sources repeatedFlag
	var variableFiles repeatedFlag
	var variables repeatedFlag
	fs.Var(&sources, "f", "configuration file or directory; may be repeated")
	fs.Var(&variableFiles, "var-file", "explicit .apfvars or .apfvars.json file; may be repeated")
	fs.Var(&variables, "var", "variable value as name=value; may be repeated")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("validate arguments: %w", err)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("validate does not accept positional arguments")
	}

	resolvedSources := make([]string, len(sources))
	for i, source := range sources {
		resolvedSources[i] = resolvePath(workingDir, source)
	}
	files, err := coreparser.DiscoverConfigFiles(discoveryWorkingDir(workingDir), resolvedSources)
	if err != nil {
		return err
	}
	resolvedVariableFiles := make([]string, len(variableFiles))
	for i, path := range variableFiles {
		resolvedVariableFiles[i] = resolvePath(workingDir, path)
	}
	external, err := coreparser.CollectExternalVariableValues(files, environ, resolvedVariableFiles, variables)
	if err != nil {
		return err
	}
	config, err := coreparser.ParseFilesWithOptions(files, coreparser.ParseOptions{VariableValues: external})
	if err != nil {
		return err
	}
	if _, err := coremerge.Compile(config); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Configuration is valid: %d file(s), %d variable(s), %d local(s).\n", len(config.Files), len(config.Variables), len(config.Locals))
	for _, name := range sortedVariableNames(config.Variables) {
		if message := config.Variables[name].Deprecated; message != "" {
			fmt.Fprintf(stdout, "Warning: variable %q is deprecated: %s\n", name, message)
		}
	}
	return nil
}

func resolvePath(workingDir, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	if sameDirectory(workingDir, currentWorkingDirectory()) {
		return path
	}
	return filepath.Join(workingDir, path)
}

func discoveryWorkingDir(workingDir string) string {
	if sameDirectory(workingDir, currentWorkingDirectory()) {
		return "."
	}
	return workingDir
}

func currentWorkingDirectory() string {
	current, err := os.Getwd()
	if err != nil {
		return ""
	}
	return current
}

func sameDirectory(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	leftAbs, leftErr := filepath.Abs(filepath.Clean(left))
	rightAbs, rightErr := filepath.Abs(filepath.Clean(right))
	return leftErr == nil && rightErr == nil && leftAbs == rightAbs
}

func sortedVariableNames(variables map[string]coreparser.Variable) []string {
	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedComponentInputNames(inputs map[string]coreparser.ComponentInput) []string {
	names := make([]string, 0, len(inputs))
	for name := range inputs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `AlpineForm declaratively configures Alpine Linux hosts.

Usage:
  apf validate
  apf plan --offline [--format text|json] [--html path] [--color auto|always|never]
  apf apply [--auto-approve] [--debug]
  apf check
  apf fmt
  apf component inspect
  apf variable inspect
  apf version

Validate, offline plan, and variable inspect load top-level *.apf.hcl files
and support repeated -f, -var-file, and -var inputs. Fmt validates before writing.
This bootstrap build does not implement Alpine resource management yet.`)
}

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	coreparser "github.com/mofelee/alpineform/internal/core/parser"
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
	case "plan", "apply", "check", "fmt", "component", "variable":
		return fmt.Errorf("%s is not available in the bootstrap build; Alpine resource management is not implemented", strings.Join(args, " "))
	default:
		return fmt.Errorf("unknown command %q; run %s help", args[0], product.CLIName)
	}
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
	files, err := coreparser.DiscoverConfigFiles(workingDir, resolvedSources)
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
	return filepath.Join(workingDir, path)
}

func sortedVariableNames(variables map[string]coreparser.Variable) []string {
	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `AlpineForm declaratively configures Alpine Linux hosts.

Usage:
  apf validate
  apf plan [--offline] [--format text|json] [--html path]
  apf apply [--auto-approve] [--debug]
  apf check
  apf fmt
  apf component inspect
  apf variable inspect
  apf version

Validate loads top-level *.apf.hcl files and supports repeated -f, -var-file,
and -var inputs.
This bootstrap build does not implement Alpine resource management yet.`)
}

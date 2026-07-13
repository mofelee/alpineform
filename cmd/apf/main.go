package main

import (
	"fmt"
	"io"
	"os"
	"strings"

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
	case "validate", "plan", "apply", "check", "fmt", "component", "variable":
		return fmt.Errorf("%s is not available in the bootstrap build; Alpine resource management is not implemented", strings.Join(args, " "))
	default:
		return fmt.Errorf("unknown command %q; run %s help", args[0], product.CLIName)
	}
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

This bootstrap build does not implement Alpine resource management yet.
Configuration files use *.apf.hcl; run commands accept repeated -f sources once available.`)
}

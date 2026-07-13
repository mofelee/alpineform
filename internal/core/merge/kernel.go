package merge

import (
	"regexp"
	"sort"
	"strings"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
)

var (
	kernelModuleNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	sysctlKeyPattern        = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,255}$`)
)

func compileKernel(kernel parser.Kernel, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (*ir.KernelSpec, error) {
	spec := &ir.KernelSpec{Source: kernel.Source}
	for _, declaration := range kernel.Modules {
		if !kernelModuleNamePattern.MatchString(declaration.Label) {
			return nil, resourceError(declaration.Source, "kernel module name %q is invalid", declaration.Label)
		}
		ensure, err := resourceStringDefault(declaration, "ensure", "present", host, facts, ctx)
		if err != nil {
			return nil, err
		}
		if ensure != "present" {
			return nil, resourceAttributeError(declaration, "ensure", "kernel module automatic absence or unload is unsupported in v0.1; remove the declaration to forget it")
		}
		spec.Modules = append(spec.Modules, ir.KernelModuleSpec{
			Name:      declaration.Label,
			Lifecycle: ir.LifecycleSpec{PreventDestroy: declaration.Lifecycle.PreventDestroy, Source: declaration.Lifecycle.Source},
			Source:    declaration.Source,
		})
	}
	for _, declaration := range kernel.Sysctls {
		key := declaration.Label
		if !sysctlKeyPattern.MatchString(key) || strings.Contains(key, "..") || strings.HasSuffix(key, ".") {
			return nil, resourceError(declaration.Source, "sysctl key %q is invalid", key)
		}
		value, exists, err := resourceString(declaration, "value", host, facts, ctx)
		if err != nil {
			return nil, err
		}
		if !exists || value == "" || len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
			return nil, resourceAttributeError(declaration, "value", "must be a required non-empty string without NUL or line breaks")
		}
		applyRuntime, err := resourceBoolDefault(declaration, "apply_runtime", true, host, facts, ctx)
		if err != nil {
			return nil, err
		}
		spec.Sysctls = append(spec.Sysctls, ir.SysctlSpec{
			Key: key, Value: value, ApplyRuntime: applyRuntime,
			Lifecycle: ir.LifecycleSpec{PreventDestroy: declaration.Lifecycle.PreventDestroy, Source: declaration.Lifecycle.Source},
			Source:    declaration.Source,
		})
	}
	sort.Slice(spec.Modules, func(i, j int) bool { return spec.Modules[i].Name < spec.Modules[j].Name })
	sort.Slice(spec.Sysctls, func(i, j int) bool { return spec.Sysctls[i].Key < spec.Sysctls[j].Key })
	return spec, nil
}

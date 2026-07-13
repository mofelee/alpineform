package parser

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/mofelee/alpineform/internal/core/ir"
)

const (
	ResourceKernelModule = "kernel_module"
	ResourceSysctl       = "sysctl"
)

type Kernel struct {
	Modules []ResourceDeclaration
	Sysctls []ResourceDeclaration
	Source  ir.SourceRef
}

var kernelResourceSchemas = map[string]resourceCollectionSchema{
	"module": {
		block:      "module",
		resource:   ResourceKernelModule,
		attributes: attributeSet("ensure"),
	},
	"sysctl": {
		block:      "sysctl",
		resource:   ResourceSysctl,
		attributes: attributeSet("value", "apply_runtime"),
	},
}

func parseKernel(file, path string, block *hclsyntax.Block, ctx EvalContext) (Kernel, error) {
	if len(block.Labels) != 0 || len(block.Body.Attributes) != 0 {
		return Kernel{}, fmt.Errorf("%s:%d:%s must be an unlabeled block containing only module and sysctl blocks", file, block.TypeRange.Start.Line, path)
	}
	kernel := Kernel{Source: ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path}}
	seen := map[string]ir.SourceRef{}
	for _, child := range block.Body.Blocks {
		schema, exists := kernelResourceSchemas[child.Type]
		if !exists {
			return Kernel{}, fmt.Errorf("%s:%d: unsupported block %s.%s", file, child.TypeRange.Start.Line, path, child.Type)
		}
		declaration, err := parseResourceDeclaration(file, path, child, schema, ctx)
		if err != nil {
			return Kernel{}, err
		}
		key := declaration.Kind + "\x00" + declaration.Label
		if previous, exists := seen[key]; exists {
			return Kernel{}, fmt.Errorf("%s:%d:%s: duplicate %s label %q; first defined at %s:%d", file, declaration.Source.Line, declaration.Source.Path, child.Type, declaration.Label, previous.File, previous.Line)
		}
		seen[key] = declaration.Source
		if declaration.Kind == ResourceKernelModule {
			kernel.Modules = append(kernel.Modules, declaration)
		} else {
			kernel.Sysctls = append(kernel.Sysctls, declaration)
		}
	}
	return kernel, nil
}

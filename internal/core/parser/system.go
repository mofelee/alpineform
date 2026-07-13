package parser

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/mofelee/alpineform/internal/core/ir"
)

func parseSystem(file, path string, block *hclsyntax.Block) (System, error) {
	if len(block.Labels) != 0 || len(block.Body.Blocks) != 0 {
		return System{}, fmt.Errorf("%s:%d:%s must be an unlabeled attribute-only block", file, block.TypeRange.Start.Line, path)
	}
	system := System{Attributes: map[string]ResourceAttribute{}, Source: ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path}}
	for name, attribute := range block.Body.Attributes {
		source := ir.SourceRef{File: file, Line: attribute.NameRange.Start.Line, Path: path + "." + name}
		switch name {
		case "hostname", "timezone":
			system.Attributes[name] = ResourceAttribute{Expression: attribute.Expr, Source: source}
		case "locale":
			return System{}, fmt.Errorf("%s:%d:%s: system.locale is unsupported on musl Alpine", file, source.Line, source.Path)
		default:
			return System{}, fmt.Errorf("%s:%d: unsupported attribute %s", file, source.Line, source.Path)
		}
	}
	return system, nil
}

package parser

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/mofelee/alpineform/internal/core/ir"
)

const ResourceOpenRCService = "openrc_service"

var openRCServiceSchema = resourceCollectionSchema{
	block:    "openrc",
	resource: "service",
	attributes: attributeSet(
		"command", "command_args", "command_user", "directory", "command_background", "pidfile", "description",
		"need", "use", "want", "after", "before", "conf",
	),
}

func parseOpenRC(file, path string, block *hclsyntax.Block, ctx EvalContext) (OpenRC, error) {
	if len(block.Labels) != 0 || len(block.Body.Attributes) != 0 {
		return OpenRC{}, fmt.Errorf("%s:%d:%s must be an unlabeled block containing only service blocks", file, block.TypeRange.Start.Line, path)
	}
	openrc := OpenRC{Source: ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path}}
	seen := map[string]ir.SourceRef{}
	for _, child := range block.Body.Blocks {
		if child.Type != "service" {
			return OpenRC{}, fmt.Errorf("%s:%d:%s supports only service blocks, got %s", file, child.TypeRange.Start.Line, path, child.Type)
		}
		declaration, err := parseResourceDeclaration(file, path, child, openRCServiceSchema, ctx)
		if err != nil {
			return OpenRC{}, err
		}
		declaration.Kind = ResourceOpenRCService
		if previous, exists := seen[declaration.Label]; exists {
			return OpenRC{}, fmt.Errorf("%s:%d:%s: duplicate service label %q; first defined at %s:%d", file, declaration.Source.Line, declaration.Source.Path, declaration.Label, previous.File, previous.Line)
		}
		seen[declaration.Label] = declaration.Source
		openrc.Services = append(openrc.Services, declaration)
	}
	return openrc, nil
}

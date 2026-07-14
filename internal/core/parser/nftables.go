package parser

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/mofelee/alpineform/internal/core/ir"
)

const ResourceNftablesTable = "nftables_table"

type Nftables struct {
	Tables []ResourceDeclaration
	Source ir.SourceRef
}

var nftablesTableSchema = resourceCollectionSchema{
	block:    "table",
	resource: ResourceNftablesTable,
	attributes: attributeSet(
		"family", "content", "content_version", "ensure", "adopt_existing", "on_remove", "rollback_timeout",
	),
}

func parseNftables(file, path string, block *hclsyntax.Block, ctx EvalContext) (Nftables, error) {
	if len(block.Labels) != 0 || len(block.Body.Attributes) != 0 {
		return Nftables{}, fmt.Errorf("%s:%d:%s must be an unlabeled block containing only table blocks", file, block.TypeRange.Start.Line, path)
	}
	nftables := Nftables{Source: ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path}}
	for _, child := range block.Body.Blocks {
		if child.Type != "table" {
			return Nftables{}, fmt.Errorf("%s:%d: unsupported block %s.%s", file, child.TypeRange.Start.Line, path, child.Type)
		}
		declaration, err := parseResourceDeclaration(file, path, child, nftablesTableSchema, ctx)
		if err != nil {
			return Nftables{}, err
		}
		// Family participates in identity and is evaluated by the merge layer, so
		// equal labels remain legal here for different nftables families.
		nftables.Tables = append(nftables.Tables, declaration)
	}
	return nftables, nil
}

package parser

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/mofelee/alpineform/internal/core/ir"
)

const (
	ResourceAPKRepository = "apk_repository"
	ResourceAPKKey        = "apk_key"
)

var apkResourceSchemas = map[string]resourceCollectionSchema{
	"repository": {
		block:      "apk",
		resource:   "repository",
		attributes: attributeSet("url", "branch", "component", "tag", "ensure"),
	},
	"key": {
		block:      "apk",
		resource:   "key",
		attributes: attributeSet("source", "sha256", "ensure"),
	},
}

func parseAPK(file, path string, block *hclsyntax.Block, ctx EvalContext) (APK, error) {
	if len(block.Labels) != 0 {
		return APK{}, fmt.Errorf("%s:%d: %s block must not have labels", file, block.TypeRange.Start.Line, path)
	}
	if err := rejectAttributesExcept(file, path, block.Body.Attributes, "ownership"); err != nil {
		return APK{}, err
	}
	apk := APK{Ownership: "managed", Source: ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path}}
	if attribute, exists := block.Body.Attributes["ownership"]; exists {
		ownership, err := evalStringAttribute(file, path, "ownership", attribute, ctx, true)
		if err != nil {
			return APK{}, err
		}
		if ownership != "managed" && ownership != "authoritative" {
			return APK{}, fmt.Errorf("%s:%d:%s.ownership must be \"managed\" or \"authoritative\"", file, attribute.NameRange.Start.Line, path)
		}
		apk.Ownership = ownership
	}
	seen := map[string]ir.SourceRef{}
	for _, child := range block.Body.Blocks {
		schema, exists := apkResourceSchemas[child.Type]
		if !exists {
			return APK{}, fmt.Errorf("%s:%d: unsupported block %s.%s", file, child.TypeRange.Start.Line, path, child.Type)
		}
		declaration, err := parseResourceDeclaration(file, path, child, schema, ctx)
		if err != nil {
			return APK{}, err
		}
		switch child.Type {
		case "repository":
			declaration.Kind = ResourceAPKRepository
		case "key":
			declaration.Kind = ResourceAPKKey
		}
		key := declaration.Kind + "\x00" + declaration.Label
		if previous, exists := seen[key]; exists {
			return APK{}, fmt.Errorf("%s:%d:%s: duplicate %s label %q; first defined at %s:%d", file, declaration.Source.Line, declaration.Source.Path, child.Type, declaration.Label, previous.File, previous.Line)
		}
		seen[key] = declaration.Source
		switch declaration.Kind {
		case ResourceAPKRepository:
			apk.Repositories = append(apk.Repositories, declaration)
		case ResourceAPKKey:
			apk.Keys = append(apk.Keys, declaration)
		}
	}
	return apk, nil
}

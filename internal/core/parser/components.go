package parser

import (
	"fmt"
	"strconv"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/mofelee/alpineform/internal/core/ir"
)

func parseComponentArtifactSource(file, componentPath string, block *hclsyntax.Block, ctx EvalContext) (ComponentArtifactSource, error) {
	if len(block.Labels) > 1 {
		return ComponentArtifactSource{}, fmt.Errorf("%s:%d:%s.source accepts at most one architecture label", file, block.TypeRange.Start.Line, componentPath)
	}
	architecture := ""
	path := componentPath + ".source"
	if len(block.Labels) == 1 {
		architecture = block.Labels[0]
		path += "[" + strconv.Quote(architecture) + "]"
	}
	if len(block.Body.Blocks) != 0 {
		return ComponentArtifactSource{}, fmt.Errorf("%s:%d:%s does not support nested blocks", file, block.Body.Blocks[0].TypeRange.Start.Line, path)
	}
	if err := rejectAttributesExcept(file, path, block.Body.Attributes, "url", "sha256"); err != nil {
		return ComponentArtifactSource{}, err
	}
	source := ComponentArtifactSource{Architecture: architecture, Source: ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path}}
	var err error
	if attr, exists := block.Body.Attributes["url"]; exists {
		source.URL, err = evalStringAttribute(file, path, "url", attr, ctx, true)
		if err != nil {
			return ComponentArtifactSource{}, err
		}
	}
	if attr, exists := block.Body.Attributes["sha256"]; exists {
		source.SHA256, err = evalStringAttribute(file, path, "sha256", attr, ctx, true)
		if err != nil {
			return ComponentArtifactSource{}, err
		}
	}
	return source, nil
}

func parseComponentArtifactExtract(file, componentPath string, block *hclsyntax.Block, ctx EvalContext) (ComponentArtifactExtract, error) {
	path := componentPath + ".extract"
	if len(block.Labels) != 0 || len(block.Body.Blocks) != 0 {
		return ComponentArtifactExtract{}, fmt.Errorf("%s:%d:%s must be an unlabeled attribute-only block", file, block.TypeRange.Start.Line, path)
	}
	if err := rejectAttributesExcept(file, path, block.Body.Attributes, "format", "strip_components", "include"); err != nil {
		return ComponentArtifactExtract{}, err
	}
	extract := ComponentArtifactExtract{Source: ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path}}
	var err error
	if attr, exists := block.Body.Attributes["format"]; exists {
		extract.Format, err = evalStringAttribute(file, path, "format", attr, ctx, true)
		if err != nil {
			return ComponentArtifactExtract{}, err
		}
	}
	if attr, exists := block.Body.Attributes["strip_components"]; exists {
		value, valueErr := evalValue(attr.Expr, ctx, ir.SourceRef{File: file, Line: attr.NameRange.Start.Line, Path: path + ".strip_components"})
		if valueErr != nil {
			return ComponentArtifactExtract{}, valueErr
		}
		if value.Kind != KindNumber {
			return ComponentArtifactExtract{}, fmt.Errorf("%s:%d:%s.strip_components must be an integer", file, attr.NameRange.Start.Line, path)
		}
		extract.StripComponents, err = strconv.Atoi(value.Number)
		if err != nil {
			return ComponentArtifactExtract{}, fmt.Errorf("%s:%d:%s.strip_components must be an integer", file, attr.NameRange.Start.Line, path)
		}
	}
	if attr, exists := block.Body.Attributes["include"]; exists {
		extract.Include, err = evalStringAttribute(file, path, "include", attr, ctx, true)
		if err != nil {
			return ComponentArtifactExtract{}, err
		}
	}
	return extract, nil
}

func parseComponentArtifactInstall(file, componentPath string, block *hclsyntax.Block, ctx EvalContext) (ComponentArtifactInstall, error) {
	path := componentPath + ".install"
	if len(block.Labels) != 0 || len(block.Body.Blocks) != 0 {
		return ComponentArtifactInstall{}, fmt.Errorf("%s:%d:%s must be an unlabeled attribute-only block", file, block.TypeRange.Start.Line, path)
	}
	if err := rejectAttributesExcept(file, path, block.Body.Attributes, "path", "owner", "group", "mode"); err != nil {
		return ComponentArtifactInstall{}, err
	}
	install := ComponentArtifactInstall{Source: ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path}}
	var err error
	for name, target := range map[string]*string{"path": &install.Path, "owner": &install.Owner, "group": &install.Group, "mode": &install.Mode} {
		if attr, exists := block.Body.Attributes[name]; exists {
			*target, err = evalStringAttribute(file, path, name, attr, ctx, true)
			if err != nil {
				return ComponentArtifactInstall{}, err
			}
		}
	}
	return install, nil
}

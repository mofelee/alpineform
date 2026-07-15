package parser

import (
	"fmt"
	"strconv"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/mofelee/alpineform/internal/core/ir"
)

var componentBuildAttributes = attributeSet(
	"working_directory", "environment", "environment_version", "output",
	"output_sha256", "max_output_bytes", "dependencies", "network", "on_remove",
)

var componentBuildInputAttributes = attributeSet(
	"source", "url", "content", "content_version", "sha256", "destination",
)

var componentBuildCommandAttributes = attributeSet("argv", "stdin", "stdin_version")
var componentBuildInputExtractAttributes = attributeSet("format", "strip_components")

func parseComponentBuild(file, componentPath string, block *hclsyntax.Block) (ComponentBuild, error) {
	path := componentPath + ".build"
	if len(block.Labels) != 0 {
		return ComponentBuild{}, fmt.Errorf("%s:%d:%s must be an unlabeled block", file, block.TypeRange.Start.Line, path)
	}
	attributes, err := buildAttributes(file, path, block.Body.Attributes, componentBuildAttributes)
	if err != nil {
		return ComponentBuild{}, err
	}
	build := ComponentBuild{Attributes: attributes, Source: ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path}}
	inputNames := map[string]ir.SourceRef{}
	for _, child := range block.Body.Blocks {
		switch child.Type {
		case "input":
			input, err := parseComponentBuildInput(file, path, child)
			if err != nil {
				return ComponentBuild{}, err
			}
			if previous, exists := inputNames[input.Name]; exists {
				return ComponentBuild{}, fmt.Errorf("%s:%d:%s: duplicate build input %q; first defined at %s:%d", file, input.Source.Line, input.Source.Path, input.Name, previous.File, previous.Line)
			}
			inputNames[input.Name] = input.Source
			build.Inputs = append(build.Inputs, input)
		case "command":
			command, err := parseComponentBuildCommand(file, path, len(build.Commands), child)
			if err != nil {
				return ComponentBuild{}, err
			}
			build.Commands = append(build.Commands, command)
		default:
			return ComponentBuild{}, fmt.Errorf("%s:%d: unsupported block %s.%s", file, child.TypeRange.Start.Line, path, child.Type)
		}
	}
	return build, nil
}

func parseComponentBuildInput(file, buildPath string, block *hclsyntax.Block) (ComponentBuildInput, error) {
	if len(block.Labels) != 1 || block.Labels[0] == "" {
		return ComponentBuildInput{}, fmt.Errorf("%s:%d:%s.input requires exactly one non-empty label", file, block.TypeRange.Start.Line, buildPath)
	}
	name := block.Labels[0]
	path := buildPath + ".input[" + strconv.Quote(name) + "]"
	attributes, err := buildAttributes(file, path, block.Body.Attributes, componentBuildInputAttributes)
	if err != nil {
		return ComponentBuildInput{}, err
	}
	input := ComponentBuildInput{Name: name, Attributes: attributes, Source: ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path}}
	for _, child := range block.Body.Blocks {
		if child.Type != "extract" {
			return ComponentBuildInput{}, fmt.Errorf("%s:%d: unsupported block %s.%s", file, child.TypeRange.Start.Line, path, child.Type)
		}
		if input.Extract != nil {
			return ComponentBuildInput{}, fmt.Errorf("%s:%d: duplicate %s.extract block", file, child.TypeRange.Start.Line, path)
		}
		if len(child.Labels) != 0 || len(child.Body.Blocks) != 0 {
			return ComponentBuildInput{}, fmt.Errorf("%s:%d:%s.extract must be an unlabeled attribute-only block", file, child.TypeRange.Start.Line, path)
		}
		extractPath := path + ".extract"
		extractAttributes, err := buildAttributes(file, extractPath, child.Body.Attributes, componentBuildInputExtractAttributes)
		if err != nil {
			return ComponentBuildInput{}, err
		}
		input.Extract = &ComponentBuildInputExtract{Attributes: extractAttributes, Source: ir.SourceRef{File: file, Line: child.TypeRange.Start.Line, Path: extractPath}}
	}
	return input, nil
}

func parseComponentBuildCommand(file, buildPath string, index int, block *hclsyntax.Block) (ComponentBuildCommand, error) {
	path := fmt.Sprintf("%s.command[%d]", buildPath, index)
	if len(block.Labels) != 0 || len(block.Body.Blocks) != 0 {
		return ComponentBuildCommand{}, fmt.Errorf("%s:%d:%s must be an unlabeled attribute-only block", file, block.TypeRange.Start.Line, path)
	}
	attributes, err := buildAttributes(file, path, block.Body.Attributes, componentBuildCommandAttributes)
	if err != nil {
		return ComponentBuildCommand{}, err
	}
	return ComponentBuildCommand{Attributes: attributes, Source: ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path}}, nil
}

func buildAttributes(file, path string, attributes hclsyntax.Attributes, allowed map[string]struct{}) (map[string]ResourceAttribute, error) {
	out := make(map[string]ResourceAttribute, len(attributes))
	for name, attribute := range attributes {
		if _, exists := allowed[name]; !exists {
			return nil, fmt.Errorf("%s:%d: unsupported attribute %s.%s", file, attribute.NameRange.Start.Line, path, name)
		}
		out[name] = ResourceAttribute{Expression: attribute.Expr, Source: ir.SourceRef{File: file, Line: attribute.NameRange.Start.Line, Path: path + "." + name}}
	}
	return out, nil
}

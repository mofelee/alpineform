package parser

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/mofelee/alpineform/internal/core/ir"
)

var dockerProjectSchema = resourceCollectionSchema{
	block:    "docker",
	resource: "project",
	attributes: attributeSet(
		"directory", "compose", "compose_version", "env", "env_version",
		"state", "sensitive", "on_remove",
	),
}

func parseDocker(file, path string, block *hclsyntax.Block, ctx EvalContext) (Docker, error) {
	if len(block.Labels) != 0 {
		return Docker{}, fmt.Errorf("%s:%d:%s must be an unlabeled block", file, block.TypeRange.Start.Line, path)
	}
	if err := rejectAttributesExcept(file, path, block.Body.Attributes,
		"ensure", "enable", "package_source", "package_repository", "members",
		"daemon_config", "daemon_config_version", "daemon_config_sensitive",
	); err != nil {
		return Docker{}, err
	}
	docker := Docker{
		Attributes: map[string]ResourceAttribute{},
		Source:     ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path},
	}
	for name, attribute := range block.Body.Attributes {
		docker.Attributes[name] = ResourceAttribute{
			Expression: attribute.Expr,
			Source:     ir.SourceRef{File: file, Line: attribute.NameRange.Start.Line, Path: path + "." + name},
		}
	}
	seen := map[string]ir.SourceRef{}
	for _, child := range block.Body.Blocks {
		switch child.Type {
		case "project":
			declaration, err := parseResourceDeclaration(file, path, child, dockerProjectSchema, ctx)
			if err != nil {
				return Docker{}, err
			}
			if previous, exists := seen[declaration.Label]; exists {
				return Docker{}, fmt.Errorf("%s:%d:%s: duplicate Docker project label %q; first defined at %s:%d", file, declaration.Source.Line, declaration.Source.Path, declaration.Label, previous.File, previous.Line)
			}
			seen[declaration.Label] = declaration.Source
			docker.Projects = append(docker.Projects, declaration)
		case "lifecycle":
			if docker.Lifecycle.Source.Path != "" {
				return Docker{}, fmt.Errorf("%s:%d: duplicate %s.lifecycle block", file, child.TypeRange.Start.Line, path)
			}
			lifecycle, err := parseLifecycle(file, path+".lifecycle", child, ctx)
			if err != nil {
				return Docker{}, err
			}
			docker.Lifecycle = lifecycle
		default:
			return Docker{}, fmt.Errorf("%s:%d: unsupported block %s.%s", file, child.TypeRange.Start.Line, path, child.Type)
		}
	}
	return docker, nil
}

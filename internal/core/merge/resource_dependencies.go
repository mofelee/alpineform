package merge

import (
	"strconv"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
)

func resolveResourceDependencies(declarations []parser.ResourceDeclaration, prefix string) ([]ir.ResourceDependencySpec, error) {
	addresses := make(map[string]string, len(declarations))
	for _, declaration := range declarations {
		if !explicitDependencyResourceKind(declaration.Kind) {
			continue
		}
		addresses[resourceDependencyKey(declaration.Kind, declaration.Label)] = resourceDependencyAddress(prefix, declaration.Kind, declaration.Label)
	}

	var dependencies []ir.ResourceDependencySpec
	for _, declaration := range declarations {
		if !explicitDependencyResourceKind(declaration.Kind) {
			continue
		}
		from := addresses[resourceDependencyKey(declaration.Kind, declaration.Label)]
		for _, reference := range declaration.DependsOn {
			dependsOn, exists := addresses[resourceDependencyKey(reference.Kind, reference.Label)]
			if !exists {
				return nil, resourceError(reference.Source, "depends_on references unknown or out-of-scope %s", formatDependencyReference(reference.Kind, reference.Label))
			}
			dependencies = append(dependencies, ir.ResourceDependencySpec{From: from, DependsOn: dependsOn, Source: reference.Source})
		}
	}
	return dependencies, nil
}

func explicitDependencyResourceKind(kind string) bool {
	return kind == parser.ResourcePackage || kind == parser.ResourceFile || kind == parser.ResourceService
}

func resourceDependencyKey(kind, label string) string {
	return kind + "\x00" + label
}

func resourceDependencyAddress(prefix, kind, label string) string {
	return prefix + "." + resourceDependencyCollection(kind) + "." + kind + "[" + strconv.Quote(label) + "]"
}

func resourceDependencyCollection(kind string) string {
	switch kind {
	case parser.ResourcePackage:
		return "packages"
	case parser.ResourceFile:
		return "files"
	case parser.ResourceService:
		return "services"
	default:
		return "resources"
	}
}

func formatDependencyReference(kind, label string) string {
	if hclIdentifier(label) {
		return kind + "." + label
	}
	return kind + "[" + strconv.Quote(label) + "]"
}

func hclIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || character == '_' || character == '-' || (index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}

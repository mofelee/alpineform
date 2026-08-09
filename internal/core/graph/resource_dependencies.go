package graph

import (
	"fmt"
	"strings"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func applyExplicitDependencies(host string, nodes []Node, dependencies []ir.ResourceDependencySpec) error {
	prefix := "host." + host + "."
	byAddress := make(map[string]int, len(nodes))
	for index := range nodes {
		byAddress[nodes[index].Address] = index
	}

	for _, dependency := range dependencies {
		if !strings.HasPrefix(dependency.From, prefix) || !strings.HasPrefix(dependency.DependsOn, prefix) {
			return explicitDependencyError(dependency.Source, fmt.Sprintf(
				"explicit dependency crosses host scope: %q depends on %q",
				dependency.From,
				dependency.DependsOn,
			))
		}

		fromIndex, exists := byAddress[dependency.From]
		if !exists {
			return explicitDependencyError(dependency.Source, fmt.Sprintf(
				"dependent resource graph address %q does not exist",
				dependency.From,
			))
		}
		if _, exists := byAddress[dependency.DependsOn]; !exists {
			return explicitDependencyError(dependency.Source, fmt.Sprintf(
				"dependency resource graph address %q does not exist",
				dependency.DependsOn,
			))
		}

		node := &nodes[fromIndex]
		node.DependsOn = sortedUniqueStrings(append(append([]string(nil), node.DependsOn...), dependency.DependsOn))
		node.ExplicitDependsOn = sortedUniqueStrings(append(append([]string(nil), node.ExplicitDependsOn...), dependency.DependsOn))
		if node.DependencySources == nil {
			node.DependencySources = make(map[string]ir.SourceRef)
		}
		if _, exists := node.DependencySources[dependency.DependsOn]; !exists {
			node.DependencySources[dependency.DependsOn] = dependency.Source
		}
	}
	return nil
}

func explicitDependencyError(source ir.SourceRef, message string) error {
	if source.File != "" {
		return fmt.Errorf("%s:%d:%s: %s", source.File, source.Line, source.Path, message)
	}
	return fmt.Errorf("%s", message)
}

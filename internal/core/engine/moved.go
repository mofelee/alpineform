package engine

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

func prepareHostPlan(host ir.HostSpec, baseNodes []graph.Node, prior corestate.State) (ir.HostSpec, []graph.Node, corestate.MoveResult, error) {
	desired := desiredComponentRoots(host)
	preliminary, err := corestate.ResolveMoves(prior, host.Moves, desired, nil)
	if err != nil {
		return ir.HostSpec{}, nil, corestate.MoveResult{}, err
	}

	plannedHost := host
	plannedNodes := baseNodes
	if len(host.Moves) > 0 || len(prior.ComponentIdentities) > 0 {
		plannedHost, err = bindHostComponentIdentities(host, preliminary.Bindings)
		if err != nil {
			return ir.HostSpec{}, nil, corestate.MoveResult{}, err
		}
		plannedNodes, err = compileManagedHostNodes(plannedHost)
		if err != nil {
			return ir.HostSpec{}, nil, corestate.MoveResult{}, fmt.Errorf("compile host %q with retained component identities: %w", host.Name, err)
		}
	}

	targets, err := buildMoveTargets(plannedHost, plannedNodes, preliminary.Moves)
	if err != nil {
		return ir.HostSpec{}, nil, corestate.MoveResult{}, err
	}
	if len(targets) == 0 {
		return plannedHost, plannedNodes, preliminary, nil
	}
	resolved, err := corestate.ResolveMoves(prior, host.Moves, desired, targets)
	if err != nil {
		return ir.HostSpec{}, nil, corestate.MoveResult{}, err
	}
	if !reflect.DeepEqual(preliminary.Moves, resolved.Moves) || !reflect.DeepEqual(preliminary.Bindings, resolved.Bindings) {
		return ir.HostSpec{}, nil, corestate.MoveResult{}, fmt.Errorf("host %q move resolution changed while rebasing desired identities", host.Name)
	}
	return plannedHost, plannedNodes, resolved, nil
}

func desiredComponentRoots(host ir.HostSpec) map[string]bool {
	desired := make(map[string]bool, len(host.Components))
	for _, component := range host.Components {
		desired[componentRoot(host.Name, component.Name)] = true
	}
	return desired
}

func bindHostComponentIdentities(host ir.HostSpec, bindings map[string]corestate.ComponentIdentity) (ir.HostSpec, error) {
	out := host
	out.Components = append([]ir.ComponentInstanceSpec(nil), host.Components...)
	for index, component := range out.Components {
		root := componentRoot(host.Name, component.Name)
		identity, exists := bindings[root]
		if !exists {
			return ir.HostSpec{}, fmt.Errorf("host %q component %q has no resolved physical identity", host.Name, component.Name)
		}
		out.Components[index] = component.WithPhysicalName(identity.PhysicalName)
	}
	return out, nil
}

func compileManagedHostNodes(host ir.HostSpec) ([]graph.Node, error) {
	compiled, err := graph.Compile(&ir.Program{Hosts: []ir.HostSpec{host}})
	if err != nil {
		return nil, err
	}
	scheduled, err := compiled.Schedule()
	if err != nil {
		return nil, err
	}
	nodes := make([]graph.Node, 0, len(scheduled))
	for _, node := range scheduled {
		if node.Managed {
			nodes = append(nodes, node)
		}
	}
	return nodes, nil
}

func buildMoveTargets(host ir.HostSpec, targetNodes []graph.Node, moves []corestate.RealizedMove) (map[string]corestate.MoveTarget, error) {
	lineage := moveLineage(moves)
	if len(lineage) == 0 {
		return nil, nil
	}
	targetByAddress := make(map[string]graph.Node, len(targetNodes))
	renames := map[string]string{}
	for _, node := range targetNodes {
		targetByAddress[node.Address] = node
	}
	for target, source := range lineage {
		if _, exists := targetByAddress[target]; !exists {
			continue
		}
		targetName, targetOK := componentNameFromAddress(host.Name, target)
		sourceName, sourceOK := componentNameFromAddress(host.Name, source)
		if !targetOK || !sourceOK {
			return nil, fmt.Errorf("host %q realized move is not contained by component roots: %s -> %s", host.Name, source, target)
		}
		if previous, exists := renames[targetName]; exists && previous != sourceName {
			return nil, fmt.Errorf("host %q component %q has inconsistent move sources %q and %q", host.Name, targetName, previous, sourceName)
		}
		renames[targetName] = sourceName
	}
	if len(renames) == 0 {
		return nil, nil
	}

	legacyHost := hostWithLogicalComponentNames(host, renames)
	legacyNodes, err := compileManagedHostNodes(legacyHost)
	if err != nil {
		return nil, fmt.Errorf("compile host %q legacy component identities: %w", host.Name, err)
	}
	legacyByAddress := make(map[string]graph.Node, len(legacyNodes))
	for _, node := range legacyNodes {
		legacyByAddress[node.Address] = node
	}

	targets := make(map[string]corestate.MoveTarget)
	addresses := make([]string, 0, len(lineage))
	for target := range lineage {
		addresses = append(addresses, target)
	}
	sort.Strings(addresses)
	for _, target := range addresses {
		targetNode, targetExists := targetByAddress[target]
		if !targetExists {
			continue
		}
		legacyAddress := lineage[target]
		legacyNode, legacyExists := legacyByAddress[legacyAddress]
		if !legacyExists {
			return nil, fmt.Errorf("host %q cannot reconstruct legacy desired identity for %s -> %s", host.Name, legacyAddress, target)
		}
		targets[target] = corestate.MoveTarget{
			LegacyDesiredDigest: corestate.Digest(legacyNode.Desired),
			TargetDesiredDigest: corestate.Digest(targetNode.Desired),
		}
	}
	return targets, nil
}

func moveLineage(moves []corestate.RealizedMove) map[string]string {
	lineage := make(map[string]string, len(moves))
	for _, move := range moves {
		source := move.From
		if original, exists := lineage[move.From]; exists {
			source = original
			delete(lineage, move.From)
		}
		lineage[move.To] = source
	}
	return lineage
}

func hostWithLogicalComponentNames(host ir.HostSpec, renames map[string]string) ir.HostSpec {
	out := host
	out.Components = make([]ir.ComponentInstanceSpec, len(host.Components))
	for index, component := range host.Components {
		copyComponent := component
		copyComponent.DependsOn = append([]string(nil), component.DependsOn...)
		copyComponent.ExplicitDependencies = append([]ir.ResourceDependencySpec(nil), component.ExplicitDependencies...)
		for dependencyIndex, dependency := range copyComponent.DependsOn {
			if legacy, exists := renames[dependency]; exists {
				copyComponent.DependsOn[dependencyIndex] = legacy
			}
		}
		legacyName, renamed := renames[component.Name]
		if renamed {
			copyComponent.Name = legacyName
			copyComponent.ExplicitDependencies = rewriteComponentExplicitDependencies(host.Name, component.Name, legacyName, component.ExplicitDependencies)
			copyComponent.Scripts = rewriteComponentScripts(component.Scripts, component.Name, legacyName)
			copyComponent.Files = rewriteComponentFiles(component.Files, component.Name, legacyName)
			copyComponent.Install = rewriteComponentInstall(component.Install, component.Name, legacyName)
		}
		out.Components[index] = copyComponent
	}
	return out
}

func rewriteComponentExplicitDependencies(host, from, to string, dependencies []ir.ResourceDependencySpec) []ir.ResourceDependencySpec {
	out := append([]ir.ResourceDependencySpec(nil), dependencies...)
	fromPrefix := componentRoot(host, from)
	toPrefix := componentRoot(host, to)
	for index := range out {
		out[index].From = rewriteComponentAddressPrefix(out[index].From, fromPrefix, toPrefix)
		out[index].DependsOn = rewriteComponentAddressPrefix(out[index].DependsOn, fromPrefix, toPrefix)
	}
	return out
}

func rewriteComponentAddressPrefix(address, from, to string) string {
	if address == from {
		return to
	}
	if !strings.HasPrefix(address, from+".") {
		return address
	}
	return to + strings.TrimPrefix(address, from)
}

func rewriteComponentScripts(scripts map[string]ir.ScriptSpec, from, to string) map[string]ir.ScriptSpec {
	if scripts == nil {
		return nil
	}
	out := make(map[string]ir.ScriptSpec, len(scripts))
	for name, script := range scripts {
		script.DeclarationID = rewriteComponentDeclarationID(script.DeclarationID, from, to)
		out[name] = script
	}
	return out
}

func rewriteComponentFiles(files []ir.ManagedFileSpec, from, to string) []ir.ManagedFileSpec {
	out := append([]ir.ManagedFileSpec(nil), files...)
	for index := range out {
		out[index].OnChange = rewriteScriptReference(out[index].OnChange, from, to)
	}
	return out
}

func rewriteComponentInstall(install *ir.ComponentArtifactInstallSpec, from, to string) *ir.ComponentArtifactInstallSpec {
	if install == nil {
		return nil
	}
	out := *install
	out.OnChange = rewriteScriptReference(install.OnChange, from, to)
	return &out
}

func rewriteScriptReference(reference *ir.ScriptReferenceSpec, from, to string) *ir.ScriptReferenceSpec {
	if reference == nil {
		return nil
	}
	out := *reference
	out.DeclarationID = rewriteComponentDeclarationID(out.DeclarationID, from, to)
	return &out
}

func rewriteComponentDeclarationID(declaration, from, to string) string {
	prefix := "component." + from + "."
	if !strings.HasPrefix(declaration, prefix) {
		return declaration
	}
	return "component." + to + "." + strings.TrimPrefix(declaration, prefix)
}

func componentRoot(host, component string) string {
	return "host." + host + ".component." + component
}

func componentNameFromAddress(host, address string) (string, bool) {
	prefix := componentRoot(host, "")
	if !strings.HasPrefix(address, prefix) {
		return "", false
	}
	remainder := strings.TrimPrefix(address, prefix)
	if separator := strings.IndexByte(remainder, '.'); separator >= 0 {
		remainder = remainder[:separator]
	}
	return remainder, remainder != ""
}

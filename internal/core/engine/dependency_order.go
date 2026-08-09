package engine

import (
	"fmt"
	"sort"
	"strings"

	corestate "github.com/mofelee/alpineform/internal/core/state"
)

func orderStepsForExecution(steps []Step) ([]Step, error) {
	byAddress := make(map[string]Step, len(steps))
	rank := make(map[string]int, len(steps))
	indegree := make(map[string]int, len(steps))
	dependents := make(map[string][]string, len(steps))
	edges := make(map[string]map[string]struct{}, len(steps))
	for index, step := range steps {
		if step.Address == "" {
			return nil, fmt.Errorf("resource action step has an empty address")
		}
		if _, exists := byAddress[step.Address]; exists {
			return nil, fmt.Errorf("duplicate resource action step %q", step.Address)
		}
		byAddress[step.Address] = step
		rank[step.Address] = index
		indegree[step.Address] = 0
	}
	addBefore := func(before, after string) {
		if _, exists := byAddress[before]; !exists {
			return
		}
		if _, exists := byAddress[after]; !exists {
			return
		}
		if edges[before] == nil {
			edges[before] = map[string]struct{}{}
		}
		if _, exists := edges[before][after]; exists {
			return
		}
		edges[before][after] = struct{}{}
		dependents[before] = append(dependents[before], after)
		indegree[after]++
	}
	addInferredBefore := func(before, after string) {
		if actionPathExists(edges, after, before) {
			// An authored teardown path takes precedence over a redundant
			// inferred shortcut compiled for the forward lifecycle.
			return
		}
		addBefore(before, after)
	}

	for _, step := range steps {
		if step.Node.Address == "" {
			if step.Prior == nil {
				continue
			}
			for _, dependency := range canonicalAddresses(step.Prior.DependsOn) {
				addBefore(step.Address, dependency)
			}
			continue
		}

		for _, dependency := range canonicalAddresses(step.Node.ExplicitDependsOn) {
			dependencyStep, exists := byAddress[dependency]
			if exists && removesRemoteObject(dependencyStep.Action) {
				addBefore(step.Address, dependency)
			} else {
				addBefore(dependency, step.Address)
			}
		}
	}
	for _, step := range steps {
		if step.Node.Address == "" {
			continue
		}
		explicit := make(map[string]struct{}, len(step.Node.ExplicitDependsOn))
		for _, dependency := range canonicalAddresses(step.Node.ExplicitDependsOn) {
			explicit[dependency] = struct{}{}
		}
		for _, dependency := range canonicalAddresses(step.Node.DependsOn) {
			if _, authored := explicit[dependency]; authored {
				continue
			}
			addInferredBefore(dependency, step.Address)
		}
	}

	less := func(left, right string) bool {
		if rank[left] != rank[right] {
			return rank[left] < rank[right]
		}
		return left < right
	}
	for address := range dependents {
		sort.SliceStable(dependents[address], func(i, j int) bool {
			return less(dependents[address][i], dependents[address][j])
		})
	}
	ready := make([]string, 0, len(steps))
	for address, count := range indegree {
		if count == 0 {
			ready = append(ready, address)
		}
	}
	sort.SliceStable(ready, func(i, j int) bool { return less(ready[i], ready[j]) })
	ordered := make([]Step, 0, len(steps))
	for len(ready) > 0 {
		address := ready[0]
		ready = ready[1:]
		ordered = append(ordered, byAddress[address])
		for _, dependent := range dependents[address] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
		sort.SliceStable(ready, func(i, j int) bool { return less(ready[i], ready[j]) })
	}
	if len(ordered) != len(steps) {
		cycle := actionDependencyCycle(edges, byAddress)
		return nil, fmt.Errorf("resource action dependency cycle: %s", strings.Join(cycle, " -> "))
	}
	return ordered, nil
}

func actionPathExists(edges map[string]map[string]struct{}, from, to string) bool {
	if from == to {
		return true
	}
	visited := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		address := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for next := range edges[address] {
			if next == to {
				return true
			}
			if visited[next] {
				continue
			}
			visited[next] = true
			stack = append(stack, next)
		}
	}
	return false
}

func actionDependencyCycle(edges map[string]map[string]struct{}, steps map[string]Step) []string {
	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[string]int, len(steps))
	stackIndex := make(map[string]int, len(steps))
	stack := make([]string, 0, len(steps))
	var cycle []string
	var visit func(string) bool
	visit = func(address string) bool {
		state[address] = visiting
		stackIndex[address] = len(stack)
		stack = append(stack, address)
		outgoing := make([]string, 0, len(edges[address]))
		for dependent := range edges[address] {
			outgoing = append(outgoing, dependent)
		}
		sort.Strings(outgoing)
		for _, dependent := range outgoing {
			switch state[dependent] {
			case unvisited:
				if visit(dependent) {
					return true
				}
			case visiting:
				cycle = append([]string(nil), stack[stackIndex[dependent]:]...)
				cycle = append(cycle, dependent)
				cycle = canonicalActionCycle(cycle)
				return true
			}
		}
		stack = stack[:len(stack)-1]
		delete(stackIndex, address)
		state[address] = visited
		return false
	}
	addresses := make([]string, 0, len(steps))
	for address := range steps {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	for _, address := range addresses {
		if state[address] == unvisited && visit(address) {
			return cycle
		}
	}
	return []string{"unknown"}
}

func canonicalActionCycle(cycle []string) []string {
	if len(cycle) <= 2 {
		return append([]string(nil), cycle...)
	}
	unique := cycle[:len(cycle)-1]
	start := 0
	for index := 1; index < len(unique); index++ {
		if unique[index] < unique[start] {
			start = index
		}
	}
	out := make([]string, 0, len(cycle))
	out = append(out, unique[start:]...)
	out = append(out, unique[:start]...)
	out = append(out, out[0])
	return out
}

func removesRemoteObject(action string) bool {
	return action == ActionDelete || action == ActionDestroy
}

func reconcileStateDependencies(resources map[string]corestate.Resource, steps []Step) {
	for _, step := range steps {
		if step.Node.Address == "" {
			continue
		}
		resource, exists := resources[step.Address]
		if !exists {
			continue
		}
		resource.DependsOn = trackedStateDependencies(step.Node.ExplicitDependsOn, resources)
		resources[step.Address] = resource
	}
}

func trackedStateDependencies(dependencies []string, resources map[string]corestate.Resource) []string {
	canonical := canonicalAddresses(dependencies)
	out := make([]string, 0, len(canonical))
	for _, dependency := range canonical {
		if _, exists := resources[dependency]; exists {
			out = append(out, dependency)
		}
	}
	return out
}

func removeStateDependencyReferences(resources map[string]corestate.Resource, removed string) {
	for address, resource := range resources {
		filtered := make([]string, 0, len(resource.DependsOn))
		changed := false
		for _, dependency := range resource.DependsOn {
			if dependency == removed {
				changed = true
				continue
			}
			filtered = append(filtered, dependency)
		}
		if !changed {
			continue
		}
		resource.DependsOn = filtered
		resources[address] = resource
	}
}

func canonicalAddresses(addresses []string) []string {
	canonical := append([]string(nil), addresses...)
	sort.Strings(canonical)
	unique := canonical[:0]
	for _, address := range canonical {
		if len(unique) == 0 || unique[len(unique)-1] != address {
			unique = append(unique, address)
		}
	}
	return unique
}

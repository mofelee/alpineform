// Package graph builds and validates AlpineForm's deterministic resource graph.
package graph

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mofelee/alpineform/internal/core/ir"
)

type ResourceGraph struct {
	Nodes []Node `json:"nodes"`
}

type Node struct {
	Host      string            `json:"host,omitempty"`
	Address   string            `json:"address"`
	Kind      string            `json:"kind"`
	Managed   bool              `json:"managed"`
	Summary   string            `json:"summary,omitempty"`
	Source    ir.SourceRef      `json:"source"`
	Lifecycle *ir.LifecycleSpec `json:"lifecycle,omitempty"`
	Desired   map[string]any    `json:"desired,omitempty"`
	DependsOn []string          `json:"depends_on,omitempty"`
	Sensitive bool              `json:"-"`
	Ephemeral bool              `json:"-"`
}

func (node Node) MarshalJSON() ([]byte, error) {
	type nodeJSON Node
	out := struct {
		nodeJSON
		Protected bool `json:"protected,omitempty"`
	}{nodeJSON: nodeJSON(node)}
	if node.Sensitive || node.Ephemeral {
		out.Desired = nil
		out.Protected = true
	}
	return json.Marshal(out)
}

func Compile(program *ir.Program) (*ResourceGraph, error) {
	graph := &ResourceGraph{}
	for _, host := range program.Hosts {
		hostAddress := "host." + host.Name
		graph.Nodes = append(graph.Nodes, Node{
			Host:    host.Name,
			Address: hostAddress,
			Kind:    "host",
			Managed: false,
			Summary: "configuration root for host " + host.Name,
			Source:  host.Source,
		})
		if host.Platform != nil {
			graph.Nodes = append(graph.Nodes, Node{
				Host:      host.Name,
				Address:   hostAddress + ".platform",
				Kind:      "platform",
				Managed:   false,
				Summary:   "offline Alpine platform facts",
				Source:    host.Platform.Source,
				DependsOn: []string{hostAddress},
				Desired: map[string]any{
					"architecture":        host.Platform.Architecture,
					"version":             host.Platform.Version,
					"branch":              host.Platform.Branch,
					"libc":                host.Platform.Libc,
					"native_architecture": host.Platform.NativeArchitecture,
				},
			})
		}
		for _, component := range host.Components {
			address := hostAddress + ".component." + component.Name
			dependencies := []string{hostAddress}
			for _, dependency := range component.DependsOn {
				dependencies = append(dependencies, hostAddress+".component."+dependency)
			}
			sort.Strings(dependencies)
			graph.Nodes = append(graph.Nodes, Node{
				Host:      host.Name,
				Address:   address,
				Kind:      "component",
				Managed:   false,
				Summary:   "component instance " + component.Name + " from " + component.Template,
				Source:    component.Source,
				Lifecycle: &component.Lifecycle,
				DependsOn: dependencies,
				Desired: map[string]any{
					"template":         component.Template,
					"input_names":      append([]string(nil), component.InputNames...),
					"protected_inputs": append([]string(nil), component.ProtectedInputs...),
				},
			})
		}
	}
	sort.SliceStable(graph.Nodes, func(i, j int) bool { return graph.Nodes[i].Address < graph.Nodes[j].Address })
	if err := graph.Validate(); err != nil {
		return nil, err
	}
	return graph, nil
}

func (graph *ResourceGraph) Validate() error {
	byAddress := make(map[string]Node, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if node.Address == "" {
			return fmt.Errorf("%s:%d:%s: graph node has an empty address", node.Source.File, node.Source.Line, node.Source.Path)
		}
		if previous, exists := byAddress[node.Address]; exists {
			return fmt.Errorf("%s:%d:%s: duplicate resource address %q; first defined at %s:%d", node.Source.File, node.Source.Line, node.Source.Path, node.Address, previous.Source.File, previous.Source.Line)
		}
		byAddress[node.Address] = node
	}
	for _, node := range graph.Nodes {
		for _, dependency := range node.DependsOn {
			if _, exists := byAddress[dependency]; !exists {
				return fmt.Errorf("%s:%d:%s: resource %q depends on unknown address %q", node.Source.File, node.Source.Line, node.Source.Path, node.Address, dependency)
			}
		}
	}
	_, err := graph.Schedule()
	return err
}

func (graph *ResourceGraph) Schedule() ([]Node, error) {
	byAddress := make(map[string]Node, len(graph.Nodes))
	indegree := make(map[string]int, len(graph.Nodes))
	dependents := make(map[string][]string, len(graph.Nodes))
	for _, node := range graph.Nodes {
		byAddress[node.Address] = node
		indegree[node.Address] = len(node.DependsOn)
		for _, dependency := range node.DependsOn {
			dependents[dependency] = append(dependents[dependency], node.Address)
		}
	}
	ready := make([]string, 0, len(graph.Nodes))
	for address, degree := range indegree {
		if degree == 0 {
			ready = append(ready, address)
		}
	}
	sort.Strings(ready)
	ordered := make([]Node, 0, len(graph.Nodes))
	for len(ready) > 0 {
		address := ready[0]
		ready = ready[1:]
		ordered = append(ordered, byAddress[address])
		for _, dependent := range dependents[address] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
				sort.Strings(ready)
			}
		}
	}
	if len(ordered) == len(graph.Nodes) {
		return ordered, nil
	}
	var cycle []string
	for address, degree := range indegree {
		if degree > 0 {
			cycle = append(cycle, address)
		}
	}
	sort.Strings(cycle)
	first := byAddress[cycle[0]].Source
	return nil, fmt.Errorf("%s:%d:%s: resource dependency cycle involves: %s", first.File, first.Line, first.Path, strings.Join(cycle, ", "))
}

func (graph *ResourceGraph) ManagedCount() int {
	count := 0
	for _, node := range graph.Nodes {
		if node.Managed {
			count++
		}
	}
	return count
}

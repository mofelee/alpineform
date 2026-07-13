// Package graph builds and validates AlpineForm's deterministic resource graph.
package graph

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mofelee/alpineform/internal/core/ir"
)

type ResourceGraph struct {
	Nodes []Node `json:"nodes"`
}

type Node struct {
	Host       string            `json:"host,omitempty"`
	Address    string            `json:"address"`
	Kind       string            `json:"kind"`
	Managed    bool              `json:"managed"`
	Summary    string            `json:"summary,omitempty"`
	Source     ir.SourceRef      `json:"source"`
	Lifecycle  *ir.LifecycleSpec `json:"lifecycle,omitempty"`
	Desired    map[string]any    `json:"desired,omitempty"`
	Payload    map[string]any    `json:"-"`
	DependsOn  []string          `json:"depends_on,omitempty"`
	Sensitive  bool              `json:"-"`
	Ephemeral  bool              `json:"-"`
	DigestSafe bool              `json:"-"`
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
		for _, directory := range host.Directories {
			deleteBehavior := directory.OnRemove
			if deleteBehavior == "forget" {
				deleteBehavior = ""
			}
			graph.Nodes = append(graph.Nodes, Node{
				Host:      host.Name,
				Address:   directoryResourceAddress(host.Name, directory.Path),
				Kind:      "directory",
				Managed:   true,
				Summary:   directorySummary(directory),
				Source:    directory.Source,
				Lifecycle: &directory.Lifecycle,
				Desired: map[string]any{
					"path":             directory.Path,
					"owner":            directory.Owner,
					"group":            directory.Group,
					"mode":             directory.Mode,
					"ensure":           directory.Ensure,
					"recursive_delete": directory.RecursiveDelete,
					"delete_behavior":  deleteBehavior,
					"delete": map[string]any{
						"path":      directory.Path,
						"recursive": directory.RecursiveDelete,
					},
					"prevent_destroy": directory.Lifecycle.PreventDestroy,
				},
				DependsOn:  directoryDependencies(host.Name, hostAddress, directory, host.Directories, host.Files),
				DigestSafe: true,
			})
		}
		for _, file := range host.Files {
			address := fileResourceAddress(host.Name, file.Path)
			deleteBehavior := file.OnRemove
			if deleteBehavior == "forget" {
				deleteBehavior = ""
			}
			contentBytes := file.ContentBytes
			if file.ContentWriteOnly {
				contentBytes = 0
			}
			desired := map[string]any{
				"path":               file.Path,
				"owner":              file.Owner,
				"group":              file.Group,
				"mode":               file.Mode,
				"ensure":             file.Ensure,
				"content_sha256":     file.ContentSHA256,
				"content_bytes":      contentBytes,
				"content_version":    file.ContentVersion,
				"content_write_only": file.ContentWriteOnly,
				"delete_behavior":    deleteBehavior,
				"delete":             map[string]any{"path": file.Path},
				"prevent_destroy":    file.Lifecycle.PreventDestroy,
			}
			graph.Nodes = append(graph.Nodes, Node{
				Host:      host.Name,
				Address:   address,
				Kind:      "file",
				Managed:   true,
				Summary:   fileSummary(file),
				Source:    file.Source,
				Lifecycle: &file.Lifecycle,
				Desired:   desired,
				Payload: map[string]any{
					"content": file.Content,
				},
				DependsOn:  fileDependencies(host.Name, hostAddress, file, host.Directories),
				Sensitive:  file.Sensitive,
				Ephemeral:  file.Ephemeral,
				DigestSafe: true,
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

func fileSummary(file ir.ManagedFileSpec) string {
	if file.Ensure == "absent" {
		return "ensure file is absent " + file.Path
	}
	return "manage file " + file.Path
}

func directorySummary(directory ir.ManagedDirectorySpec) string {
	if directory.Ensure == "absent" {
		return "ensure directory is absent " + directory.Path
	}
	return "manage directory " + directory.Path
}

func fileDependencies(host, hostAddress string, file ir.ManagedFileSpec, directories []ir.ManagedDirectorySpec) []string {
	dependencies := []string{hostAddress}
	if file.Ensure != "present" {
		return dependencies
	}
	if parent, exists := nearestPresentDirectory(file.Path, directories); exists {
		dependencies = append(dependencies, directoryResourceAddress(host, parent.Path))
	}
	return dependencies
}

func directoryDependencies(host, hostAddress string, directory ir.ManagedDirectorySpec, directories []ir.ManagedDirectorySpec, files []ir.ManagedFileSpec) []string {
	dependencies := []string{hostAddress}
	if directory.Ensure == "present" {
		if parent, exists := nearestPresentDirectory(directory.Path, directories); exists {
			dependencies = append(dependencies, directoryResourceAddress(host, parent.Path))
		}
		return dependencies
	}
	for _, child := range directories {
		if child.Ensure == "absent" && pathWithin(directory.Path, child.Path) {
			dependencies = append(dependencies, directoryResourceAddress(host, child.Path))
		}
	}
	for _, file := range files {
		if file.Ensure == "absent" && pathWithin(directory.Path, file.Path) {
			dependencies = append(dependencies, fileResourceAddress(host, file.Path))
		}
	}
	sort.Strings(dependencies)
	return dependencies
}

func nearestPresentDirectory(path string, directories []ir.ManagedDirectorySpec) (ir.ManagedDirectorySpec, bool) {
	var nearest ir.ManagedDirectorySpec
	found := false
	for _, directory := range directories {
		if directory.Ensure != "present" || !pathWithin(directory.Path, path) {
			continue
		}
		if !found || len(directory.Path) > len(nearest.Path) {
			nearest = directory
			found = true
		}
	}
	return nearest, found
}

func pathWithin(parent, child string) bool {
	if parent == child {
		return false
	}
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func directoryResourceAddress(host, path string) string {
	return "host." + host + ".directories.directory[" + strconv.Quote(path) + "]"
}

func fileResourceAddress(host, path string) string {
	return "host." + host + ".files.file[" + strconv.Quote(path) + "]"
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

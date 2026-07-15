package graph

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func appendComponentBuildNodes(resourceGraph *ResourceGraph, host ir.HostSpec, component ir.ComponentInstanceSpec, componentAddress string) {
	if component.Build == nil || component.Install == nil {
		return
	}
	build := *component.Build
	install := *component.Install
	addressDigest := sha256.Sum256([]byte(componentAddress))
	ownerID := fmt.Sprintf("%x", addressDigest[:16])
	workspace := "/var/tmp/alpineform/builds/" + build.Identity
	outputCache := "/var/cache/alpineform/builds/outputs/" + build.Identity + "/artifact"
	outputMarker := outputCache + ".sha256"
	dependencyMarker := "/var/lib/alpineform/builds/" + ownerID + ".dependencies"
	installMarker := "/var/lib/alpineform/builds/" + ownerID + ".installed"
	virtualPackage := ".alpineform-build-" + ownerID[:24]
	deleteBehavior := ""
	if build.OnRemove == "destroy" {
		deleteBehavior = "destroy"
	}

	inputAddresses := make([]string, 0, len(build.Inputs))
	inputPaths := make(map[string]string, len(build.Inputs))
	inputSHA256 := make(map[string]string, len(build.Inputs))
	protectedInputPaths := []string{}
	for _, input := range build.Inputs {
		cacheKey := input.PayloadSHA256
		cacheRoot := "/var/cache/alpineform/builds/inputs/"
		if input.Sensitive || input.Ephemeral {
			digest := sha256.Sum256([]byte(build.Identity + "\x00" + input.Name))
			cacheKey = fmt.Sprintf("%x", digest[:])
			cacheRoot = "/run/alpineform/build-inputs/"
			protectedInputPaths = append(protectedInputPaths, cacheRoot+cacheKey)
		}
		cachePath := cacheRoot + cacheKey
		address := componentAddress + ".build.input[" + strconv.Quote(input.Name) + "]"
		inputAddresses = append(inputAddresses, address)
		inputPaths[input.Destination] = cachePath
		inputSHA256[input.Destination] = input.PayloadSHA256
		desired := map[string]any{
			"name": input.Name, "kind": input.Kind, "path": cachePath, "destination": input.Destination,
			"sha256": input.SHA256, "content_version": input.ContentVersion, "url": input.URL,
			"ensure": "present", "delete_behavior": deleteBehavior,
			"delete": map[string]any{"path": cachePath}, "prevent_destroy": component.Lifecycle.PreventDestroy,
		}
		resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
			Host: host.Name, Address: address, Kind: "component_build_input", Managed: true,
			Summary: "stage checksummed source-build input " + input.Name, Source: input.Source, Lifecycle: &component.Lifecycle,
			Desired: desired, Payload: map[string]any{"content": append([]byte(nil), input.Content...), "sha256": input.PayloadSHA256},
			DependsOn: []string{componentAddress}, Sensitive: input.Sensitive, Ephemeral: input.Ephemeral, DigestSafe: true,
		})
	}
	sort.Strings(inputAddresses)

	dependenciesAddress := componentAddress + ".build.dependencies"
	resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
		Host: host.Name, Address: dependenciesAddress, Kind: "component_build_dependencies", Managed: true,
		Summary: "own source-build APK dependencies through " + virtualPackage, Source: build.Source, Lifecycle: &component.Lifecycle,
		Desired: map[string]any{
			"build_identity": build.Identity, "packages": append([]string(nil), build.Dependencies...),
			"virtual_package": virtualPackage, "marker_path": dependencyMarker, "output_marker": outputMarker,
			"ensure": "present", "delete_behavior": deleteBehavior,
			"delete": map[string]any{
				"virtual_package": virtualPackage, "marker_path": dependencyMarker,
				"build_identity": build.Identity, "workspace": workspace,
			},
			"prevent_destroy": component.Lifecycle.PreventDestroy,
		},
		DependsOn: inputAddresses, DigestSafe: true,
	})

	workspaceAddress := componentAddress + ".build.workspace"
	commands := make([]map[string]any, 0, len(build.Commands))
	commandPayload := make([]map[string]any, 0, len(build.Commands))
	for _, command := range build.Commands {
		commands = append(commands, map[string]any{
			"argv": append([]string(nil), command.Argv...), "stdin_sha256": command.StdinSHA256,
			"stdin_version": command.StdinVersion, "protected": command.Sensitive || command.Ephemeral,
		})
		commandPayload = append(commandPayload, map[string]any{"argv": append([]string(nil), command.Argv...), "stdin": append([]byte(nil), command.Stdin...)})
	}
	resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
		Host: host.Name, Address: workspaceAddress, Kind: "component_build_workspace", Managed: true,
		Summary: "execute deterministic source build in an isolated workspace", Source: build.Source, Lifecycle: &component.Lifecycle,
		Desired: map[string]any{
			"build_identity": build.Identity, "workspace": workspace, "working_directory": build.WorkingDirectory,
			"input_paths": inputPaths, "commands": commands, "environment_names": append([]string(nil), build.EnvironmentNames...),
			"environment_version": build.EnvironmentVersion, "network": build.Network, "output": build.Output,
			"output_marker": outputMarker, "virtual_package": virtualPackage, "dependency_marker": dependencyMarker,
			"protected_input_paths": append([]string(nil), protectedInputPaths...),
			"ensure":                "present", "delete_behavior": deleteBehavior,
			"delete": map[string]any{"workspace": workspace}, "prevent_destroy": component.Lifecycle.PreventDestroy,
		},
		Payload:   map[string]any{"environment": cloneStringMap(build.Environment), "commands": commandPayload, "input_sha256": inputSHA256},
		DependsOn: append(append([]string(nil), inputAddresses...), dependenciesAddress), TriggeredBy: append(append([]string(nil), inputAddresses...), dependenciesAddress),
		Sensitive: build.Sensitive, Ephemeral: build.Ephemeral, DigestSafe: true,
	})

	outputAddress := componentAddress + ".build.output[" + strconv.Quote(build.Output) + "]"
	resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
		Host: host.Name, Address: outputAddress, Kind: "component_build_output", Managed: true,
		Summary: "verify source-build output " + build.Output, Source: build.Source, Lifecycle: &component.Lifecycle,
		Desired: map[string]any{
			"build_identity": build.Identity, "workspace": workspace, "output": build.Output,
			"output_sha256": build.OutputSHA256, "max_output_bytes": build.MaxOutputBytes,
			"cache_path": outputCache, "marker_path": outputMarker, "virtual_package": virtualPackage,
			"dependency_marker": dependencyMarker, "ensure": "present", "delete_behavior": deleteBehavior,
			"protected_input_paths": append([]string(nil), protectedInputPaths...),
			"delete":                map[string]any{"cache_path": outputCache, "marker_path": outputMarker},
			"prevent_destroy":       component.Lifecycle.PreventDestroy,
		},
		DependsOn: []string{workspaceAddress}, TriggeredBy: []string{workspaceAddress},
		Sensitive: build.Sensitive, Ephemeral: build.Ephemeral, DigestSafe: true,
	})

	cleanupAddress := componentAddress + ".build.cleanup"
	resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
		Host: host.Name, Address: cleanupAddress, Kind: "component_build_cleanup", Managed: true,
		Summary: "clean source-build workspace and owned APK dependencies", Source: build.Source, Lifecycle: &component.Lifecycle,
		Desired: map[string]any{
			"build_identity": build.Identity, "workspace": workspace, "output_marker": outputMarker,
			"virtual_package": virtualPackage, "dependency_marker": dependencyMarker,
			"protected_input_paths": append([]string(nil), protectedInputPaths...),
			"ensure":                "present", "delete_behavior": "", "prevent_destroy": component.Lifecycle.PreventDestroy,
		},
		DependsOn: []string{outputAddress}, TriggeredBy: []string{outputAddress}, DigestSafe: true,
	})

	installAddress := componentAddress + ".build.install[" + strconv.Quote(install.Path) + "]"
	resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
		Host: host.Name, Address: installAddress, Kind: "component_build_install", Managed: true,
		Summary: "atomically install source-build output at " + install.Path, Source: install.Source, Lifecycle: &component.Lifecycle,
		Desired: map[string]any{
			"build_identity": build.Identity, "cache_path": outputCache, "output_marker": outputMarker,
			"path": install.Path, "owner": install.Owner, "group": install.Group, "mode": install.Mode,
			"install_marker": installMarker, "ensure": "present", "delete_behavior": deleteBehavior,
			"delete":          map[string]any{"path": install.Path, "install_marker": installMarker, "cache_path": outputCache, "output_marker": outputMarker},
			"prevent_destroy": component.Lifecycle.PreventDestroy,
		},
		DependsOn: []string{cleanupAddress}, TriggeredBy: []string{cleanupAddress},
		Sensitive: build.Sensitive, Ephemeral: build.Ephemeral, DigestSafe: true,
	})
}

func cloneStringMap(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

// Package graph builds and validates AlpineForm's deterministic resource graph.
package graph

import (
	"crypto/sha256"
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
	Host        string            `json:"host,omitempty"`
	Address     string            `json:"address"`
	Kind        string            `json:"kind"`
	Managed     bool              `json:"managed"`
	Summary     string            `json:"summary,omitempty"`
	Source      ir.SourceRef      `json:"source"`
	Lifecycle   *ir.LifecycleSpec `json:"lifecycle,omitempty"`
	Desired     map[string]any    `json:"desired,omitempty"`
	Payload     map[string]any    `json:"-"`
	DependsOn   []string          `json:"depends_on,omitempty"`
	TriggeredBy []string          `json:"triggered_by,omitempty"`
	Sensitive   bool              `json:"-"`
	Ephemeral   bool              `json:"-"`
	DigestSafe  bool              `json:"-"`
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
		appendAPKNodes(graph, host, hostAddress)
		appendPackageNodes(graph, host, hostAddress)
		for _, group := range host.Groups {
			deleteBehavior := group.OnRemove
			if deleteBehavior == "forget" {
				deleteBehavior = ""
			}
			graph.Nodes = append(graph.Nodes, Node{
				Host:      host.Name,
				Address:   groupResourceAddress(host.Name, group.Name),
				Kind:      "group",
				Managed:   true,
				Summary:   groupSummary(group),
				Source:    group.Source,
				Lifecycle: &group.Lifecycle,
				Desired: map[string]any{
					"name":            group.Name,
					"gid":             group.GID,
					"system":          group.System,
					"ensure":          group.Ensure,
					"delete_behavior": deleteBehavior,
					"delete":          map[string]any{"name": group.Name},
					"prevent_destroy": group.Lifecycle.PreventDestroy,
				},
				DependsOn:  groupDependencies(host.Name, hostAddress, group, host.Directories, host.Files, host.Users),
				DigestSafe: true,
			})
		}
		for _, user := range host.Users {
			deleteBehavior := user.OnRemove
			if deleteBehavior == "forget" {
				deleteBehavior = ""
			}
			graph.Nodes = append(graph.Nodes, Node{
				Host:      host.Name,
				Address:   userResourceAddress(host.Name, user.Name),
				Kind:      "user",
				Managed:   true,
				Summary:   userSummary(user),
				Source:    user.Source,
				Lifecycle: &user.Lifecycle,
				Desired: map[string]any{
					"name":            user.Name,
					"uid":             user.UID,
					"group":           user.PrimaryGroup,
					"home":            user.Home,
					"shell":           user.Shell,
					"system":          user.System,
					"ensure":          user.Ensure,
					"delete_behavior": deleteBehavior,
					"delete":          map[string]any{"name": user.Name},
					"prevent_destroy": user.Lifecycle.PreventDestroy,
				},
				DependsOn:  userDependencies(host.Name, hostAddress, user, host.Groups, host.Directories, host.Files),
				DigestSafe: true,
			})
			for _, membership := range user.Groups {
				graph.Nodes = append(graph.Nodes, Node{
					Host:      host.Name,
					Address:   membershipResourceAddress(host.Name, user.Name, membership.Group),
					Kind:      "membership",
					Managed:   true,
					Summary:   membershipSummary(user.Name, membership),
					Source:    membership.Source,
					Lifecycle: &user.Lifecycle,
					Desired: map[string]any{
						"user":            user.Name,
						"group":           membership.Group,
						"ensure":          membership.Ensure,
						"delete_behavior": "destroy",
						"delete":          map[string]any{"user": user.Name, "group": membership.Group},
						"prevent_destroy": user.Lifecycle.PreventDestroy,
					},
					DependsOn:  membershipDependencies(host.Name, hostAddress, user, membership, host.Groups),
					DigestSafe: true,
				})
			}
			for _, key := range user.AuthorizedKeys {
				graph.Nodes = append(graph.Nodes, Node{
					Host:      host.Name,
					Address:   authorizedKeyResourceAddress(host.Name, user.Name, key.Fingerprint),
					Kind:      "authorized_key",
					Managed:   true,
					Summary:   authorizedKeySummary(user.Name, key),
					Source:    key.Source,
					Lifecycle: &user.Lifecycle,
					Desired: map[string]any{
						"user":            user.Name,
						"fingerprint":     key.Fingerprint,
						"metadata_ok":     true,
						"ensure":          key.Ensure,
						"delete_behavior": "destroy",
						"delete":          map[string]any{"user": user.Name, "key_type": key.KeyType, "key_blob": key.KeyBlob},
						"prevent_destroy": user.Lifecycle.PreventDestroy,
					},
					Payload: map[string]any{
						"line":     key.Line,
						"key_type": key.KeyType,
						"key_blob": key.KeyBlob,
					},
					DependsOn:  authorizedKeyDependencies(host.Name, hostAddress, user, key),
					DigestSafe: true,
				})
			}
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
				DependsOn:  directoryDependencies(host.Name, hostAddress, directory, host.Directories, host.Files, host.Groups, host.Users),
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
				DependsOn:  fileDependencies(host.Name, hostAddress, file, host.Directories, host.Groups, host.Users),
				Sensitive:  file.Sensitive,
				Ephemeral:  file.Ephemeral,
				DigestSafe: true,
			})
		}
		appendServiceNodes(graph, host, hostAddress)
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

func appendServiceNodes(resourceGraph *ResourceGraph, host ir.HostSpec, hostAddress string) {
	for _, service := range host.Services {
		dependencies := []string{hostAddress}
		triggers := []string{}
		for _, file := range host.Files {
			if file.Ensure == "present" && (file.Path == "/etc/init.d/"+service.Name || file.Path == "/etc/conf.d/"+service.Name) {
				address := fileResourceAddress(host.Name, file.Path)
				dependencies = append(dependencies, address)
				if service.Operation != "" {
					triggers = append(triggers, address)
				}
			}
		}
		if service.Package != "" {
			dependencies = append(dependencies, packageResourceAddress(host.Name, service.Package))
		}
		if service.User != "" {
			if user, found := managedUser(service.User, host.Users); found && user.Ensure == "present" {
				dependencies = append(dependencies, userResourceAddress(host.Name, user.Name))
				dependencies = append(dependencies, presentUserChildAddresses(host.Name, user)...)
			}
		}
		if service.Group != "" {
			if group, found := managedGroup(service.Group, host.Groups); found && group.Ensure == "present" {
				dependencies = append(dependencies, groupResourceAddress(host.Name, group.Name))
			}
		}
		sort.Strings(dependencies)
		sort.Strings(triggers)
		resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
			Host: host.Name, Address: serviceResourceAddress(host.Name, service.Name), Kind: "service", Managed: true,
			Summary: serviceSummary(service), Source: service.Source, Lifecycle: &service.Lifecycle,
			Desired: map[string]any{
				"name":            service.Name,
				"enabled":         service.Enabled,
				"runlevel":        service.Runlevel,
				"state":           service.State,
				"operation":       service.Operation,
				"package":         service.Package,
				"user":            service.User,
				"group":           service.Group,
				"delete_behavior": "",
				"prevent_destroy": service.Lifecycle.PreventDestroy,
			},
			DependsOn: dependencies, TriggeredBy: triggers, DigestSafe: true,
		})
	}
}

func serviceSummary(service ir.ServiceSpec) string {
	state := service.State
	if service.Enabled {
		return "keep OpenRC service " + service.Name + " " + state + " and enabled in " + service.Runlevel
	}
	return "keep OpenRC service " + service.Name + " " + state + " and disabled in " + service.Runlevel
}

func serviceResourceAddress(host, name string) string {
	return "host." + host + ".services.service[" + strconv.Quote(name) + "]"
}

func appendPackageNodes(resourceGraph *ResourceGraph, host ir.HostSpec, hostAddress string) {
	for _, pkg := range host.Packages {
		dependencies := []string{hostAddress}
		if host.APK != nil && (len(host.APK.Keys) > 0 || len(host.APK.Repositories) > 0 || host.APK.Ownership == "authoritative") {
			dependencies = append(dependencies, hostAddress+".apk.update")
		}
		resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
			Host: host.Name, Address: packageResourceAddress(host.Name, pkg.Name), Kind: "package", Managed: true,
			Summary: packageSummary(pkg), Source: pkg.Source, Lifecycle: &pkg.Lifecycle,
			Desired: map[string]any{
				"name":            pkg.Name,
				"repository":      pkg.RepositoryTag,
				"world_intent":    pkg.WorldIntent,
				"installed":       true,
				"world":           true,
				"ensure":          pkg.Ensure,
				"delete_behavior": "",
				"delete":          map[string]any{"name": pkg.Name},
				"prevent_destroy": pkg.Lifecycle.PreventDestroy,
			},
			DependsOn: dependencies, DigestSafe: true,
		})
	}
}

func packageSummary(pkg ir.PackageSpec) string {
	if pkg.Ensure == "absent" {
		return "explicitly remove APK package " + pkg.Name
	}
	return "install explicit APK world intent " + pkg.WorldIntent
}

func packageResourceAddress(host, name string) string {
	return "host." + host + ".packages.package[" + strconv.Quote(name) + "]"
}

func appendAPKNodes(resourceGraph *ResourceGraph, host ir.HostSpec, hostAddress string) {
	if host.APK == nil {
		return
	}
	apk := host.APK
	keyAddresses := make([]string, 0, len(apk.Keys))
	readiness := make([]Node, 0, len(apk.Keys)+len(apk.Repositories))
	for _, key := range apk.Keys {
		address := apkKeyResourceAddress(host.Name, key.Filename)
		node := Node{
			Host: host.Name, Address: address, Kind: "apk_key", Managed: true,
			Summary: apkKeySummary(key), Source: key.Source, Lifecycle: &key.Lifecycle,
			Desired: map[string]any{
				"filename":        key.Filename,
				"sha256":          key.SHA256,
				"ensure":          key.Ensure,
				"delete_behavior": "",
				"delete":          map[string]any{"filename": key.Filename},
				"prevent_destroy": key.Lifecycle.PreventDestroy,
			},
			Payload:   map[string]any{"content": append([]byte(nil), key.Content...)},
			DependsOn: []string{hostAddress}, DigestSafe: true,
		}
		resourceGraph.Nodes = append(resourceGraph.Nodes, node)
		keyAddresses = append(keyAddresses, address)
		readiness = append(readiness, node)
	}
	sort.Strings(keyAddresses)
	if apk.Ownership == "authoritative" {
		lines := make([]string, 0, len(apk.Repositories))
		for _, repository := range apk.Repositories {
			if repository.Ensure == "present" {
				lines = append(lines, repository.Line)
			}
		}
		node := Node{
			Host: host.Name, Address: hostAddress + ".apk.repositories", Kind: "apk_repositories", Managed: true,
			Summary: "authoritatively manage /etc/apk/repositories", Source: apk.Source,
			Desired: map[string]any{
				"ownership":       "authoritative",
				"lines":           lines,
				"final_newline":   len(lines) > 0,
				"ensure":          "present",
				"delete_behavior": "",
			},
			DependsOn: append([]string{hostAddress}, keyAddresses...), DigestSafe: true,
		}
		resourceGraph.Nodes = append(resourceGraph.Nodes, node)
		readiness = append(readiness, node)
	} else {
		for _, repository := range apk.Repositories {
			node := Node{
				Host: host.Name, Address: apkRepositoryResourceAddress(host.Name, repository.Name), Kind: "apk_repository", Managed: true,
				Summary: apkRepositorySummary(repository), Source: repository.Source, Lifecycle: &repository.Lifecycle,
				Desired: map[string]any{
					"name":            repository.Name,
					"line":            repository.Line,
					"ownership":       "managed",
					"ensure":          repository.Ensure,
					"delete_behavior": "",
					"delete":          map[string]any{"name": repository.Name},
					"prevent_destroy": repository.Lifecycle.PreventDestroy,
				},
				DependsOn: append([]string{hostAddress}, keyAddresses...), DigestSafe: true,
			}
			resourceGraph.Nodes = append(resourceGraph.Nodes, node)
			readiness = append(readiness, node)
		}
	}
	if len(readiness) == 0 {
		return
	}
	dependencies := make([]string, 0, len(readiness))
	for _, node := range readiness {
		dependencies = append(dependencies, node.Address)
	}
	sort.Strings(dependencies)
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte("alpineform-apk-update-v1\x00"+host.Name)))
	resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
		Host: host.Name, Address: hostAddress + ".apk.update", Kind: "apk_update", Managed: true,
		Summary: "refresh APK indexes after repository or key changes", Source: apk.Source,
		Desired: map[string]any{
			"fingerprint":     fingerprint,
			"ensure":          "present",
			"delete_behavior": "",
		},
		Payload: map[string]any{"readiness": readiness}, DependsOn: dependencies, DigestSafe: true,
	})
}

func apkRepositorySummary(repository ir.APKRepositorySpec) string {
	if repository.Ensure == "absent" {
		return "remove managed APK repository " + repository.Name
	}
	return "manage APK repository " + repository.Line
}

func apkKeySummary(key ir.APKKeySpec) string {
	if key.Ensure == "absent" {
		return "remove custom APK key " + key.Filename
	}
	return "manage custom APK key " + key.Filename
}

func apkRepositoryResourceAddress(host, name string) string {
	return "host." + host + ".apk.repository[" + strconv.Quote(name) + "]"
}

func apkKeyResourceAddress(host, filename string) string {
	return "host." + host + ".apk.key[" + strconv.Quote(filename) + "]"
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

func groupSummary(group ir.ManagedGroupSpec) string {
	if group.Ensure == "absent" {
		return "ensure group is absent " + group.Name
	}
	return "manage group " + group.Name
}

func userSummary(user ir.ManagedUserSpec) string {
	if user.Ensure == "absent" {
		return "ensure user is absent " + user.Name
	}
	return "manage user " + user.Name
}

func membershipSummary(user string, membership ir.ManagedMembershipSpec) string {
	if membership.Ensure == "absent" {
		return "ensure supplementary membership is absent " + user + ":" + membership.Group
	}
	return "manage supplementary membership " + user + ":" + membership.Group
}

func authorizedKeySummary(user string, key ir.ManagedAuthorizedKeySpec) string {
	if key.Ensure == "absent" {
		return "ensure authorized key is absent for " + user + " " + key.Fingerprint
	}
	return "manage authorized key for " + user + " " + key.Fingerprint
}

func fileDependencies(host, hostAddress string, file ir.ManagedFileSpec, directories []ir.ManagedDirectorySpec, groups []ir.ManagedGroupSpec, users []ir.ManagedUserSpec) []string {
	dependencies := []string{hostAddress}
	if file.Ensure != "present" {
		return dependencies
	}
	if parent, exists := nearestPresentDirectory(file.Path, directories); exists {
		dependencies = append(dependencies, directoryResourceAddress(host, parent.Path))
	}
	if group, exists := managedGroup(file.Group, groups); exists && group.Ensure == "present" {
		dependencies = append(dependencies, groupResourceAddress(host, group.Name))
	}
	if user, exists := managedUser(file.Owner, users); exists && user.Ensure == "present" {
		dependencies = append(dependencies, userResourceAddress(host, user.Name))
		dependencies = append(dependencies, presentUserChildAddresses(host, user)...)
	}
	sort.Strings(dependencies)
	return dependencies
}

func directoryDependencies(host, hostAddress string, directory ir.ManagedDirectorySpec, directories []ir.ManagedDirectorySpec, files []ir.ManagedFileSpec, groups []ir.ManagedGroupSpec, users []ir.ManagedUserSpec) []string {
	dependencies := []string{hostAddress}
	if directory.Ensure == "present" {
		if parent, exists := nearestPresentDirectory(directory.Path, directories); exists {
			dependencies = append(dependencies, directoryResourceAddress(host, parent.Path))
		}
		if group, exists := managedGroup(directory.Group, groups); exists && group.Ensure == "present" {
			dependencies = append(dependencies, groupResourceAddress(host, group.Name))
		}
		if user, exists := managedUser(directory.Owner, users); exists && user.Ensure == "present" {
			dependencies = append(dependencies, userResourceAddress(host, user.Name))
			dependencies = append(dependencies, presentUserChildAddresses(host, user)...)
		}
		sort.Strings(dependencies)
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

func groupDependencies(host, hostAddress string, group ir.ManagedGroupSpec, directories []ir.ManagedDirectorySpec, files []ir.ManagedFileSpec, users []ir.ManagedUserSpec) []string {
	dependencies := []string{hostAddress}
	if group.Ensure == "present" {
		return dependencies
	}
	for _, directory := range directories {
		if directory.Ensure == "absent" && groupMatchesReference(group, directory.Group) {
			dependencies = append(dependencies, directoryResourceAddress(host, directory.Path))
		}
	}
	for _, file := range files {
		if file.Ensure == "absent" && groupMatchesReference(group, file.Group) {
			dependencies = append(dependencies, fileResourceAddress(host, file.Path))
		}
	}
	for _, user := range users {
		if user.Ensure == "absent" && groupMatchesReference(group, user.PrimaryGroup) {
			dependencies = append(dependencies, userResourceAddress(host, user.Name))
		}
		for _, membership := range user.Groups {
			if membership.Ensure == "absent" && groupMatchesReference(group, membership.Group) {
				dependencies = append(dependencies, membershipResourceAddress(host, user.Name, membership.Group))
			}
		}
	}
	sort.Strings(dependencies)
	return dependencies
}

func userDependencies(host, hostAddress string, user ir.ManagedUserSpec, groups []ir.ManagedGroupSpec, directories []ir.ManagedDirectorySpec, files []ir.ManagedFileSpec) []string {
	dependencies := []string{hostAddress}
	if user.Ensure == "present" {
		if group, exists := managedGroup(user.PrimaryGroup, groups); exists && group.Ensure == "present" {
			dependencies = append(dependencies, groupResourceAddress(host, group.Name))
		}
		sort.Strings(dependencies)
		return dependencies
	}
	for _, directory := range directories {
		if directory.Ensure == "absent" && userMatchesReference(user, directory.Owner) {
			dependencies = append(dependencies, directoryResourceAddress(host, directory.Path))
		}
	}
	for _, file := range files {
		if file.Ensure == "absent" && userMatchesReference(user, file.Owner) {
			dependencies = append(dependencies, fileResourceAddress(host, file.Path))
		}
	}
	for _, membership := range user.Groups {
		if membership.Ensure == "absent" {
			dependencies = append(dependencies, membershipResourceAddress(host, user.Name, membership.Group))
		}
	}
	for _, key := range user.AuthorizedKeys {
		if key.Ensure == "absent" {
			dependencies = append(dependencies, authorizedKeyResourceAddress(host, user.Name, key.Fingerprint))
		}
	}
	sort.Strings(dependencies)
	return dependencies
}

func membershipDependencies(host, hostAddress string, user ir.ManagedUserSpec, membership ir.ManagedMembershipSpec, groups []ir.ManagedGroupSpec) []string {
	dependencies := []string{hostAddress}
	if membership.Ensure != "present" {
		return dependencies
	}
	dependencies = append(dependencies, userResourceAddress(host, user.Name))
	if group, exists := managedGroup(membership.Group, groups); exists && group.Ensure == "present" {
		dependencies = append(dependencies, groupResourceAddress(host, group.Name))
	}
	sort.Strings(dependencies)
	return dependencies
}

func authorizedKeyDependencies(host, hostAddress string, user ir.ManagedUserSpec, key ir.ManagedAuthorizedKeySpec) []string {
	dependencies := []string{hostAddress}
	if key.Ensure == "present" {
		dependencies = append(dependencies, userResourceAddress(host, user.Name))
	}
	return dependencies
}

func presentUserChildAddresses(host string, user ir.ManagedUserSpec) []string {
	addresses := make([]string, 0, len(user.Groups)+len(user.AuthorizedKeys))
	for _, membership := range user.Groups {
		if membership.Ensure == "present" {
			addresses = append(addresses, membershipResourceAddress(host, user.Name, membership.Group))
		}
	}
	for _, key := range user.AuthorizedKeys {
		if key.Ensure == "present" {
			addresses = append(addresses, authorizedKeyResourceAddress(host, user.Name, key.Fingerprint))
		}
	}
	return addresses
}

func managedGroup(reference string, groups []ir.ManagedGroupSpec) (ir.ManagedGroupSpec, bool) {
	for _, group := range groups {
		if groupMatchesReference(group, reference) {
			return group, true
		}
	}
	return ir.ManagedGroupSpec{}, false
}

func groupMatchesReference(group ir.ManagedGroupSpec, reference string) bool {
	return reference == group.Name || (group.GID != "" && reference == group.GID)
}

func managedUser(reference string, users []ir.ManagedUserSpec) (ir.ManagedUserSpec, bool) {
	for _, user := range users {
		if userMatchesReference(user, reference) {
			return user, true
		}
	}
	return ir.ManagedUserSpec{}, false
}

func userMatchesReference(user ir.ManagedUserSpec, reference string) bool {
	return reference == user.Name || (user.UID != "" && reference == user.UID)
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

func groupResourceAddress(host, name string) string {
	return "host." + host + ".groups.group[" + strconv.Quote(name) + "]"
}

func userResourceAddress(host, name string) string {
	return "host." + host + ".users.user[" + strconv.Quote(name) + "]"
}

func membershipResourceAddress(host, user, group string) string {
	return userResourceAddress(host, user) + ".groups.group[" + strconv.Quote(group) + "]"
}

func authorizedKeyResourceAddress(host, user, fingerprint string) string {
	return userResourceAddress(host, user) + ".ssh_authorized_keys.key[" + strconv.Quote(fingerprint) + "]"
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
		for _, trigger := range node.TriggeredBy {
			if _, exists := byAddress[trigger]; !exists {
				return fmt.Errorf("%s:%d:%s: resource %q is triggered by unknown address %q", node.Source.File, node.Source.Line, node.Source.Path, node.Address, trigger)
			}
			if !containsAddress(node.DependsOn, trigger) {
				return fmt.Errorf("%s:%d:%s: resource %q trigger %q must also be a dependency", node.Source.File, node.Source.Line, node.Source.Path, node.Address, trigger)
			}
		}
	}
	_, err := graph.Schedule()
	return err
}

func containsAddress(addresses []string, wanted string) bool {
	for _, address := range addresses {
		if address == wanted {
			return true
		}
	}
	return false
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

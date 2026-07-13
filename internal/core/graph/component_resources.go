package graph

import (
	"sort"
	"strconv"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func appendComponentResourceNodes(resourceGraph *ResourceGraph, host ir.HostSpec, component ir.ComponentInstanceSpec, componentAddress string) {
	for _, group := range component.Groups {
		deleteBehavior := componentDeleteBehavior(group.OnRemove)
		resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
			Host: host.Name, Address: componentGroupAddress(componentAddress, group.Name), Kind: "group", Managed: true,
			Summary: groupSummary(group), Source: group.Source, Lifecycle: &group.Lifecycle,
			Desired: map[string]any{
				"name": group.Name, "gid": group.GID, "system": group.System, "ensure": group.Ensure,
				"delete_behavior": deleteBehavior, "delete": map[string]any{"name": group.Name}, "prevent_destroy": group.Lifecycle.PreventDestroy,
			},
			DependsOn: []string{componentAddress}, DigestSafe: true,
		})
	}
	for _, pkg := range component.Packages {
		dependencies := []string{componentAddress}
		if host.APK != nil && (len(host.APK.Keys) > 0 || len(host.APK.Repositories) > 0 || host.APK.Ownership == "authoritative") {
			dependencies = append(dependencies, "host."+host.Name+".apk.update")
		}
		resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
			Host: host.Name, Address: componentPackageAddress(componentAddress, pkg.Name), Kind: "package", Managed: true,
			Summary: packageSummary(pkg), Source: pkg.Source, Lifecycle: &pkg.Lifecycle,
			Desired: map[string]any{
				"name": pkg.Name, "repository": pkg.RepositoryTag, "world_intent": pkg.WorldIntent,
				"installed": true, "world": true, "ensure": pkg.Ensure, "delete_behavior": "",
				"delete": map[string]any{"name": pkg.Name}, "prevent_destroy": pkg.Lifecycle.PreventDestroy,
			},
			DependsOn: dependencies, DigestSafe: true,
		})
	}
	for _, user := range component.Users {
		dependencies := []string{componentAddress}
		if group, exists := managedGroup(user.PrimaryGroup, component.Groups); exists && group.Ensure == "present" {
			dependencies = append(dependencies, componentGroupAddress(componentAddress, group.Name))
		} else if group, exists := managedGroup(user.PrimaryGroup, host.Groups); exists && group.Ensure == "present" {
			dependencies = append(dependencies, groupResourceAddress(host.Name, group.Name))
		}
		sort.Strings(dependencies)
		resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
			Host: host.Name, Address: componentUserAddress(componentAddress, user.Name), Kind: "user", Managed: true,
			Summary: userSummary(user), Source: user.Source, Lifecycle: &user.Lifecycle,
			Desired: map[string]any{
				"name": user.Name, "uid": user.UID, "group": user.PrimaryGroup, "home": user.Home, "shell": user.Shell,
				"system": user.System, "ensure": user.Ensure, "delete_behavior": componentDeleteBehavior(user.OnRemove),
				"delete": map[string]any{"name": user.Name}, "prevent_destroy": user.Lifecycle.PreventDestroy,
			},
			DependsOn: dependencies, DigestSafe: true,
		})
		for _, membership := range user.Groups {
			membershipDependencies := []string{componentUserAddress(componentAddress, user.Name)}
			if group, exists := managedGroup(membership.Group, component.Groups); exists && group.Ensure == "present" {
				membershipDependencies = append(membershipDependencies, componentGroupAddress(componentAddress, group.Name))
			} else if group, exists := managedGroup(membership.Group, host.Groups); exists && group.Ensure == "present" {
				membershipDependencies = append(membershipDependencies, groupResourceAddress(host.Name, group.Name))
			}
			sort.Strings(membershipDependencies)
			resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
				Host: host.Name, Address: componentMembershipAddress(componentAddress, user.Name, membership.Group), Kind: "membership", Managed: true,
				Summary: membershipSummary(user.Name, membership), Source: membership.Source, Lifecycle: &user.Lifecycle,
				Desired: map[string]any{
					"user": user.Name, "group": membership.Group, "ensure": membership.Ensure, "delete_behavior": "destroy",
					"delete": map[string]any{"user": user.Name, "group": membership.Group}, "prevent_destroy": user.Lifecycle.PreventDestroy,
				},
				DependsOn: membershipDependencies, DigestSafe: true,
			})
		}
		for _, key := range user.AuthorizedKeys {
			resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
				Host: host.Name, Address: componentAuthorizedKeyAddress(componentAddress, user.Name, key.Fingerprint), Kind: "authorized_key", Managed: true,
				Summary: authorizedKeySummary(user.Name, key), Source: key.Source, Lifecycle: &user.Lifecycle,
				Desired: map[string]any{
					"user": user.Name, "fingerprint": key.Fingerprint, "metadata_ok": true, "ensure": key.Ensure,
					"delete_behavior": "destroy", "delete": map[string]any{"user": user.Name, "key_type": key.KeyType, "key_blob": key.KeyBlob},
					"prevent_destroy": user.Lifecycle.PreventDestroy,
				},
				Payload:   map[string]any{"line": key.Line, "key_type": key.KeyType, "key_blob": key.KeyBlob},
				DependsOn: []string{componentUserAddress(componentAddress, user.Name)}, DigestSafe: true,
			})
		}
	}
	for _, directory := range component.Directories {
		dependencies := componentPathDependencies(host, component, componentAddress, directory.Path, directory.Owner, directory.Group)
		resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
			Host: host.Name, Address: componentDirectoryAddress(componentAddress, directory.Path), Kind: "directory", Managed: true,
			Summary: directorySummary(directory), Source: directory.Source, Lifecycle: &directory.Lifecycle,
			Desired: map[string]any{
				"path": directory.Path, "owner": directory.Owner, "group": directory.Group, "mode": directory.Mode,
				"ensure": directory.Ensure, "recursive_delete": directory.RecursiveDelete,
				"delete_behavior": componentDeleteBehavior(directory.OnRemove),
				"delete":          map[string]any{"path": directory.Path, "recursive": directory.RecursiveDelete}, "prevent_destroy": directory.Lifecycle.PreventDestroy,
			},
			DependsOn: dependencies, DigestSafe: true,
		})
	}
	for _, file := range component.Files {
		contentBytes := file.ContentBytes
		if file.ContentWriteOnly {
			contentBytes = 0
		}
		resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
			Host: host.Name, Address: componentFileAddress(componentAddress, file.Path), Kind: "file", Managed: true,
			Summary: fileSummary(file), Source: file.Source, Lifecycle: &file.Lifecycle,
			Desired: map[string]any{
				"path": file.Path, "owner": file.Owner, "group": file.Group, "mode": file.Mode, "ensure": file.Ensure,
				"content_sha256": file.ContentSHA256, "content_bytes": contentBytes, "content_version": file.ContentVersion,
				"content_write_only": file.ContentWriteOnly, "delete_behavior": componentDeleteBehavior(file.OnRemove),
				"delete": map[string]any{"path": file.Path}, "prevent_destroy": file.Lifecycle.PreventDestroy,
			},
			Payload:   map[string]any{"content": file.Content},
			DependsOn: componentPathDependencies(host, component, componentAddress, file.Path, file.Owner, file.Group),
			Sensitive: file.Sensitive, Ephemeral: file.Ephemeral, DigestSafe: true,
		})
	}
	appendComponentServiceNodes(resourceGraph, host, component, componentAddress)
}

func appendComponentServiceNodes(resourceGraph *ResourceGraph, host ir.HostSpec, component ir.ComponentInstanceSpec, componentAddress string) {
	for _, service := range component.Services {
		dependencies := []string{componentAddress}
		triggers := []string{}
		for _, file := range component.Files {
			if file.Ensure == "present" && (file.Path == "/etc/init.d/"+service.Name || file.Path == "/etc/conf.d/"+service.Name) {
				address := componentFileAddress(componentAddress, file.Path)
				dependencies = append(dependencies, address)
				if service.Operation != "" {
					triggers = append(triggers, address)
				}
			}
		}
		if _, exists := managedPackage(service.Package, component.Packages); exists {
			dependencies = append(dependencies, componentPackageAddress(componentAddress, service.Package))
		} else if _, exists := managedPackage(service.Package, host.Packages); exists {
			dependencies = append(dependencies, packageResourceAddress(host.Name, service.Package))
		}
		if _, exists := managedUser(service.User, component.Users); exists {
			dependencies = append(dependencies, componentUserAddress(componentAddress, service.User))
		} else if _, exists := managedUser(service.User, host.Users); exists {
			dependencies = append(dependencies, userResourceAddress(host.Name, service.User))
		}
		if _, exists := managedGroup(service.Group, component.Groups); exists {
			dependencies = append(dependencies, componentGroupAddress(componentAddress, service.Group))
		} else if _, exists := managedGroup(service.Group, host.Groups); exists {
			dependencies = append(dependencies, groupResourceAddress(host.Name, service.Group))
		}
		dependencies = sortedUniqueStrings(dependencies)
		triggers = sortedUniqueStrings(triggers)
		resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
			Host: host.Name, Address: componentServiceAddress(componentAddress, service.Name), Kind: "service", Managed: true,
			Summary: serviceSummary(service), Source: service.Source, Lifecycle: &service.Lifecycle,
			Desired: map[string]any{
				"name": service.Name, "enabled": service.Enabled, "runlevel": service.Runlevel, "state": service.State,
				"operation": service.Operation, "package": service.Package, "user": service.User, "group": service.Group,
				"delete_behavior": "", "prevent_destroy": service.Lifecycle.PreventDestroy,
			},
			DependsOn: dependencies, TriggeredBy: triggers, DigestSafe: true,
		})
	}
}

func componentPathDependencies(host ir.HostSpec, component ir.ComponentInstanceSpec, componentAddress, path, owner, group string) []string {
	dependencies := []string{componentAddress}
	if parent, exists := nearestPresentDirectory(path, component.Directories); exists {
		dependencies = append(dependencies, componentDirectoryAddress(componentAddress, parent.Path))
	} else if parent, exists := nearestPresentDirectory(path, host.Directories); exists {
		dependencies = append(dependencies, directoryResourceAddress(host.Name, parent.Path))
	}
	if managed, exists := managedGroup(group, component.Groups); exists && managed.Ensure == "present" {
		dependencies = append(dependencies, componentGroupAddress(componentAddress, managed.Name))
	} else if managed, exists := managedGroup(group, host.Groups); exists && managed.Ensure == "present" {
		dependencies = append(dependencies, groupResourceAddress(host.Name, managed.Name))
	}
	if managed, exists := managedUser(owner, component.Users); exists && managed.Ensure == "present" {
		dependencies = append(dependencies, componentUserAddress(componentAddress, managed.Name))
	} else if managed, exists := managedUser(owner, host.Users); exists && managed.Ensure == "present" {
		dependencies = append(dependencies, userResourceAddress(host.Name, managed.Name))
	}
	return sortedUniqueStrings(dependencies)
}

func managedPackage(name string, packages []ir.PackageSpec) (ir.PackageSpec, bool) {
	for _, pkg := range packages {
		if pkg.Name == name {
			return pkg, true
		}
	}
	return ir.PackageSpec{}, false
}

func componentDeleteBehavior(value string) string {
	if value == "forget" {
		return ""
	}
	return value
}

func componentFileAddress(prefix, path string) string {
	return prefix + ".files.file[" + strconv.Quote(path) + "]"
}
func componentDirectoryAddress(prefix, path string) string {
	return prefix + ".directories.directory[" + strconv.Quote(path) + "]"
}
func componentGroupAddress(prefix, name string) string {
	return prefix + ".groups.group[" + strconv.Quote(name) + "]"
}
func componentUserAddress(prefix, name string) string {
	return prefix + ".users.user[" + strconv.Quote(name) + "]"
}
func componentMembershipAddress(prefix, user, group string) string {
	return componentUserAddress(prefix, user) + ".groups.group[" + strconv.Quote(group) + "]"
}
func componentAuthorizedKeyAddress(prefix, user, fingerprint string) string {
	return componentUserAddress(prefix, user) + ".ssh_authorized_keys.key[" + strconv.Quote(fingerprint) + "]"
}
func componentPackageAddress(prefix, name string) string {
	return prefix + ".packages.package[" + strconv.Quote(name) + "]"
}
func componentServiceAddress(prefix, name string) string {
	return prefix + ".services.service[" + strconv.Quote(name) + "]"
}

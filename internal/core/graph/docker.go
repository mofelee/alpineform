package graph

import (
	"path/filepath"
	"sort"
	"strconv"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func appendDockerNodes(resourceGraph *ResourceGraph, host ir.HostSpec, hostAddress string) {
	docker := host.Docker
	if docker == nil {
		return
	}
	projectAddresses := make([]string, 0, len(docker.Projects))
	for _, project := range docker.Projects {
		projectAddresses = append(projectAddresses, dockerProjectAddress(host.Name, project.Name))
	}
	sort.Strings(projectAddresses)

	configAddress := ""
	if docker.DaemonConfig != nil || docker.Ensure == "absent" {
		configAddress = dockerDaemonConfigAddress(host.Name)
		config := docker.DaemonConfig
		desired := map[string]any{
			"path": "/etc/docker/daemon.json", "owner": "root", "group": "root", "mode": "0644",
			"ensure": docker.Ensure, "delete_behavior": "",
			"delete": map[string]any{"path": "/etc/docker/daemon.json"}, "prevent_destroy": docker.Lifecycle.PreventDestroy,
		}
		payload := map[string]any{"content": ""}
		sensitive := false
		ephemeral := false
		if config != nil {
			desired["content_sha256"] = config.ContentSHA256
			desired["content_bytes"] = config.ContentBytes
			desired["content_version"] = config.ContentVersion
			desired["content_write_only"] = config.ContentWriteOnly
			payload["content"] = config.Content
			sensitive = config.Sensitive
			ephemeral = config.Ephemeral
		}
		dependencies := []string{hostAddress}
		if docker.Ensure == "present" && docker.PackageSource != "none" {
			dependencies = append(dependencies, packageResourceAddress(host.Name, "docker"))
		}
		if docker.Ensure == "absent" {
			dependencies = append(dependencies, dockerServiceAddress(host.Name))
		}
		sort.Strings(dependencies)
		resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
			Host: host.Name, Address: configAddress, Kind: "docker_daemon_config", Managed: true,
			Summary: "manage validated Docker daemon configuration", Source: docker.Source, Lifecycle: &docker.Lifecycle,
			Desired: desired, Payload: payload, DependsOn: dependencies,
			Sensitive: sensitive, Ephemeral: ephemeral, DigestSafe: true,
		})
	}

	serviceDependencies := []string{hostAddress}
	serviceTriggers := []string{}
	if docker.Ensure == "present" {
		if docker.PackageSource != "none" {
			serviceDependencies = append(serviceDependencies, packageResourceAddress(host.Name, "docker"))
		}
		if configAddress != "" {
			serviceDependencies = append(serviceDependencies, configAddress)
			serviceTriggers = append(serviceTriggers, configAddress)
		}
	} else {
		serviceDependencies = append(serviceDependencies, projectAddresses...)
	}
	sort.Strings(serviceDependencies)
	resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
		Host: host.Name, Address: dockerServiceAddress(host.Name), Kind: "docker_service", Managed: true,
		Summary: "manage Docker OpenRC service", Source: docker.Source, Lifecycle: &docker.Lifecycle,
		Desired: map[string]any{
			"name": "docker", "enabled": docker.Enabled, "runlevel": "default", "state": dockerServiceState(docker),
			"operation": "restarted", "package": "docker", "user": "", "group": "docker", "ensure": docker.Ensure,
			"delete_behavior": "", "delete": map[string]any{"name": "docker", "runlevel": "default"},
			"prevent_destroy": docker.Lifecycle.PreventDestroy,
		},
		DependsOn: serviceDependencies, TriggeredBy: serviceTriggers, DigestSafe: true,
	})

	for _, member := range docker.Members {
		dependencies := []string{hostAddress}
		if docker.Ensure == "present" {
			dependencies = append(dependencies, groupResourceAddress(host.Name, "docker"))
			if user, found := managedUser(member, host.Users); found && user.Ensure == "present" {
				dependencies = append(dependencies, userResourceAddress(host.Name, user.Name))
			}
			for _, component := range host.Components {
				if user, found := managedUser(member, component.Users); found && user.Ensure == "present" {
					componentAddress := "host." + host.Name + ".component." + component.Name
					dependencies = append(dependencies, componentUserAddress(componentAddress, user.Name))
				}
			}
		}
		sort.Strings(dependencies)
		resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
			Host: host.Name, Address: dockerMembershipAddress(host.Name, member), Kind: "membership", Managed: true,
			Summary: "manage Docker group membership for " + member, Source: docker.Source, Lifecycle: &docker.Lifecycle,
			Desired: map[string]any{
				"user": member, "group": "docker", "ensure": docker.Ensure, "delete_behavior": "",
				"delete": map[string]any{"user": member, "group": "docker"}, "prevent_destroy": docker.Lifecycle.PreventDestroy,
			},
			DependsOn: dependencies, DigestSafe: true,
		})
	}

	for _, project := range docker.Projects {
		ensure := "present"
		if project.State == "absent" {
			ensure = "absent"
		}
		deleteBehavior := ""
		if project.OnRemove == "destroy" {
			deleteBehavior = "destroy"
		}
		composeBytes := project.ComposeBytes
		envBytes := project.EnvBytes
		if project.ComposeWriteOnly {
			composeBytes = 0
		}
		if project.EnvWriteOnly {
			envBytes = 0
		}
		envOwner := ""
		envGroup := ""
		envMode := ""
		if project.HasEnv {
			envOwner = "root"
			envGroup = "root"
			envMode = "0600"
		}
		dependencies := []string{hostAddress}
		if docker.Ensure == "present" {
			dependencies = append(dependencies, dockerServiceAddress(host.Name))
			if docker.PackageSource != "none" {
				dependencies = append(dependencies, packageResourceAddress(host.Name, "docker-cli-compose"))
			}
		}
		sort.Strings(dependencies)
		resourceGraph.Nodes = append(resourceGraph.Nodes, Node{
			Host: host.Name, Address: dockerProjectAddress(host.Name, project.Name), Kind: "docker_compose_project", Managed: true,
			Summary: "keep Docker Compose project " + project.Name + " " + project.State, Source: project.Source, Lifecycle: &project.Lifecycle,
			Desired: map[string]any{
				"name": project.Name, "directory": project.Directory, "compose_path": filepath.Join(project.Directory, "compose.yaml"),
				"env_path": filepath.Join(project.Directory, ".env"), "has_env": project.HasEnv, "state": project.State,
				"directory_owner": "root", "directory_group": "root", "directory_mode": "0755",
				"compose_owner": "root", "compose_group": "root", "compose_mode": "0600",
				"env_owner": envOwner, "env_group": envGroup, "env_mode": envMode,
				"compose_sha256": project.ComposeSHA256, "compose_bytes": composeBytes, "compose_version": project.ComposeVersion,
				"compose_write_only": project.ComposeWriteOnly, "env_sha256": project.EnvSHA256, "env_bytes": envBytes,
				"env_version": project.EnvVersion, "env_write_only": project.EnvWriteOnly,
				"content_write_only": project.ComposeWriteOnly || project.EnvWriteOnly, "ensure": ensure,
				"delete_behavior": deleteBehavior, "prevent_destroy": project.Lifecycle.PreventDestroy,
				"delete": map[string]any{
					"name": project.Name, "directory": project.Directory, "compose_path": filepath.Join(project.Directory, "compose.yaml"),
					"env_path": filepath.Join(project.Directory, ".env"), "has_env": project.HasEnv,
				},
			},
			Payload: map[string]any{"compose": project.Compose, "env": project.Env}, DependsOn: dependencies,
			Sensitive: project.Sensitive, Ephemeral: project.Ephemeral, DigestSafe: true,
		})
	}
}

func dockerServiceState(docker *ir.DockerSpec) string {
	if docker.Ensure == "present" && docker.Enabled {
		return "running"
	}
	return "stopped"
}

func dockerServiceAddress(host string) string {
	return "host." + host + ".docker.service"
}

func dockerDaemonConfigAddress(host string) string {
	return "host." + host + ".docker.daemon_config"
}

func dockerMembershipAddress(host, user string) string {
	return "host." + host + ".docker.membership[" + strconv.Quote(user) + "]"
}

func dockerProjectAddress(host, name string) string {
	return "host." + host + ".docker.project[" + strconv.Quote(name) + "]"
}

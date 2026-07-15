package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
)

type RunnerFactory func(host string) (backend.Runner, error)

type Native struct {
	NewRunner        RunnerFactory
	NewNFTablesToken func() (string, error)
	NFTablesNow      func() time.Time
	NFTablesWait     func(context.Context, time.Duration) error
}

func (provider Native) Inspect(ctx context.Context, node graph.Node) (engine.ObservedResource, error) {
	runner, err := provider.runner(node.Host)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	switch node.Kind {
	case "file":
		return inspectFile(ctx, runner, node)
	case "directory":
		return inspectDirectory(ctx, runner, node)
	case "group":
		return inspectGroup(ctx, runner, node)
	case "user":
		return inspectUser(ctx, runner, node)
	case "membership":
		return inspectMembership(ctx, runner, node)
	case "authorized_key":
		return inspectAuthorizedKey(ctx, runner, node)
	case "apk_key":
		return inspectAPKKey(ctx, runner, node)
	case "apk_repository", "apk_repositories":
		return inspectAPKRepository(ctx, runner, node)
	case "apk_update":
		return inspectAPKUpdate(ctx, runner, node)
	case "package":
		return inspectPackage(ctx, runner, node)
	case "service":
		return inspectService(ctx, runner, node)
	case "docker_service":
		return inspectService(ctx, runner, node)
	case "docker_daemon_config":
		return inspectDockerDaemonConfig(ctx, runner, node)
	case "docker_compose_project":
		return inspectDockerComposeProject(ctx, runner, node)
	case "system_hostname":
		return inspectSystemHostname(ctx, runner, node)
	case "system_timezone":
		return inspectSystemTimezone(ctx, runner, node)
	case "kernel_module":
		return inspectKernelModule(ctx, runner, node)
	case "sysctl":
		return inspectSysctl(ctx, runner, node)
	case "sysctl_runtime":
		return inspectSysctlRuntime(node)
	case "nftables_table":
		return inspectNftablesPersistence(ctx, runner, node)
	case "nftables_service":
		return inspectNftablesService(ctx, runner, node)
	case "component_artifact_source":
		return inspectComponentSource(ctx, runner, node)
	case "component_binary", "component_file":
		return inspectComponentInstall(ctx, runner, node)
	case "component_ca_certificate":
		return inspectComponentCACertificate(ctx, runner, node)
	case "component_archive":
		return inspectComponentArchive(ctx, runner, node)
	case "component_script":
		return inspectComponentScript(ctx, runner, node)
	case "component_build_input":
		return inspectComponentBuildInput(ctx, runner, node)
	case "component_build_dependencies":
		return inspectComponentBuildDependencies(ctx, runner, node)
	case "component_build_workspace":
		return inspectComponentBuildWorkspace(ctx, runner, node)
	case "component_build_output":
		return inspectComponentBuildOutput(ctx, runner, node)
	case "component_build_cleanup":
		return inspectComponentBuildCleanup(ctx, runner, node)
	case "component_build_install":
		return inspectComponentBuildInstall(ctx, runner, node)
	default:
		return engine.ObservedResource{}, fmt.Errorf("no Alpine provider is registered for resource kind %q", node.Kind)
	}
}

func (provider Native) Apply(ctx context.Context, step engine.Step) (engine.ObservedResource, error) {
	runner, err := provider.runner(step.Host)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	switch step.Node.Kind {
	case "file":
		return applyFile(ctx, runner, step.Node)
	case "directory":
		return applyDirectory(ctx, runner, step.Node)
	case "group":
		return applyGroup(ctx, runner, step.Node)
	case "user":
		return applyUser(ctx, runner, step.Node)
	case "membership":
		return applyMembership(ctx, runner, step.Node)
	case "authorized_key":
		return applyAuthorizedKey(ctx, runner, step.Node)
	case "apk_key":
		return applyAPKKey(ctx, runner, step.Node)
	case "apk_repository", "apk_repositories":
		return applyAPKRepository(ctx, runner, step.Node)
	case "apk_update":
		return applyAPKUpdate(ctx, runner, step.Node)
	case "package":
		return applyPackage(ctx, runner, step.Node)
	case "service":
		return applyService(ctx, runner, step)
	case "docker_service":
		return applyDockerService(ctx, runner, step)
	case "docker_daemon_config":
		return applyDockerDaemonConfig(ctx, runner, step.Node)
	case "docker_compose_project":
		return applyDockerComposeProject(ctx, runner, step.Node)
	case "system_hostname":
		return applySystemHostname(ctx, runner, step.Node)
	case "system_timezone":
		return applySystemTimezone(ctx, runner, step.Node)
	case "kernel_module":
		return applyKernelModule(ctx, runner, step.Node)
	case "sysctl":
		return applySysctl(ctx, runner, step.Node)
	case "sysctl_runtime":
		return applySysctlRuntime(ctx, runner, step.Node)
	case "nftables_table":
		return applyNftablesTransaction(ctx, runner, func() (backend.Runner, error) {
			return provider.runner(step.Host)
		}, step, nftablesTransactionRuntime{
			NewToken: provider.NewNFTablesToken,
			Now:      provider.NFTablesNow,
			Wait:     provider.NFTablesWait,
		})
	case "nftables_service":
		return applyNftablesService(ctx, runner, step.Node)
	case "component_artifact_source":
		return applyComponentSource(ctx, runner, step.Node)
	case "component_binary", "component_file":
		return applyComponentInstall(ctx, runner, step.Node)
	case "component_ca_certificate":
		return applyComponentCACertificate(ctx, runner, step.Node)
	case "component_archive":
		return applyComponentArchive(ctx, runner, step.Node)
	case "component_script":
		return applyComponentScript(ctx, runner, step)
	case "component_build_input":
		return applyComponentBuildInput(ctx, runner, step)
	case "component_build_dependencies":
		return applyComponentBuildDependencies(ctx, runner, step.Node)
	case "component_build_workspace":
		return applyComponentBuildWorkspace(ctx, runner, step.Node)
	case "component_build_output":
		return applyComponentBuildOutput(ctx, runner, step.Node)
	case "component_build_cleanup":
		return applyComponentBuildCleanup(ctx, runner, step.Node)
	case "component_build_install":
		return applyComponentBuildInstall(ctx, runner, step.Node)
	default:
		return engine.ObservedResource{}, fmt.Errorf("no Alpine provider is registered for resource kind %q", step.Node.Kind)
	}
}

func (provider Native) Delete(ctx context.Context, step engine.Step) error {
	runner, err := provider.runner(step.Host)
	if err != nil {
		return err
	}
	kind := step.Node.Kind
	if kind == "" && step.Prior != nil {
		kind = step.Prior.Kind
	}
	switch kind {
	case "file":
		return deleteFile(ctx, runner, step)
	case "directory":
		return deleteDirectory(ctx, runner, step)
	case "group":
		return deleteGroup(ctx, runner, step)
	case "user":
		return deleteUser(ctx, runner, step)
	case "membership":
		return deleteMembership(ctx, runner, step)
	case "authorized_key":
		return deleteAuthorizedKey(ctx, runner, step)
	case "apk_key":
		return deleteAPKKey(ctx, runner, step)
	case "apk_repository":
		return deleteAPKRepository(ctx, runner, step)
	case "apk_repositories", "apk_update":
		return fmt.Errorf("resource kind %q can only be forgotten when its declaration is removed", kind)
	case "package":
		return deletePackage(ctx, runner, step)
	case "service":
		return fmt.Errorf("OpenRC service declarations can only be forgotten; disable or stop the service explicitly first")
	case "docker_service":
		return deleteDockerService(ctx, runner, step)
	case "docker_daemon_config":
		return deleteDockerDaemonConfig(ctx, runner, step)
	case "docker_compose_project":
		return deleteDockerComposeProject(ctx, runner, step)
	case "system_hostname", "system_timezone":
		return fmt.Errorf("system declarations can only be forgotten when removed")
	case "kernel_module", "sysctl_runtime":
		return fmt.Errorf("resource kind %q can only be forgotten when removed", kind)
	case "sysctl":
		return deleteSysctl(ctx, runner, step)
	case "nftables_table":
		return deleteNftablesTransaction(ctx, runner, func() (backend.Runner, error) {
			return provider.runner(step.Host)
		}, step, nftablesTransactionRuntime{
			NewToken: provider.NewNFTablesToken,
			Now:      provider.NFTablesNow,
			Wait:     provider.NFTablesWait,
		})
	case "nftables_service":
		return fmt.Errorf("AlpineForm nftables service declarations can only be forgotten")
	case "component_artifact_source":
		return deleteComponentSource(ctx, runner, step)
	case "component_binary", "component_file":
		return deleteComponentInstall(ctx, runner, step)
	case "component_ca_certificate":
		return deleteComponentCACertificate(ctx, runner, step)
	case "component_archive":
		return deleteComponentArchive(ctx, runner, step)
	case "component_script":
		return fmt.Errorf("component scripts can only be forgotten when their declaration is removed")
	case "component_build_input", "component_build_dependencies", "component_build_workspace", "component_build_output", "component_build_cleanup", "component_build_install":
		return deleteComponentBuildResource(ctx, runner, step)
	default:
		return fmt.Errorf("no Alpine provider is registered for resource kind %q", kind)
	}
}

func (provider Native) runner(host string) (backend.Runner, error) {
	if provider.NewRunner == nil {
		return nil, fmt.Errorf("Alpine native provider requires a runner factory")
	}
	runner, err := provider.NewRunner(host)
	if err != nil {
		return nil, fmt.Errorf("create Alpine provider runner for host %q: %w", host, err)
	}
	if runner == nil {
		return nil, fmt.Errorf("Alpine provider runner factory returned nil for host %q", host)
	}
	return runner, nil
}

package merge

import (
	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
)

func compileService(declaration parser.ResourceDeclaration, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (ir.ServiceSpec, error) {
	name := declaration.Label
	if !openRCNamePattern.MatchString(name) {
		return ir.ServiceSpec{}, resourceError(declaration.Source, "service name %q is invalid", name)
	}
	enabled, err := resourceBoolDefault(declaration, "enabled", true, host, facts, ctx)
	if err != nil {
		return ir.ServiceSpec{}, err
	}
	runlevel, err := resourceStringDefault(declaration, "runlevel", "default", host, facts, ctx)
	if err != nil {
		return ir.ServiceSpec{}, err
	}
	if !openRCNamePattern.MatchString(runlevel) {
		return ir.ServiceSpec{}, resourceAttributeError(declaration, "runlevel", "must be a valid existing OpenRC runlevel name")
	}
	state, err := resourceStringDefault(declaration, "state", "running", host, facts, ctx)
	if err != nil {
		return ir.ServiceSpec{}, err
	}
	if state != "running" && state != "stopped" {
		return ir.ServiceSpec{}, resourceAttributeError(declaration, "state", "must be \"running\" or \"stopped\"")
	}
	dependencies := map[string]string{}
	for _, attribute := range []string{"package", "user", "group"} {
		value, err := resourceStringDefault(declaration, attribute, "", host, facts, ctx)
		if err != nil {
			return ir.ServiceSpec{}, err
		}
		if value == "" {
			continue
		}
		if attribute == "package" && !apkPackageNamePattern.MatchString(value) {
			return ir.ServiceSpec{}, resourceAttributeError(declaration, attribute, "must be a valid declared package name")
		}
		if attribute != "package" && !accountNamePattern.MatchString(value) {
			return ir.ServiceSpec{}, resourceAttributeError(declaration, attribute, "must be a valid declared Alpine account name")
		}
		dependencies[attribute] = value
	}
	return ir.ServiceSpec{
		Name: name, Enabled: enabled, Runlevel: runlevel, State: state,
		Package: dependencies["package"], User: dependencies["user"], Group: dependencies["group"],
		Lifecycle: ir.LifecycleSpec{PreventDestroy: declaration.Lifecycle.PreventDestroy, Source: declaration.Lifecycle.Source}, Source: declaration.Source,
	}, nil
}

func resolveAndValidateServiceDependencies(services []ir.ServiceSpec, openrc []ir.OpenRCServiceSpec, packages []ir.PackageSpec, users []ir.ManagedUserSpec, groups []ir.ManagedGroupSpec) error {
	for index := range services {
		service := &services[index]
		if service.User == "" {
			for _, generated := range openrc {
				if generated.Name == service.Name && generated.CommandUser != "" {
					if user, found := managedUserForReference(generated.CommandUser, users); found && user.Ensure == "present" {
						service.User = generated.CommandUser
					}
					break
				}
			}
		}
		if service.Package != "" {
			found := false
			for _, pkg := range packages {
				if pkg.Name == service.Package && pkg.Ensure == "present" {
					found = true
					break
				}
			}
			if !found {
				return resourceError(service.Source, "service %q references package %q that is not declared present", service.Name, service.Package)
			}
		}
		if service.User != "" {
			user, found := managedUserForReference(service.User, users)
			if !found || user.Ensure != "present" {
				return resourceError(service.Source, "service %q references user %q that is not declared present", service.Name, service.User)
			}
		}
		if service.Group != "" {
			group, found := managedGroupForReference(service.Group, groups)
			if !found || group.Ensure != "present" {
				return resourceError(service.Source, "service %q references group %q that is not declared present", service.Name, service.Group)
			}
		}
	}
	return nil
}

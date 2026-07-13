package merge

import (
	"fmt"

	"github.com/mofelee/alpineform/internal/core/ir"
)

func validateComponentResourceCollisions(host ir.HostSpec) error {
	type owner struct {
		kind   string
		name   string
		source ir.SourceRef
	}
	seen := map[string]owner{}
	add := func(kind, name string, source ir.SourceRef) error {
		key := kind + "\x00" + name
		if previous, exists := seen[key]; exists {
			return fmt.Errorf("%s:%d:%s: component %s %q conflicts with %s declared at %s:%d:%s", source.File, source.Line, source.Path, kind, name, previous.kind, previous.source.File, previous.source.Line, previous.source.Path)
		}
		seen[key] = owner{kind: kind, name: name, source: source}
		return nil
	}
	for _, file := range host.Files {
		if err := add("path", file.Path, file.Source); err != nil {
			return err
		}
	}
	for _, directory := range host.Directories {
		if err := add("path", directory.Path, directory.Source); err != nil {
			return err
		}
	}
	for _, group := range host.Groups {
		if err := add("group", group.Name, group.Source); err != nil {
			return err
		}
	}
	for _, user := range host.Users {
		if err := add("user", user.Name, user.Source); err != nil {
			return err
		}
	}
	for _, pkg := range host.Packages {
		if err := add("package", pkg.Name, pkg.Source); err != nil {
			return err
		}
	}
	for _, service := range host.Services {
		if err := add("service", service.Name, service.Source); err != nil {
			return err
		}
	}
	for _, component := range host.Components {
		if component.Install != nil {
			if err := add("path", component.Install.Path, component.Install.Source); err != nil {
				return err
			}
		}
		for _, file := range component.Files {
			if err := add("path", file.Path, file.Source); err != nil {
				return err
			}
		}
		for _, directory := range component.Directories {
			if err := add("path", directory.Path, directory.Source); err != nil {
				return err
			}
		}
		for _, group := range component.Groups {
			if err := add("group", group.Name, group.Source); err != nil {
				return err
			}
		}
		for _, user := range component.Users {
			if err := add("user", user.Name, user.Source); err != nil {
				return err
			}
		}
		for _, pkg := range component.Packages {
			if err := add("package", pkg.Name, pkg.Source); err != nil {
				return err
			}
		}
		for _, service := range component.Services {
			if err := add("service", service.Name, service.Source); err != nil {
				return err
			}
		}
	}
	return nil
}

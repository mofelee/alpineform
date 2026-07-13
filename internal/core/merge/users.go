package merge

import (
	"path/filepath"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
)

func compileUser(declaration parser.ResourceDeclaration, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (ir.ManagedUserSpec, error) {
	name := declaration.Label
	if !managedAccountNamePattern.MatchString(name) || name == "root" {
		return ir.ManagedUserSpec{}, resourceError(declaration.Source, "user name %q must be a valid non-root Alpine account name", name)
	}
	uid, err := resourceNumericID(declaration, "uid", host, facts, ctx)
	if err != nil {
		return ir.ManagedUserSpec{}, err
	}
	if uid == "0" {
		return ir.ManagedUserSpec{}, resourceAttributeError(declaration, "uid", "must be between 1 and 2147483647; uid 0 is reserved")
	}
	primaryGroup, err := resourceStringDefault(declaration, "group", "", host, facts, ctx)
	if err != nil {
		return ir.ManagedUserSpec{}, err
	}
	if primaryGroup != "" && !validAccountReference(primaryGroup) {
		return ir.ManagedUserSpec{}, resourceAttributeError(declaration, "group", "must be a valid Alpine group name or numeric ID")
	}
	home, err := resourceStringDefault(declaration, "home", "", host, facts, ctx)
	if err != nil {
		return ir.ManagedUserSpec{}, err
	}
	if home != "" && (!filepath.IsAbs(home) || filepath.Clean(home) != home || home == "/") {
		return ir.ManagedUserSpec{}, resourceAttributeError(declaration, "home", "must be a clean absolute non-root path")
	}
	shell, err := resourceStringDefault(declaration, "shell", "", host, facts, ctx)
	if err != nil {
		return ir.ManagedUserSpec{}, err
	}
	if shell != "" && (!filepath.IsAbs(shell) || filepath.Clean(shell) != shell) {
		return ir.ManagedUserSpec{}, resourceAttributeError(declaration, "shell", "must be a clean absolute path")
	}
	system, err := resourceBoolDefault(declaration, "system", false, host, facts, ctx)
	if err != nil {
		return ir.ManagedUserSpec{}, err
	}
	ensure, err := resourceStringDefault(declaration, "ensure", "present", host, facts, ctx)
	if err != nil {
		return ir.ManagedUserSpec{}, err
	}
	if ensure != "present" && ensure != "absent" {
		return ir.ManagedUserSpec{}, resourceAttributeError(declaration, "ensure", "must be \"present\" or \"absent\"")
	}
	onRemove, err := resourceStringDefault(declaration, "on_remove", "forget", host, facts, ctx)
	if err != nil {
		return ir.ManagedUserSpec{}, err
	}
	if onRemove != "forget" && onRemove != "destroy" {
		return ir.ManagedUserSpec{}, resourceAttributeError(declaration, "on_remove", "must be \"forget\" or \"destroy\"")
	}
	return ir.ManagedUserSpec{
		Name:         name,
		UID:          uid,
		PrimaryGroup: primaryGroup,
		Home:         home,
		Shell:        shell,
		System:       system,
		Ensure:       ensure,
		OnRemove:     onRemove,
		Lifecycle:    ir.LifecycleSpec{PreventDestroy: declaration.Lifecycle.PreventDestroy, Source: declaration.Lifecycle.Source},
		Source:       declaration.Source,
	}, nil
}

func validAccountReference(value string) bool {
	if managedAccountNamePattern.MatchString(value) {
		return true
	}
	return validateNumericIDString(value)
}

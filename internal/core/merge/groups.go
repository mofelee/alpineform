package merge

import (
	"regexp"
	"strconv"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
)

var managedAccountNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func compileGroup(declaration parser.ResourceDeclaration, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (ir.ManagedGroupSpec, error) {
	if !managedAccountNamePattern.MatchString(declaration.Label) {
		return ir.ManagedGroupSpec{}, resourceError(declaration.Source, "group name %q must be a valid Alpine account name", declaration.Label)
	}
	gid, err := resourceNumericID(declaration, "gid", host, facts, ctx)
	if err != nil {
		return ir.ManagedGroupSpec{}, err
	}
	system, err := resourceBoolDefault(declaration, "system", false, host, facts, ctx)
	if err != nil {
		return ir.ManagedGroupSpec{}, err
	}
	ensure, err := resourceStringDefault(declaration, "ensure", "present", host, facts, ctx)
	if err != nil {
		return ir.ManagedGroupSpec{}, err
	}
	if ensure != "present" && ensure != "absent" {
		return ir.ManagedGroupSpec{}, resourceAttributeError(declaration, "ensure", "must be \"present\" or \"absent\"")
	}
	onRemove, err := resourceStringDefault(declaration, "on_remove", "forget", host, facts, ctx)
	if err != nil {
		return ir.ManagedGroupSpec{}, err
	}
	if onRemove != "forget" && onRemove != "destroy" {
		return ir.ManagedGroupSpec{}, resourceAttributeError(declaration, "on_remove", "must be \"forget\" or \"destroy\"")
	}
	return ir.ManagedGroupSpec{
		Name:      declaration.Label,
		GID:       gid,
		System:    system,
		Ensure:    ensure,
		OnRemove:  onRemove,
		Lifecycle: ir.LifecycleSpec{PreventDestroy: declaration.Lifecycle.PreventDestroy, Source: declaration.Lifecycle.Source},
		Source:    declaration.Source,
	}, nil
}

func resourceNumericID(declaration parser.ResourceDeclaration, name string, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (string, error) {
	value, exists, err := resourceValue(declaration, name, host, facts, ctx)
	if err != nil || !exists {
		return "", err
	}
	if value.Kind != parser.KindNumber || value.ContainsSensitive() || value.ContainsEphemeral() {
		return "", resourceAttributeError(declaration, name, "must evaluate to a non-protected integer")
	}
	id, err := strconv.ParseUint(value.Number, 10, 31)
	if err != nil {
		return "", resourceAttributeError(declaration, name, "must be an integer between 0 and 2147483647")
	}
	return strconv.FormatUint(id, 10), nil
}

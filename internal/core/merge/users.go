package merge

import (
	"path/filepath"
	"strings"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
	"golang.org/x/crypto/ssh"
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
	ensure, err := resourceStringDefault(declaration, "ensure", "present", host, facts, ctx)
	if err != nil {
		return ir.ManagedUserSpec{}, err
	}
	if ensure != "present" && ensure != "absent" {
		return ir.ManagedUserSpec{}, resourceAttributeError(declaration, "ensure", "must be \"present\" or \"absent\"")
	}
	groups, err := compileSupplementaryGroups(declaration, primaryGroup, ensure, host, facts, ctx)
	if err != nil {
		return ir.ManagedUserSpec{}, err
	}
	authorizedKeys, err := compileAuthorizedKeys(declaration, ensure, host, facts, ctx)
	if err != nil {
		return ir.ManagedUserSpec{}, err
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
	onRemove, err := resourceStringDefault(declaration, "on_remove", "forget", host, facts, ctx)
	if err != nil {
		return ir.ManagedUserSpec{}, err
	}
	if onRemove != "forget" && onRemove != "destroy" {
		return ir.ManagedUserSpec{}, resourceAttributeError(declaration, "on_remove", "must be \"forget\" or \"destroy\"")
	}
	return ir.ManagedUserSpec{
		Name:           name,
		UID:            uid,
		PrimaryGroup:   primaryGroup,
		Groups:         groups,
		AuthorizedKeys: authorizedKeys,
		Home:           home,
		Shell:          shell,
		System:         system,
		Ensure:         ensure,
		OnRemove:       onRemove,
		Lifecycle:      ir.LifecycleSpec{PreventDestroy: declaration.Lifecycle.PreventDestroy, Source: declaration.Lifecycle.Source},
		Source:         declaration.Source,
	}, nil
}

func compileSupplementaryGroups(declaration parser.ResourceDeclaration, primaryGroup, ensure string, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) ([]ir.ManagedMembershipSpec, error) {
	values, err := resourceStringList(declaration, "groups", host, facts, ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]ir.ManagedMembershipSpec, 0, len(values))
	for _, value := range values {
		if !managedAccountNamePattern.MatchString(value.String) {
			return nil, resourceAttributeError(declaration, "groups", "must contain valid Alpine group names")
		}
		if value.String == primaryGroup {
			return nil, resourceAttributeError(declaration, "groups", "must not contain the primary group %q", primaryGroup)
		}
		if seen[value.String] {
			continue
		}
		seen[value.String] = true
		out = append(out, ir.ManagedMembershipSpec{Group: value.String, Ensure: ensure, Source: value.Source})
	}
	return out, nil
}

func compileAuthorizedKeys(declaration parser.ResourceDeclaration, ensure string, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) ([]ir.ManagedAuthorizedKeySpec, error) {
	values, err := resourceStringList(declaration, "ssh_authorized_keys", host, facts, ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]ir.ManagedAuthorizedKeySpec, 0, len(values))
	for _, value := range values {
		publicKey, comment, options, rest, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(value.String)))
		if err != nil || len(rest) != 0 {
			return nil, resourceAttributeError(declaration, "ssh_authorized_keys", "contains an invalid SSH public key")
		}
		if len(options) != 0 {
			return nil, resourceAttributeError(declaration, "ssh_authorized_keys", "does not support authorized_keys options in v0.1")
		}
		fields := strings.Fields(strings.TrimSpace(string(ssh.MarshalAuthorizedKey(publicKey))))
		if len(fields) != 2 {
			return nil, resourceAttributeError(declaration, "ssh_authorized_keys", "contains an unsupported SSH public key")
		}
		fingerprint := ssh.FingerprintSHA256(publicKey)
		if seen[fingerprint] {
			continue
		}
		seen[fingerprint] = true
		line := strings.Join(fields, " ")
		if comment = strings.TrimSpace(comment); comment != "" {
			line += " " + comment
		}
		out = append(out, ir.ManagedAuthorizedKeySpec{
			Line: line, KeyType: fields[0], KeyBlob: fields[1], Fingerprint: fingerprint, Ensure: ensure, Source: value.Source,
		})
	}
	return out, nil
}

func resourceStringList(declaration parser.ResourceDeclaration, name string, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) ([]parser.Value, error) {
	value, exists, err := resourceValue(declaration, name, host, facts, ctx)
	if err != nil || !exists {
		return nil, err
	}
	if value.Kind != parser.KindList || value.ContainsSensitive() || value.ContainsEphemeral() {
		return nil, resourceAttributeError(declaration, name, "must evaluate to a non-protected list of strings")
	}
	for _, item := range value.List {
		if item.Kind != parser.KindString || strings.TrimSpace(item.String) == "" {
			return nil, resourceAttributeError(declaration, name, "must contain non-empty strings")
		}
	}
	return value.List, nil
}

func validAccountReference(value string) bool {
	if managedAccountNamePattern.MatchString(value) {
		return true
	}
	return validateNumericIDString(value)
}

package merge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
)

var accountNamePattern = regexp.MustCompile(`^(?:[a-z_][a-z0-9_-]{0,31}|[0-9]{1,10})$`)

func compileHostFiles(host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) ([]ir.ManagedFileSpec, error) {
	files := make([]ir.ManagedFileSpec, 0)
	for _, declaration := range host.Resources {
		if declaration.Kind != parser.ResourceFile {
			return nil, fmt.Errorf("%s:%d:%s: unsupported compiled host resource kind %q", declaration.Source.File, declaration.Source.Line, declaration.Source.Path, declaration.Kind)
		}
		file, err := compileFile(declaration, host, facts, ctx)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func compileFile(declaration parser.ResourceDeclaration, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (ir.ManagedFileSpec, error) {
	path := declaration.Label
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return ir.ManagedFileSpec{}, resourceError(declaration.Source, "file path %q must be a clean absolute non-root path", path)
	}
	ensure, err := resourceStringDefault(declaration, "ensure", "present", host, facts, ctx)
	if err != nil {
		return ir.ManagedFileSpec{}, err
	}
	if ensure != "present" && ensure != "absent" {
		return ir.ManagedFileSpec{}, resourceAttributeError(declaration, "ensure", "must be \"present\" or \"absent\"")
	}
	onRemove, err := resourceStringDefault(declaration, "on_remove", "forget", host, facts, ctx)
	if err != nil {
		return ir.ManagedFileSpec{}, err
	}
	if onRemove != "forget" && onRemove != "destroy" {
		return ir.ManagedFileSpec{}, resourceAttributeError(declaration, "on_remove", "must be \"forget\" or \"destroy\"")
	}
	owner, err := resourceStringDefault(declaration, "owner", "root", host, facts, ctx)
	if err != nil {
		return ir.ManagedFileSpec{}, err
	}
	group, err := resourceStringDefault(declaration, "group", "root", host, facts, ctx)
	if err != nil {
		return ir.ManagedFileSpec{}, err
	}
	if !accountNamePattern.MatchString(owner) {
		return ir.ManagedFileSpec{}, resourceAttributeError(declaration, "owner", "must be a valid Alpine account name or numeric ID")
	}
	if !accountNamePattern.MatchString(group) {
		return ir.ManagedFileSpec{}, resourceAttributeError(declaration, "group", "must be a valid Alpine group name or numeric ID")
	}
	mode, err := resourceStringDefault(declaration, "mode", "0644", host, facts, ctx)
	if err != nil {
		return ir.ManagedFileSpec{}, err
	}
	mode, err = normalizeFileMode(mode)
	if err != nil {
		return ir.ManagedFileSpec{}, resourceAttributeError(declaration, "mode", "%v", err)
	}
	sensitive, err := resourceBoolDefault(declaration, "sensitive", false, host, facts, ctx)
	if err != nil {
		return ir.ManagedFileSpec{}, err
	}
	content, hasContent, err := resourceValue(declaration, "content", host, facts, ctx)
	if err != nil {
		return ir.ManagedFileSpec{}, err
	}
	source, hasSource, err := resourceValue(declaration, "source", host, facts, ctx)
	if err != nil {
		return ir.ManagedFileSpec{}, err
	}
	if hasContent && hasSource {
		return ir.ManagedFileSpec{}, resourceError(declaration.Source, "files.file requires only one of content or source")
	}
	if ensure == "present" && hasContent == hasSource {
		return ir.ManagedFileSpec{}, resourceError(declaration.Source, "files.file requires exactly one of content or source when ensure is present")
	}
	if ensure == "absent" && (hasContent || hasSource) {
		return ir.ManagedFileSpec{}, resourceError(declaration.Source, "files.file with ensure=absent must not set content or source")
	}
	var payload string
	var contentSensitive bool
	var contentEphemeral bool
	if hasContent {
		if content.Kind != parser.KindString {
			return ir.ManagedFileSpec{}, resourceAttributeError(declaration, "content", "must evaluate to a string")
		}
		payload = content.String
		contentSensitive = content.ContainsSensitive()
		contentEphemeral = content.ContainsEphemeral()
	}
	if hasSource {
		if source.Kind != parser.KindString || source.String == "" {
			return ir.ManagedFileSpec{}, resourceAttributeError(declaration, "source", "must evaluate to a non-empty string")
		}
		if source.ContainsSensitive() || source.ContainsEphemeral() {
			return ir.ManagedFileSpec{}, resourceAttributeError(declaration, "source", "must not use sensitive or ephemeral values")
		}
		sourcePath := source.String
		if !filepath.IsAbs(sourcePath) {
			sourcePath = filepath.Join(filepath.Dir(source.Source.File), sourcePath)
		}
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			return ir.ManagedFileSpec{}, resourceAttributeError(declaration, "source", "read %q: %v", sourcePath, err)
		}
		payload = string(data)
	}
	contentVersion, hasContentVersion, err := resourceString(declaration, "content_version", host, facts, ctx)
	if err != nil {
		return ir.ManagedFileSpec{}, err
	}
	if hasContentVersion {
		if contentVersion == "" {
			return ir.ManagedFileSpec{}, resourceAttributeError(declaration, "content_version", "must not be empty")
		}
	}
	if contentEphemeral && !hasContentVersion {
		return ir.ManagedFileSpec{}, resourceAttributeError(declaration, "content_version", "is required when content is ephemeral")
	}
	file := ir.ManagedFileSpec{
		Path:             path,
		Content:          payload,
		ContentVersion:   contentVersion,
		ContentWriteOnly: contentEphemeral,
		Owner:            owner,
		Group:            group,
		Mode:             mode,
		Ensure:           ensure,
		OnRemove:         onRemove,
		Sensitive:        sensitive || contentSensitive || contentEphemeral,
		Ephemeral:        contentEphemeral,
		Lifecycle:        ir.LifecycleSpec{PreventDestroy: declaration.Lifecycle.PreventDestroy, Source: declaration.Lifecycle.Source},
		Source:           declaration.Source,
	}
	if ensure == "present" {
		file.ContentBytes = int64(len(payload))
		if !contentEphemeral {
			sum := sha256.Sum256([]byte(payload))
			file.ContentSHA256 = hex.EncodeToString(sum[:])
		}
	}
	return file, nil
}

func resourceValue(declaration parser.ResourceDeclaration, name string, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (parser.Value, bool, error) {
	attribute, exists := declaration.Attributes[name]
	if !exists {
		return parser.Value{}, false, nil
	}
	if err := requirePlatformForExpression(attribute.Expression, attribute.Source, host, facts); err != nil {
		return parser.Value{}, false, err
	}
	attributeContext := ctx
	attributeContext.ModuleDir = filepath.Dir(attribute.Source.File)
	value, err := parser.EvaluateExpression(attribute.Expression, attributeContext, attribute.Source)
	if err != nil {
		return parser.Value{}, false, fmt.Errorf("%s:%d:%s: evaluate resource attribute: %w", attribute.Source.File, attribute.Source.Line, attribute.Source.Path, err)
	}
	return value, true, nil
}

func resourceString(declaration parser.ResourceDeclaration, name string, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (string, bool, error) {
	value, exists, err := resourceValue(declaration, name, host, facts, ctx)
	if err != nil || !exists {
		return "", exists, err
	}
	if value.Kind != parser.KindString {
		return "", true, resourceAttributeError(declaration, name, "must evaluate to a string")
	}
	if value.ContainsSensitive() || value.ContainsEphemeral() {
		return "", true, resourceAttributeError(declaration, name, "must not use sensitive or ephemeral values")
	}
	return value.String, true, nil
}

func resourceStringDefault(declaration parser.ResourceDeclaration, name, defaultValue string, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (string, error) {
	value, exists, err := resourceString(declaration, name, host, facts, ctx)
	if err != nil {
		return "", err
	}
	if !exists {
		return defaultValue, nil
	}
	return value, nil
}

func resourceBoolDefault(declaration parser.ResourceDeclaration, name string, defaultValue bool, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (bool, error) {
	value, exists, err := resourceValue(declaration, name, host, facts, ctx)
	if err != nil {
		return false, err
	}
	if !exists {
		return defaultValue, nil
	}
	if value.Kind != parser.KindBool || value.ContainsSensitive() || value.ContainsEphemeral() {
		return false, resourceAttributeError(declaration, name, "must evaluate to a non-protected boolean")
	}
	return value.Bool, nil
}

func normalizeFileMode(value string) (string, error) {
	if len(value) == 3 {
		value = "0" + value
	}
	if len(value) != 4 || value[0] < '0' || value[0] > '7' || strings.ContainsAny(value[1:], "89") {
		return "", fmt.Errorf("must be a three- or four-digit octal string")
	}
	for _, character := range value {
		if character < '0' || character > '7' {
			return "", fmt.Errorf("must be a three- or four-digit octal string")
		}
	}
	return value, nil
}

func resourceError(source ir.SourceRef, format string, args ...any) error {
	return fmt.Errorf("%s:%d:%s: %s", source.File, source.Line, source.Path, fmt.Sprintf(format, args...))
}

func resourceAttributeError(declaration parser.ResourceDeclaration, name, format string, args ...any) error {
	source := declaration.Source
	if attribute, exists := declaration.Attributes[name]; exists {
		source = attribute.Source
	} else {
		source.Path += "." + name
	}
	return resourceError(source, format, args...)
}

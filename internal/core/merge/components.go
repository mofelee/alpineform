package merge

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
	"github.com/zclconf/go-cty/cty"
)

var (
	componentSHA256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	componentModePattern   = regexp.MustCompile(`^0[0-7]{3}$`)
)

func validateComponentArtifactTemplates(components map[string]parser.Component) error {
	for _, name := range sortedComponentNames(components) {
		component := components[name]
		hasArtifact := component.ArtifactType != "" || component.Version != "" || len(component.Sources) > 0 || component.Extract != nil || component.Install != nil || component.Build != nil
		if !hasArtifact {
			continue
		}
		if component.ArtifactType == "" {
			return fmt.Errorf("%s:%d:%s.type: artifact component requires type", component.Source.File, component.Source.Line, component.Source.Path)
		}
		switch component.ArtifactType {
		case "binary", "file", "source":
		case "archive", "ca_certificate":
		default:
			return fmt.Errorf("%s:%d:%s.type: unsupported artifact type %q; supported types are binary, file, archive, ca_certificate, and source", component.Source.File, component.Source.Line, component.Source.Path, component.ArtifactType)
		}
		if component.Build != nil {
			if component.ArtifactType != "source" {
				return fmt.Errorf("%s:%d:%s: build blocks require component type \"source\"", component.Build.Source.File, component.Build.Source.Line, component.Build.Source.Path)
			}
			if len(component.Sources) != 0 || component.Extract != nil {
				return fmt.Errorf("%s:%d:%s: source-build components cannot use artifact source or extract blocks", component.Build.Source.File, component.Build.Source.Line, component.Build.Source.Path)
			}
		} else if component.ArtifactType == "source" {
			return fmt.Errorf("%s:%d:%s.build: source components require a build block", component.Source.File, component.Source.Line, component.Source.Path)
		}
		switch component.ArtifactType {
		case "archive":
			if component.Extract == nil {
				return fmt.Errorf("%s:%d:%s.extract: archive component requires an extract block", component.Source.File, component.Source.Line, component.Source.Path)
			}
			if format := component.Extract.Format; format != "" && format != "tar.gz" {
				return fmt.Errorf("%s:%d:%s.format: archive extraction supports only tar.gz in v0.1", component.Extract.Source.File, component.Extract.Source.Line, component.Extract.Source.Path)
			}
			if component.Extract.StripComponents < 0 {
				return fmt.Errorf("%s:%d:%s.strip_components: strip_components must be non-negative", component.Extract.Source.File, component.Extract.Source.Line, component.Extract.Source.Path)
			}
			if component.Extract.Include != "" {
				return fmt.Errorf("%s:%d:%s.include: archive extraction does not support include", component.Extract.Source.File, component.Extract.Source.Line, component.Extract.Source.Path)
			}
		case "binary":
			if component.Extract != nil {
				return fmt.Errorf("%s:%d:%s: binary extraction is not supported in this increment", component.Extract.Source.File, component.Extract.Source.Line, component.Extract.Source.Path)
			}
		default:
			if component.Extract != nil {
				return fmt.Errorf("%s:%d:%s: %s artifact does not support extraction", component.Extract.Source.File, component.Extract.Source.Line, component.Extract.Source.Path, component.ArtifactType)
			}
		}
		if component.Build == nil && len(component.Sources) == 0 {
			return fmt.Errorf("%s:%d:%s.source: artifact component requires at least one fixed source", component.Source.File, component.Source.Line, component.Source.Path)
		}
		if _, unlabelled := component.Sources[""]; unlabelled && len(component.Sources) != 1 {
			return fmt.Errorf("%s:%d:%s.source: architecture-independent and architecture-specific sources cannot be mixed", component.Source.File, component.Source.Line, component.Source.Path)
		}
		for _, architecture := range sortedComponentArtifactSourceArchitectures(component.Sources) {
			source := component.Sources[architecture]
			if architecture != "" && architecture != "amd64" && architecture != "arm64" {
				return fmt.Errorf("%s:%d:%s: source architecture %q must be amd64 or arm64", source.Source.File, source.Source.Line, source.Source.Path, architecture)
			}
			if source.URLExpr == nil {
				field := componentArtifactSourceField(source, "url")
				return fmt.Errorf("%s:%d:%s: source URL is required", field.File, field.Line, field.Path)
			}
			if source.SHA256Expr == nil {
				field := componentArtifactSourceField(source, "sha256")
				return fmt.Errorf("%s:%d:%s: source SHA-256 is required", field.File, field.Line, field.Path)
			}
		}
		if component.Install == nil {
			return fmt.Errorf("%s:%d:%s.install: artifact component requires an install block", component.Source.File, component.Source.Line, component.Source.Path)
		}
		install := component.Install
		if !filepath.IsAbs(install.Path) || filepath.Clean(install.Path) != install.Path || install.Path == "/" || strings.ContainsAny(install.Path, "\x00\r\n") {
			return fmt.Errorf("%s:%d:%s.path: install path must be a clean absolute non-root path", install.Source.File, install.Source.Line, install.Source.Path)
		}
		if install.Mode != "" && !componentModePattern.MatchString(install.Mode) {
			return fmt.Errorf("%s:%d:%s.mode: install mode must be a four-digit octal string", install.Source.File, install.Source.Line, install.Source.Path)
		}
		if component.ArtifactType == "ca_certificate" && (!strings.HasPrefix(install.Path, "/usr/local/share/ca-certificates/") || !strings.HasSuffix(install.Path, ".crt")) {
			return fmt.Errorf("%s:%d:%s.path: CA certificate path must be a .crt file under /usr/local/share/ca-certificates", install.Source.File, install.Source.Line, install.Source.Path)
		}
	}
	return nil
}

func componentArtifactSourceSpec(source parser.ComponentArtifactSource) ir.ComponentArtifactSourceSpec {
	return ir.ComponentArtifactSourceSpec{
		Architecture:    source.Architecture,
		URL:             source.URL,
		SHA256:          strings.ToLower(source.SHA256),
		URLSensitive:    source.URLSensitive,
		SHA256Sensitive: source.SHA256Sensitive,
		URLEphemeral:    source.URLEphemeral,
		SHA256Ephemeral: source.SHA256Ephemeral,
		URLSource:       source.URLSource,
		SHA256Source:    source.SHA256Source,
		Source:          source.Source,
	}
}

func componentArtifactExtractSpec(extract *parser.ComponentArtifactExtract) *ir.ComponentArtifactExtractSpec {
	if extract == nil {
		return nil
	}
	return &ir.ComponentArtifactExtractSpec{Format: extract.Format, StripComponents: extract.StripComponents, Include: extract.Include, Source: extract.Source}
}

func inferComponentArtifactFormat(rawURL string) string {
	path := strings.ToLower(rawURL)
	if query := strings.IndexAny(path, "?#"); query >= 0 {
		path = path[:query]
	}
	if strings.HasSuffix(path, ".tar.gz") || strings.HasSuffix(path, ".tgz") {
		return "tar.gz"
	}
	return ""
}

func resolveComponentArtifactSources(template parser.Component, ctx parser.EvalContext, instance parser.ComponentInstance) (map[string]parser.ComponentArtifactSource, error) {
	if len(template.Sources) == 0 {
		return nil, nil
	}
	resolved := make(map[string]parser.ComponentArtifactSource, len(template.Sources))
	for _, architecture := range sortedComponentArtifactSourceArchitectures(template.Sources) {
		source := template.Sources[architecture]
		var err error
		source.URL, source.URLSensitive, source.URLEphemeral, err = evaluateResolvedComponentArtifactString("url", source.URLExpr, ctx, source.URLSource, instance.Source)
		if err != nil {
			return nil, err
		}
		if err := validateResolvedComponentArtifactURL(source, instance.Source); err != nil {
			return nil, err
		}
		source.SHA256, source.SHA256Sensitive, source.SHA256Ephemeral, err = evaluateResolvedComponentArtifactString("sha256", source.SHA256Expr, ctx, source.SHA256Source, instance.Source)
		if err != nil {
			return nil, err
		}
		if err := validateResolvedComponentArtifactSHA256(source, instance.Source); err != nil {
			return nil, err
		}
		resolved[architecture] = source
	}
	return resolved, nil
}

func evaluateResolvedComponentArtifactString(name string, expr hcl.Expression, ctx parser.EvalContext, source, mount ir.SourceRef) (string, bool, bool, error) {
	if expr == nil {
		return "", false, false, componentMountedError(source, mount, fmt.Sprintf("source %s is required", name))
	}
	protected := expressionReferencesProtectedValue(expr, ctx)
	ctx.ModuleDir = filepath.Dir(source.File)
	value, err := parser.EvaluateExpression(expr, ctx, source)
	if err != nil {
		if protected {
			return "", false, false, componentMountedError(source, mount, "protected artifact source expression failed to evaluate")
		}
		return "", false, false, componentMountedError(source, mount, fmt.Sprintf("artifact source expression failed to evaluate: %v", err))
	}
	if value.Kind == parser.KindNull {
		return "", false, false, componentMountedError(source, mount, fmt.Sprintf("%s must not be null", name))
	}
	if value.Kind != parser.KindString {
		return "", false, false, componentMountedError(source, mount, fmt.Sprintf("%s must be a string", name))
	}
	if value.String == "" {
		return "", false, false, componentMountedError(source, mount, fmt.Sprintf("%s must be a non-empty string", name))
	}
	return value.String, value.ContainsSensitive(), value.ContainsEphemeral(), nil
}

func validateResolvedComponentArtifactURL(source parser.ComponentArtifactSource, mount ir.SourceRef) error {
	urlSource := componentArtifactSourceField(source, "url")
	parsed, err := url.Parse(source.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.Fragment != "" {
		return componentMountedError(urlSource, mount, "source URL must be an absolute http(s) URL without credentials or a fragment")
	}
	return nil
}

func validateResolvedComponentArtifactSHA256(source parser.ComponentArtifactSource, mount ir.SourceRef) error {
	if !componentSHA256Pattern.MatchString(source.SHA256) {
		shaSource := componentArtifactSourceField(source, "sha256")
		return componentMountedError(shaSource, mount, "source SHA-256 must be exactly 64 hexadecimal characters")
	}
	return nil
}

func componentArtifactSourceField(source parser.ComponentArtifactSource, name string) ir.SourceRef {
	field := source.URLSource
	if name == "sha256" {
		field = source.SHA256Source
	}
	if field.File == "" {
		field = source.Source
		field.Path += "." + name
	}
	return field
}

func componentMountedError(field, mount ir.SourceRef, message string) error {
	return fmt.Errorf("%s:%d:%s mounted at %s:%d:%s: %s", field.File, field.Line, field.Path, mount.File, mount.Line, mount.Path, message)
}

func expressionReferencesProtectedValue(expr hcl.Expression, ctx parser.EvalContext) bool {
	for _, traversal := range expr.Variables() {
		root, ok := componentArtifactTraversalRoot(traversal)
		if !ok {
			continue
		}
		name, hasName := componentArtifactTraversalFirstName(traversal)
		if root == "local" {
			if hasName {
				value, exists := ctx.Locals[name]
				if exists && (value.ContainsSensitive() || value.ContainsEphemeral()) {
					return true
				}
				continue
			}
			for _, value := range ctx.Locals {
				if value.ContainsSensitive() || value.ContainsEphemeral() {
					return true
				}
			}
			continue
		}
		value, exists := ctx.Variables[root]
		if !exists {
			continue
		}
		if value.HasMark(parser.SensitiveMark) || value.HasMark(parser.EphemeralMark) {
			return true
		}
		unmarked, _ := value.Unmark()
		if hasName && unmarked.IsKnown() && !unmarked.IsNull() {
			switch {
			case unmarked.Type().IsObjectType() && unmarked.Type().HasAttribute(name):
				value = unmarked.GetAttr(name)
			case unmarked.Type().IsMapType():
				key := cty.StringVal(name)
				if present := unmarked.HasIndex(key); present.IsKnown() && present.True() {
					value = unmarked.Index(key)
				} else {
					value = unmarked
				}
			default:
				value = unmarked
			}
		} else {
			value = unmarked
		}
		if componentArtifactCtyValueProtected(value) {
			return true
		}
	}
	return false
}

func componentArtifactTraversalRoot(traversal hcl.Traversal) (string, bool) {
	if len(traversal) == 0 {
		return "", false
	}
	root, ok := traversal[0].(hcl.TraverseRoot)
	return root.Name, ok
}

func componentArtifactTraversalFirstName(traversal hcl.Traversal) (string, bool) {
	if len(traversal) < 2 {
		return "", false
	}
	switch step := traversal[1].(type) {
	case hcl.TraverseAttr:
		return step.Name, true
	case hcl.TraverseIndex:
		key, _ := step.Key.UnmarkDeep()
		if key.IsKnown() && !key.IsNull() && key.Type().Equals(cty.String) {
			return key.AsString(), true
		}
	}
	return "", false
}

func componentArtifactCtyValueProtected(value cty.Value) bool {
	if value.HasMark(parser.SensitiveMark) || value.HasMark(parser.EphemeralMark) {
		return true
	}
	_, marks := value.UnmarkDeep()
	return marks.Has(parser.SensitiveMark) || marks.Has(parser.EphemeralMark)
}

func sortedComponentArtifactSourceArchitectures(sources map[string]parser.ComponentArtifactSource) []string {
	architectures := make([]string, 0, len(sources))
	for architecture := range sources {
		architectures = append(architectures, architecture)
	}
	sort.Strings(architectures)
	return architectures
}

func componentArtifactInstallSpec(artifactType string, install *parser.ComponentArtifactInstall) *ir.ComponentArtifactInstallSpec {
	if install == nil {
		return nil
	}
	owner := install.Owner
	if owner == "" {
		owner = "root"
	}
	group := install.Group
	if group == "" {
		group = "root"
	}
	mode := install.Mode
	if mode == "" {
		mode = "0755"
		if artifactType == "file" || artifactType == "ca_certificate" {
			mode = "0644"
		}
	}
	var onChange *ir.ScriptReferenceSpec
	if install.OnChange != nil {
		onChange = &ir.ScriptReferenceSpec{Name: install.OnChange.Name, Scope: string(install.OnChange.Scope), Source: install.OnChange.Source}
	}
	return &ir.ComponentArtifactInstallSpec{Path: install.Path, Owner: owner, Group: group, Mode: mode, OnChange: onChange, Source: install.Source}
}

func selectComponentArtifactSource(template parser.Component, sources map[string]parser.ComponentArtifactSource, host parser.Host, facts *ir.HostFacts, instance parser.ComponentInstance) (*ir.ComponentArtifactSourceSpec, error) {
	if len(sources) == 0 {
		return nil, nil
	}
	if source, exists := sources[""]; exists {
		selected := componentArtifactSourceSpec(source)
		return &selected, nil
	}
	architecture := ""
	if facts != nil {
		architecture = facts.Architecture
	} else if host.Platform != nil {
		architecture = host.Platform.Architecture
	}
	if architecture == "" {
		field := template.Source
		field.Path += ".source"
		return nil, componentMountedError(field, instance.Source, fmt.Sprintf("component.%s requires host %q to declare platform.architecture for offline source selection", template.Name, host.Name))
	}
	source, exists := sources[architecture]
	if !exists {
		field := template.Source
		field.Path += ".source"
		return nil, componentMountedError(field, instance.Source, fmt.Sprintf("component.%s has no source for normalized architecture %q", template.Name, architecture))
	}
	selected := componentArtifactSourceSpec(source)
	return &selected, nil
}

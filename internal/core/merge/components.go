package merge

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
)

var (
	componentSHA256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	componentModePattern   = regexp.MustCompile(`^0[0-7]{3}$`)
)

func validateComponentArtifacts(components map[string]parser.Component) error {
	for _, name := range sortedComponentNames(components) {
		component := components[name]
		hasArtifact := component.ArtifactType != "" || component.Version != "" || len(component.Sources) > 0 || component.Extract != nil || component.Install != nil
		if !hasArtifact {
			continue
		}
		if component.ArtifactType == "" {
			return fmt.Errorf("%s:%d:%s.type: artifact component requires type", component.Source.File, component.Source.Line, component.Source.Path)
		}
		switch component.ArtifactType {
		case "binary", "file":
		case "archive", "ca_certificate":
		default:
			return fmt.Errorf("%s:%d:%s.type: unsupported artifact type %q; v0.1 supports binary, file, archive, and ca_certificate", component.Source.File, component.Source.Line, component.Source.Path, component.ArtifactType)
		}
		switch component.ArtifactType {
		case "archive":
			if component.Extract == nil {
				return fmt.Errorf("%s:%d:%s.extract: archive component requires an extract block", component.Source.File, component.Source.Line, component.Source.Path)
			}
			format := component.Extract.Format
			if format == "" {
				format = inferComponentArtifactFormat(component.Sources)
			}
			if format != "tar.gz" {
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
		if len(component.Sources) == 0 {
			return fmt.Errorf("%s:%d:%s.source: artifact component requires at least one fixed source", component.Source.File, component.Source.Line, component.Source.Path)
		}
		if _, unlabelled := component.Sources[""]; unlabelled && len(component.Sources) != 1 {
			return fmt.Errorf("%s:%d:%s.source: architecture-independent and architecture-specific sources cannot be mixed", component.Source.File, component.Source.Line, component.Source.Path)
		}
		for architecture, source := range component.Sources {
			if architecture != "" && architecture != "amd64" && architecture != "arm64" {
				return fmt.Errorf("%s:%d:%s: source architecture %q must be amd64 or arm64", source.Source.File, source.Source.Line, source.Source.Path, architecture)
			}
			parsed, err := url.Parse(source.URL)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.Fragment != "" {
				return fmt.Errorf("%s:%d:%s.url: source URL must be an absolute http(s) URL without credentials or a fragment", source.Source.File, source.Source.Line, source.Source.Path)
			}
			if !componentSHA256Pattern.MatchString(source.SHA256) {
				return fmt.Errorf("%s:%d:%s.sha256: source SHA-256 must be exactly 64 hexadecimal characters", source.Source.File, source.Source.Line, source.Source.Path)
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
	return ir.ComponentArtifactSourceSpec{Architecture: source.Architecture, URL: source.URL, SHA256: strings.ToLower(source.SHA256), Source: source.Source}
}

func componentArtifactExtractSpec(extract *parser.ComponentArtifactExtract) *ir.ComponentArtifactExtractSpec {
	if extract == nil {
		return nil
	}
	return &ir.ComponentArtifactExtractSpec{Format: extract.Format, StripComponents: extract.StripComponents, Include: extract.Include, Source: extract.Source}
}

func inferComponentArtifactFormat(sources map[string]parser.ComponentArtifactSource) string {
	for _, source := range sources {
		path := strings.ToLower(source.URL)
		if query := strings.IndexAny(path, "?#"); query >= 0 {
			path = path[:query]
		}
		if strings.HasSuffix(path, ".tar.gz") || strings.HasSuffix(path, ".tgz") {
			return "tar.gz"
		}
	}
	return ""
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
	return &ir.ComponentArtifactInstallSpec{Path: install.Path, Owner: owner, Group: group, Mode: mode, Source: install.Source}
}

func selectComponentArtifactSource(template parser.Component, host parser.Host, facts *ir.HostFacts, instance parser.ComponentInstance) (*ir.ComponentArtifactSourceSpec, error) {
	if len(template.Sources) == 0 {
		return nil, nil
	}
	if source, exists := template.Sources[""]; exists {
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
		return nil, fmt.Errorf("%s:%d:%s: component.%s requires host %q to declare platform.architecture for offline source selection", instance.Source.File, instance.Source.Line, instance.Source.Path, template.Name, host.Name)
	}
	source, exists := template.Sources[architecture]
	if !exists {
		return nil, fmt.Errorf("%s:%d:%s: component.%s has no source for normalized architecture %q", instance.Source.File, instance.Source.Line, instance.Source.Path, template.Name, architecture)
	}
	selected := componentArtifactSourceSpec(source)
	return &selected, nil
}

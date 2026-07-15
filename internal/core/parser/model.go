package parser

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/zclconf/go-cty/cty"
)

var declarationLabelPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Profile struct {
	Name       string
	Imports    []string
	Components []ComponentInstance
	Asserts    []Assert
	Source     ir.SourceRef
}

type Component struct {
	Name         string
	Description  string
	ArtifactType string
	Version      string
	Inputs       map[string]ComponentInput
	Scripts      map[string]Script
	Sources      map[string]ComponentArtifactSource
	Extract      *ComponentArtifactExtract
	Install      *ComponentArtifactInstall
	Build        *ComponentBuild
	OpenRC       *OpenRC
	Resources    []ResourceDeclaration
	Asserts      []Assert
	Source       ir.SourceRef
}

type ComponentBuild struct {
	Attributes map[string]ResourceAttribute
	Inputs     []ComponentBuildInput
	Commands   []ComponentBuildCommand
	Source     ir.SourceRef
}

type ComponentBuildInput struct {
	Name       string
	Attributes map[string]ResourceAttribute
	Extract    *ComponentBuildInputExtract
	Source     ir.SourceRef
}

type ComponentBuildInputExtract struct {
	Attributes map[string]ResourceAttribute
	Source     ir.SourceRef
}

type ComponentBuildCommand struct {
	Attributes map[string]ResourceAttribute
	Source     ir.SourceRef
}

type ComponentArtifactSource struct {
	Architecture string
	URL          string
	SHA256       string
	Source       ir.SourceRef
}

type ComponentArtifactExtract struct {
	Format          string
	StripComponents int
	Include         string
	Source          ir.SourceRef
}

type ComponentArtifactInstall struct {
	Path     string
	Owner    string
	Group    string
	Mode     string
	OnChange *ScriptReference
	Source   ir.SourceRef
}

type ComponentInput struct {
	Name        string
	Type        string
	TypeSpec    ComponentInputTypeSpec
	Description string
	Default     *Value
	Nullable    bool
	Sensitive   bool
	Ephemeral   bool
	Deprecated  string
	Validations []ComponentInputValidation
	Source      ir.SourceRef
}

type ComponentInputValidation struct {
	Source          ir.SourceRef
	Condition       hcl.Expression
	ConditionSource ir.SourceRef
	Message         string
	MessageSource   ir.SourceRef
}

type ComponentInstance struct {
	Name      string
	Template  string
	Inputs    map[string]Value
	DependsOn []string
	Lifecycle Lifecycle
	Source    ir.SourceRef
}

type Lifecycle struct {
	PreventDestroy bool
	Source         ir.SourceRef
}

type Platform struct {
	Architecture       string
	Version            string
	Branch             string
	Libc               string
	NativeArchitecture string
	Source             ir.SourceRef
}

type Host struct {
	Name       string
	Imports    []string
	SSH        SSH
	Platform   *Platform
	APK        *APK
	OpenRC     *OpenRC
	System     *System
	Kernel     *Kernel
	Nftables   *Nftables
	Docker     *Docker
	Components []ComponentInstance
	Resources  []ResourceDeclaration
	Asserts    []Assert
	Source     ir.SourceRef
}

type Docker struct {
	Attributes map[string]ResourceAttribute
	Projects   []ResourceDeclaration
	Lifecycle  Lifecycle
	Source     ir.SourceRef
}

type OpenRC struct {
	Services []ResourceDeclaration
	Source   ir.SourceRef
}

type System struct {
	Attributes map[string]ResourceAttribute
	Source     ir.SourceRef
}

type APK struct {
	Ownership    string
	Repositories []ResourceDeclaration
	Keys         []ResourceDeclaration
	Source       ir.SourceRef
}

type SSH struct {
	Host         string
	Port         int
	User         string
	IdentityFile string
	Source       ir.SourceRef
}

type Script struct {
	Name        string
	Description string
	Attributes  map[string]ResourceAttribute
	Sensitive   bool
	Source      ir.SourceRef
}

type ScriptReferenceScope string

const (
	ScriptReferenceAuto   ScriptReferenceScope = "auto"
	ScriptReferenceGlobal ScriptReferenceScope = "global"
)

type ScriptReference struct {
	Name   string
	Scope  ScriptReferenceScope
	Source ir.SourceRef
}

type Assert struct {
	Condition       hcl.Expression
	ConditionSource ir.SourceRef
	Message         string
	MessageSource   ir.SourceRef
	Source          ir.SourceRef
}

func parseModelBlocks(cfg *Config, file string, body *hclsyntax.Body) error {
	ctx, err := evalContextForConfig(cfg, filepath.Dir(file))
	if err != nil {
		return err
	}
	for _, block := range body.Blocks {
		switch block.Type {
		case "variable", "locals":
			continue
		case "profile":
			profile, err := parseProfile(file, block, ctx)
			if err != nil {
				return err
			}
			if previous, exists := cfg.Profiles[profile.Name]; exists {
				return duplicateDeclarationError("profile", profile.Name, profile.Source, previous.Source)
			}
			cfg.Profiles[profile.Name] = profile
		case "component":
			component, err := parseComponent(file, block, ctx)
			if err != nil {
				return err
			}
			if previous, exists := cfg.Components[component.Name]; exists {
				return duplicateDeclarationError("component", component.Name, component.Source, previous.Source)
			}
			cfg.Components[component.Name] = component
		case "host":
			host, err := parseHost(file, block, ctx)
			if err != nil {
				return err
			}
			if previous, exists := cfg.Hosts[host.Name]; exists {
				return duplicateDeclarationError("host", host.Name, host.Source, previous.Source)
			}
			cfg.Hosts[host.Name] = host
		case "script":
			script, err := parseScript(file, block, ctx)
			if err != nil {
				return err
			}
			if previous, exists := cfg.Scripts[script.Name]; exists {
				return duplicateDeclarationError("script", script.Name, script.Source, previous.Source)
			}
			cfg.Scripts[script.Name] = script
		case "assert":
			assertion, err := parseAssert(file, "assert", block, ctx)
			if err != nil {
				return err
			}
			cfg.Asserts = append(cfg.Asserts, assertion)
		default:
			return fmt.Errorf("%s:%d: unknown top-level block %q", file, block.TypeRange.Start.Line, block.Type)
		}
	}
	return nil
}

func parseProfile(file string, block *hclsyntax.Block, ctx EvalContext) (Profile, error) {
	name, path, source, err := declarationIdentity(file, "profile", block)
	if err != nil {
		return Profile{}, err
	}
	if err := rejectAttributesExcept(file, path, block.Body.Attributes, "imports"); err != nil {
		return Profile{}, err
	}
	profile := Profile{Name: name, Source: source}
	if attr, exists := block.Body.Attributes["imports"]; exists {
		profile.Imports, err = parseReferences(file, path+".imports", attr.Expr, "profile")
		if err != nil {
			return Profile{}, err
		}
	}
	for _, child := range block.Body.Blocks {
		switch child.Type {
		case "component":
			instance, err := parseComponentInstance(file, path, child, ctx)
			if err != nil {
				return Profile{}, err
			}
			if err := rejectDuplicateInstance(profile.Components, instance); err != nil {
				return Profile{}, err
			}
			profile.Components = append(profile.Components, instance)
		case "assert":
			assertion, err := parseAssert(file, path+".assert", child, ctx)
			if err != nil {
				return Profile{}, err
			}
			profile.Asserts = append(profile.Asserts, assertion)
		default:
			return Profile{}, fmt.Errorf("%s:%d: unsupported block %s.%s", file, child.TypeRange.Start.Line, path, child.Type)
		}
	}
	return profile, nil
}

func parseComponent(file string, block *hclsyntax.Block, ctx EvalContext) (Component, error) {
	name, path, source, err := declarationIdentity(file, "component", block)
	if err != nil {
		return Component{}, err
	}
	if err := rejectAttributesExcept(file, path, block.Body.Attributes, "description", "type", "version"); err != nil {
		return Component{}, err
	}
	component := Component{Name: name, Inputs: map[string]ComponentInput{}, Scripts: map[string]Script{}, Sources: map[string]ComponentArtifactSource{}, Source: source}
	if attr, exists := block.Body.Attributes["description"]; exists {
		component.Description, err = evalStringAttribute(file, path, "description", attr, ctx, false)
		if err != nil {
			return Component{}, err
		}
	}
	if attr, exists := block.Body.Attributes["type"]; exists {
		component.ArtifactType, err = evalStringAttribute(file, path, "type", attr, ctx, true)
		if err != nil {
			return Component{}, err
		}
	}
	if attr, exists := block.Body.Attributes["version"]; exists {
		component.Version, err = evalStringAttribute(file, path, "version", attr, ctx, true)
		if err != nil {
			return Component{}, err
		}
	}
	for _, child := range block.Body.Blocks {
		switch child.Type {
		case "input":
			input, err := parseComponentInput(file, path, child, ctx)
			if err != nil {
				return Component{}, err
			}
			if previous, exists := component.Inputs[input.Name]; exists {
				return Component{}, duplicateDeclarationError("component input", input.Name, input.Source, previous.Source)
			}
			component.Inputs[input.Name] = input
		case "script":
			script, err := parseScriptAt(file, path, child, ctx)
			if err != nil {
				return Component{}, err
			}
			if previous, exists := component.Scripts[script.Name]; exists {
				return Component{}, duplicateDeclarationError("component script", script.Name, script.Source, previous.Source)
			}
			component.Scripts[script.Name] = script
		case "files", "directories", "groups", "users", "packages", "services":
			resources, err := parseHostResourceCollection(file, path, child, ctx)
			if err != nil {
				return Component{}, err
			}
			component.Resources, err = appendUniqueResources(component.Resources, resources)
			if err != nil {
				return Component{}, err
			}
		case "openrc":
			if component.OpenRC != nil {
				return Component{}, fmt.Errorf("%s:%d: duplicate %s.openrc block", file, child.TypeRange.Start.Line, path)
			}
			openrc, err := parseOpenRC(file, path+".openrc", child, ctx)
			if err != nil {
				return Component{}, err
			}
			component.OpenRC = &openrc
		case "source":
			artifactSource, err := parseComponentArtifactSource(file, path, child, ctx)
			if err != nil {
				return Component{}, err
			}
			if previous, exists := component.Sources[artifactSource.Architecture]; exists {
				return Component{}, duplicateDeclarationError("component source", artifactSource.Architecture, artifactSource.Source, previous.Source)
			}
			component.Sources[artifactSource.Architecture] = artifactSource
		case "extract":
			if component.Extract != nil {
				return Component{}, fmt.Errorf("%s:%d: duplicate %s.extract block", file, child.TypeRange.Start.Line, path)
			}
			extract, err := parseComponentArtifactExtract(file, path, child, ctx)
			if err != nil {
				return Component{}, err
			}
			component.Extract = &extract
		case "install":
			if component.Install != nil {
				return Component{}, fmt.Errorf("%s:%d: duplicate %s.install block", file, child.TypeRange.Start.Line, path)
			}
			install, err := parseComponentArtifactInstall(file, path, child, ctx)
			if err != nil {
				return Component{}, err
			}
			component.Install = &install
		case "build":
			if component.Build != nil {
				return Component{}, fmt.Errorf("%s:%d: duplicate %s.build block", file, child.TypeRange.Start.Line, path)
			}
			build, err := parseComponentBuild(file, path, child)
			if err != nil {
				return Component{}, err
			}
			component.Build = &build
		case "assert":
			assertion, err := parseAssert(file, path+".assert", child, ctx)
			if err != nil {
				return Component{}, err
			}
			component.Asserts = append(component.Asserts, assertion)
		default:
			return Component{}, fmt.Errorf("%s:%d: unsupported block %s.%s", file, child.TypeRange.Start.Line, path, child.Type)
		}
	}
	return component, nil
}

func parseComponentInput(file, parentPath string, block *hclsyntax.Block, ctx EvalContext) (ComponentInput, error) {
	name, path, source, err := declarationIdentityAt(file, parentPath+".input", "input", block)
	if err != nil {
		return ComponentInput{}, err
	}
	if err := rejectAttributesExcept(file, path, block.Body.Attributes, "type", "default", "description", "nullable", "sensitive", "ephemeral", "deprecated"); err != nil {
		return ComponentInput{}, err
	}
	for _, child := range block.Body.Blocks {
		if child.Type != "validation" {
			return ComponentInput{}, fmt.Errorf("%s:%d: unsupported block %s.%s", file, child.TypeRange.Start.Line, path, child.Type)
		}
	}
	typeAttr, exists := block.Body.Attributes["type"]
	if !exists {
		return ComponentInput{}, fmt.Errorf("%s:%d: %s.type is required", file, source.Line, path)
	}
	typeSpec, typeName, err := parseComponentInputType(typeAttr.Expr, ctx, path+".type")
	if err != nil {
		return ComponentInput{}, fmt.Errorf("%s:%d:%s.type: %w", file, typeAttr.NameRange.Start.Line, path, err)
	}
	input := ComponentInput{Name: name, Type: typeName, TypeSpec: typeSpec, Nullable: true, Source: source}
	if attr, exists := block.Body.Attributes["description"]; exists {
		input.Description, err = evalStringAttribute(file, path, "description", attr, ctx, false)
		if err != nil {
			return ComponentInput{}, err
		}
	}
	if attr, exists := block.Body.Attributes["deprecated"]; exists {
		input.Deprecated, err = evalStringAttribute(file, path, "deprecated", attr, ctx, true)
		if err != nil {
			return ComponentInput{}, err
		}
	}
	if attr, exists := block.Body.Attributes["default"]; exists {
		defaultSource := ir.SourceRef{File: file, Line: attr.NameRange.Start.Line, Path: path + ".default"}
		value, err := evalValue(attr.Expr, ctx, defaultSource)
		if err != nil {
			return ComponentInput{}, err
		}
		input.Default = &value
	}
	for _, item := range []struct {
		name string
		set  func(bool)
	}{
		{name: "nullable", set: func(value bool) { input.Nullable = value }},
		{name: "sensitive", set: func(value bool) { input.Sensitive = value }},
		{name: "ephemeral", set: func(value bool) { input.Ephemeral = value }},
	} {
		if attr, exists := block.Body.Attributes[item.name]; exists {
			value, err := evalBoolAttribute(file, path, item.name, attr, ctx)
			if err != nil {
				return ComponentInput{}, err
			}
			item.set(value)
		}
	}
	for i, child := range block.Body.Blocks {
		validation, err := parseComponentInputValidationBlock(file, fmt.Sprintf("%s.validation[%d]", path, i), child, ctx)
		if err != nil {
			return ComponentInput{}, err
		}
		input.Validations = append(input.Validations, validation)
	}
	if input.Default != nil {
		normalized, err := NormalizeComponentInputValue(input, *input.Default)
		if err != nil {
			if input.Sensitive || input.Ephemeral {
				return ComponentInput{}, fmt.Errorf("%s:%d:%s: invalid protected component input default", input.Source.File, input.Source.Line, input.Source.Path)
			}
			return ComponentInput{}, err
		}
		input.Default = &normalized
	}
	return input, nil
}

func parseHost(file string, block *hclsyntax.Block, ctx EvalContext) (Host, error) {
	name, path, source, err := declarationIdentity(file, "host", block)
	if err != nil {
		return Host{}, err
	}
	if err := rejectAttributesExcept(file, path, block.Body.Attributes, "imports"); err != nil {
		return Host{}, err
	}
	host := Host{Name: name, SSH: SSH{Host: name, User: "root", Source: source}, Source: source}
	if attr, exists := block.Body.Attributes["imports"]; exists {
		host.Imports, err = parseReferences(file, path+".imports", attr.Expr, "profile")
		if err != nil {
			return Host{}, err
		}
	}
	for _, child := range block.Body.Blocks {
		switch child.Type {
		case "files", "directories", "groups", "users", "packages", "services":
			resources, err := parseHostResourceCollection(file, path, child, ctx)
			if err != nil {
				return Host{}, err
			}
			host.Resources, err = appendUniqueResources(host.Resources, resources)
			if err != nil {
				return Host{}, err
			}
		case "apk":
			if host.APK != nil {
				return Host{}, fmt.Errorf("%s:%d: duplicate %s.apk block", file, child.TypeRange.Start.Line, path)
			}
			apk, err := parseAPK(file, path+".apk", child, ctx)
			if err != nil {
				return Host{}, err
			}
			host.APK = &apk
		case "openrc":
			if host.OpenRC != nil {
				return Host{}, fmt.Errorf("%s:%d: duplicate %s.openrc block", file, child.TypeRange.Start.Line, path)
			}
			openrc, err := parseOpenRC(file, path+".openrc", child, ctx)
			if err != nil {
				return Host{}, err
			}
			host.OpenRC = &openrc
		case "system":
			if host.System != nil {
				return Host{}, fmt.Errorf("%s:%d: duplicate %s.system block", file, child.TypeRange.Start.Line, path)
			}
			system, err := parseSystem(file, path+".system", child)
			if err != nil {
				return Host{}, err
			}
			host.System = &system
		case "kernel":
			if host.Kernel != nil {
				return Host{}, fmt.Errorf("%s:%d: duplicate %s.kernel block", file, child.TypeRange.Start.Line, path)
			}
			kernel, err := parseKernel(file, path+".kernel", child, ctx)
			if err != nil {
				return Host{}, err
			}
			host.Kernel = &kernel
		case "nftables":
			if host.Nftables != nil {
				return Host{}, fmt.Errorf("%s:%d: duplicate %s.nftables block", file, child.TypeRange.Start.Line, path)
			}
			nftables, err := parseNftables(file, path+".nftables", child, ctx)
			if err != nil {
				return Host{}, err
			}
			host.Nftables = &nftables
		case "docker":
			if host.Docker != nil {
				return Host{}, fmt.Errorf("%s:%d: duplicate %s.docker block", file, child.TypeRange.Start.Line, path)
			}
			docker, err := parseDocker(file, path+".docker", child, ctx)
			if err != nil {
				return Host{}, err
			}
			host.Docker = &docker
		case "ssh":
			if host.SSH.Source.Path != host.Source.Path {
				return Host{}, fmt.Errorf("%s:%d: duplicate %s.ssh block", file, child.TypeRange.Start.Line, path)
			}
			ssh, err := parseSSH(file, path+".ssh", child, ctx, name)
			if err != nil {
				return Host{}, err
			}
			host.SSH = ssh
		case "platform":
			if host.Platform != nil {
				return Host{}, fmt.Errorf("%s:%d: duplicate %s.platform block", file, child.TypeRange.Start.Line, path)
			}
			platform, err := parsePlatform(file, path+".platform", child, ctx)
			if err != nil {
				return Host{}, err
			}
			host.Platform = &platform
		case "component":
			instance, err := parseComponentInstance(file, path, child, ctx)
			if err != nil {
				return Host{}, err
			}
			if err := rejectDuplicateInstance(host.Components, instance); err != nil {
				return Host{}, err
			}
			host.Components = append(host.Components, instance)
		case "assert":
			assertion, err := parseAssert(file, path+".assert", child, ctx)
			if err != nil {
				return Host{}, err
			}
			host.Asserts = append(host.Asserts, assertion)
		default:
			return Host{}, fmt.Errorf("%s:%d: unsupported block %s.%s", file, child.TypeRange.Start.Line, path, child.Type)
		}
	}
	return host, nil
}

func parseSSH(file, path string, block *hclsyntax.Block, ctx EvalContext, defaultHost string) (SSH, error) {
	if len(block.Labels) != 0 || len(block.Body.Blocks) != 0 {
		return SSH{}, fmt.Errorf("%s:%d: %s must be an unlabeled attribute-only block", file, block.TypeRange.Start.Line, path)
	}
	if err := rejectAttributesExcept(file, path, block.Body.Attributes, "host", "port", "user", "identity_file"); err != nil {
		return SSH{}, err
	}
	ssh := SSH{Host: defaultHost, User: "root", Source: ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path}}
	var err error
	if attr, exists := block.Body.Attributes["host"]; exists {
		ssh.Host, err = evalStringAttribute(file, path, "host", attr, ctx, true)
		if err != nil {
			return SSH{}, err
		}
		if strings.HasPrefix(ssh.Host, "-") || strings.ContainsAny(ssh.Host, " \t\r\n") {
			return SSH{}, fmt.Errorf("%s:%d:%s.host: SSH host must be a single alias or address", file, attr.NameRange.Start.Line, path)
		}
	}
	if attr, exists := block.Body.Attributes["port"]; exists {
		value, err := evalValue(attr.Expr, ctx, ir.SourceRef{File: file, Line: attr.NameRange.Start.Line, Path: path + ".port"})
		if err != nil {
			return SSH{}, err
		}
		if value.Kind != KindNumber || strings.ContainsAny(value.Number, ".eE") {
			return SSH{}, fmt.Errorf("%s:%d:%s.port: SSH port must be an integer", file, attr.NameRange.Start.Line, path)
		}
		port, err := strconv.Atoi(value.Number)
		if err != nil || port < 1 || port > 65535 {
			return SSH{}, fmt.Errorf("%s:%d:%s.port: SSH port must be between 1 and 65535", file, attr.NameRange.Start.Line, path)
		}
		ssh.Port = port
	}
	if attr, exists := block.Body.Attributes["user"]; exists {
		ssh.User, err = evalStringAttribute(file, path, "user", attr, ctx, true)
		if err != nil {
			return SSH{}, err
		}
		if ssh.User != "root" {
			return SSH{}, fmt.Errorf("%s:%d:%s.user: AlpineForm v0.1 requires root SSH; user %q is unsupported", file, attr.NameRange.Start.Line, path, ssh.User)
		}
	}
	if attr, exists := block.Body.Attributes["identity_file"]; exists {
		ssh.IdentityFile, err = evalStringAttribute(file, path, "identity_file", attr, ctx, true)
		if err != nil {
			return SSH{}, err
		}
		if strings.ContainsAny(ssh.IdentityFile, "\x00\r\n") {
			return SSH{}, fmt.Errorf("%s:%d:%s.identity_file: invalid identity file path", file, attr.NameRange.Start.Line, path)
		}
	}
	return ssh, nil
}

func parsePlatform(file, path string, block *hclsyntax.Block, ctx EvalContext) (Platform, error) {
	if len(block.Labels) != 0 {
		return Platform{}, fmt.Errorf("%s:%d: %s block must not have labels", file, block.TypeRange.Start.Line, path)
	}
	if len(block.Body.Blocks) != 0 {
		return Platform{}, fmt.Errorf("%s:%d: %s does not support nested blocks", file, block.Body.Blocks[0].TypeRange.Start.Line, path)
	}
	for name, attr := range block.Body.Attributes {
		switch name {
		case "architecture", "version":
		case "branch", "libc", "native_architecture":
			return Platform{}, fmt.Errorf("%s:%d: %s.%s is a read-only derived fact and cannot be configured", file, attr.NameRange.Start.Line, path, name)
		default:
			return Platform{}, fmt.Errorf("%s:%d: unsupported attribute %s.%s", file, attr.NameRange.Start.Line, path, name)
		}
	}
	platform := Platform{Libc: "musl", Source: ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path}}
	if attr, exists := block.Body.Attributes["architecture"]; exists {
		value, err := evalStringAttribute(file, path, "architecture", attr, ctx, true)
		if err != nil {
			return Platform{}, err
		}
		switch value {
		case "amd64", "x86_64":
			platform.Architecture = "amd64"
			platform.NativeArchitecture = "x86_64"
		case "arm64", "aarch64":
			platform.Architecture = "arm64"
			platform.NativeArchitecture = "aarch64"
		default:
			return Platform{}, fmt.Errorf("%s:%d:%s.architecture: unsupported architecture %q; use amd64 or arm64", file, attr.NameRange.Start.Line, path, value)
		}
	}
	if attr, exists := block.Body.Attributes["version"]; exists {
		value, err := evalStringAttribute(file, path, "version", attr, ctx, true)
		if err != nil {
			return Platform{}, err
		}
		platform.Version = value
		platform.Branch = alpineBranch(value)
	}
	return platform, nil
}

func parseComponentInstance(file, parentPath string, block *hclsyntax.Block, ctx EvalContext) (ComponentInstance, error) {
	name, path, source, err := declarationIdentityAt(file, parentPath+".component", "component", block)
	if err != nil {
		return ComponentInstance{}, err
	}
	if err := rejectAttributesExcept(file, path, block.Body.Attributes, "source", "inputs", "depends_on"); err != nil {
		return ComponentInstance{}, err
	}
	instance := ComponentInstance{Name: name, Inputs: map[string]Value{}, Source: source}
	sourceAttr, exists := block.Body.Attributes["source"]
	if !exists {
		return ComponentInstance{}, fmt.Errorf("%s:%d: %s.source is required", file, source.Line, path)
	}
	references, err := parseReferences(file, path+".source", sourceAttr.Expr, "component")
	if err != nil || len(references) != 1 {
		return ComponentInstance{}, fmt.Errorf("%s:%d: %s.source must be component.<name>", file, sourceAttr.NameRange.Start.Line, path)
	}
	instance.Template = references[0]
	if attr, exists := block.Body.Attributes["inputs"]; exists {
		value, err := evalValue(attr.Expr, ctx, ir.SourceRef{File: file, Line: attr.NameRange.Start.Line, Path: path + ".inputs"})
		if err != nil {
			return ComponentInstance{}, err
		}
		if !value.IsMap() {
			return ComponentInstance{}, fmt.Errorf("%s:%d: %s.inputs must be an object", file, attr.NameRange.Start.Line, path)
		}
		instance.Inputs = value.Map
	}
	if attr, exists := block.Body.Attributes["depends_on"]; exists {
		instance.DependsOn, err = parseReferences(file, path+".depends_on", attr.Expr, "component")
		if err != nil {
			return ComponentInstance{}, err
		}
	}
	for _, child := range block.Body.Blocks {
		if child.Type != "lifecycle" {
			return ComponentInstance{}, fmt.Errorf("%s:%d: unsupported block %s.%s", file, child.TypeRange.Start.Line, path, child.Type)
		}
		if instance.Lifecycle.Source.File != "" {
			return ComponentInstance{}, fmt.Errorf("%s:%d: duplicate %s.lifecycle block", file, child.TypeRange.Start.Line, path)
		}
		lifecycle, err := parseLifecycle(file, path+".lifecycle", child, ctx)
		if err != nil {
			return ComponentInstance{}, err
		}
		instance.Lifecycle = lifecycle
	}
	return instance, nil
}

func parseLifecycle(file, path string, block *hclsyntax.Block, ctx EvalContext) (Lifecycle, error) {
	if len(block.Labels) != 0 || len(block.Body.Blocks) != 0 {
		return Lifecycle{}, fmt.Errorf("%s:%d: %s must be an unlabeled attribute-only block", file, block.TypeRange.Start.Line, path)
	}
	if err := rejectAttributesExcept(file, path, block.Body.Attributes, "prevent_destroy"); err != nil {
		return Lifecycle{}, err
	}
	lifecycle := Lifecycle{Source: ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path}}
	if attr, exists := block.Body.Attributes["prevent_destroy"]; exists {
		value, err := evalBoolAttribute(file, path, "prevent_destroy", attr, ctx)
		if err != nil {
			return Lifecycle{}, err
		}
		lifecycle.PreventDestroy = value
	}
	return lifecycle, nil
}

func parseScript(file string, block *hclsyntax.Block, ctx EvalContext) (Script, error) {
	return parseScriptAt(file, "", block, ctx)
}

func parseScriptAt(file, parentPath string, block *hclsyntax.Block, ctx EvalContext) (Script, error) {
	var name, path string
	var source ir.SourceRef
	var err error
	if parentPath == "" {
		name, path, source, err = declarationIdentity(file, "script", block)
	} else {
		name, path, source, err = declarationIdentityAt(file, parentPath+".script", "script", block)
	}
	if err != nil {
		return Script{}, err
	}
	if len(block.Body.Blocks) != 0 {
		return Script{}, fmt.Errorf("%s:%d: %s does not support nested blocks yet", file, block.Body.Blocks[0].TypeRange.Start.Line, path)
	}
	if err := rejectAttributesExcept(file, path, block.Body.Attributes, "description", "commands", "interpreter", "content", "outputs", "sensitive"); err != nil {
		return Script{}, err
	}
	script := Script{Name: name, Attributes: map[string]ResourceAttribute{}, Source: source}
	if attr, exists := block.Body.Attributes["description"]; exists {
		script.Description, err = evalStringAttribute(file, path, "description", attr, ctx, false)
		if err != nil {
			return Script{}, err
		}
	}
	if attr, exists := block.Body.Attributes["sensitive"]; exists {
		script.Sensitive, err = evalBoolAttribute(file, path, "sensitive", attr, ctx)
		if err != nil {
			return Script{}, err
		}
	}
	for _, attributeName := range []string{"commands", "interpreter", "content", "outputs"} {
		if attr, exists := block.Body.Attributes[attributeName]; exists {
			script.Attributes[attributeName] = ResourceAttribute{
				Expression: attr.Expr,
				Source:     ir.SourceRef{File: file, Line: attr.NameRange.Start.Line, Path: path + "." + attributeName},
			}
		}
	}
	if parentPath == "" {
		for _, attribute := range script.Attributes {
			for _, traversal := range attribute.Expression.Variables() {
				if root, ok := traversal[0].(hcl.TraverseRoot); ok && root.Name == "input" {
					return Script{}, fmt.Errorf("%s:%d:%s: top-level script cannot reference component input.*", file, traversal.SourceRange().Start.Line, path)
				}
			}
		}
	}
	return script, nil
}

func parseAssert(file, path string, block *hclsyntax.Block, ctx EvalContext) (Assert, error) {
	if len(block.Labels) != 0 || len(block.Body.Blocks) != 0 {
		return Assert{}, fmt.Errorf("%s:%d: %s must be an unlabeled attribute-only block", file, block.TypeRange.Start.Line, path)
	}
	if err := rejectAttributesExcept(file, path, block.Body.Attributes, "condition", "error_message"); err != nil {
		return Assert{}, err
	}
	condition, exists := block.Body.Attributes["condition"]
	if !exists {
		return Assert{}, fmt.Errorf("%s:%d: %s.condition is required", file, block.TypeRange.Start.Line, path)
	}
	message, exists := block.Body.Attributes["error_message"]
	if !exists {
		return Assert{}, fmt.Errorf("%s:%d: %s.error_message is required", file, block.TypeRange.Start.Line, path)
	}
	messageSource := ir.SourceRef{File: file, Line: message.NameRange.Start.Line, Path: path + ".error_message"}
	messageValue, err := evalValue(message.Expr, ctx, messageSource)
	if err != nil {
		return Assert{}, err
	}
	if messageValue.Kind != KindString || messageValue.String == "" {
		return Assert{}, fmt.Errorf("%s:%d:%s: error_message must be a non-empty string", file, messageSource.Line, messageSource.Path)
	}
	if messageValue.ContainsSensitive() || messageValue.ContainsEphemeral() {
		return Assert{}, fmt.Errorf("%s:%d:%s: error_message must not use sensitive or ephemeral values", file, messageSource.Line, messageSource.Path)
	}
	return Assert{
		Condition:       condition.Expr,
		ConditionSource: ir.SourceRef{File: file, Line: condition.NameRange.Start.Line, Path: path + ".condition"},
		Message:         messageValue.String,
		MessageSource:   messageSource,
		Source:          ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path},
	}, nil
}

func parseComponentInputValidationBlock(file, path string, block *hclsyntax.Block, ctx EvalContext) (ComponentInputValidation, error) {
	validation, err := parseVariableValidationBlock(file, path, block, ctx)
	if err != nil {
		return ComponentInputValidation{}, err
	}
	return ComponentInputValidation{
		Source:          validation.Source,
		Condition:       validation.Condition,
		ConditionSource: validation.ConditionSource,
		Message:         validation.Message,
		MessageSource:   validation.MessageSource,
	}, nil
}

func evalContextForConfig(cfg *Config, moduleDir string) (EvalContext, error) {
	variables, err := variableNamespaceValue(cfg)
	if err != nil {
		return EvalContext{}, err
	}
	return EvalContext{ModuleDir: moduleDir, Locals: cfg.Locals, Variables: map[string]cty.Value{"var": variables}}, nil
}

func EvaluateExpression(expr hcl.Expression, ctx EvalContext, source ir.SourceRef) (Value, error) {
	return evalValue(expr, ctx, source)
}

func declarationIdentity(file, kind string, block *hclsyntax.Block) (string, string, ir.SourceRef, error) {
	return declarationIdentityAt(file, kind, kind, block)
}

func declarationIdentityAt(file, pathPrefix, kind string, block *hclsyntax.Block) (string, string, ir.SourceRef, error) {
	if len(block.Labels) != 1 {
		return "", "", ir.SourceRef{}, fmt.Errorf("%s:%d: %s block requires exactly one label", file, block.TypeRange.Start.Line, kind)
	}
	name := block.Labels[0]
	if !declarationLabelPattern.MatchString(name) {
		return "", "", ir.SourceRef{}, fmt.Errorf("%s:%d: %s label %q must match %s", file, block.TypeRange.Start.Line, kind, name, declarationLabelPattern.String())
	}
	path := fmt.Sprintf("%s[%s]", pathPrefix, strconv.Quote(name))
	return name, path, ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path}, nil
}

func duplicateDeclarationError(kind, name string, current, previous ir.SourceRef) error {
	return fmt.Errorf("%s:%d: duplicate %s %q; first defined at %s:%d", current.File, current.Line, kind, name, previous.File, previous.Line)
}

func rejectAttributesExcept(file, path string, attributes hclsyntax.Attributes, allowed ...string) error {
	set := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		set[name] = true
	}
	for name, attr := range attributes {
		if !set[name] {
			return fmt.Errorf("%s:%d: unsupported attribute %s.%s", file, attr.NameRange.Start.Line, path, name)
		}
	}
	return nil
}

func parseReferences(file, path string, expr hcl.Expression, rootName string) ([]string, error) {
	items, diags := hcl.ExprList(expr)
	if diags.HasErrors() {
		items = []hcl.Expression{expr}
	}
	refs := make([]string, 0, len(items))
	for _, item := range items {
		traversal, diags := hcl.AbsTraversalForExpr(item)
		if diags.HasErrors() || len(traversal) != 2 {
			return nil, fmt.Errorf("%s:%d:%s: reference must be %s.<name>", file, item.Range().Start.Line, path, rootName)
		}
		root, rootOK := traversal[0].(hcl.TraverseRoot)
		name, nameOK := traversal[1].(hcl.TraverseAttr)
		if !rootOK || root.Name != rootName || !nameOK || !declarationLabelPattern.MatchString(name.Name) {
			return nil, fmt.Errorf("%s:%d:%s: reference must be %s.<name>", file, item.Range().Start.Line, path, rootName)
		}
		refs = append(refs, name.Name)
	}
	return refs, nil
}

func rejectDuplicateInstance(instances []ComponentInstance, candidate ComponentInstance) error {
	for _, instance := range instances {
		if instance.Name == candidate.Name {
			return duplicateDeclarationError("component instance", candidate.Name, candidate.Source, instance.Source)
		}
	}
	return nil
}

func alpineBranch(version string) string {
	for i, r := range version {
		if r == '.' {
			for j, next := range version[i+1:] {
				if next == '.' {
					return version[:i+1+j]
				}
			}
			return version
		}
	}
	return version
}

func NormalizeComponentInputValue(input ComponentInput, value Value) (Value, error) {
	if value.Kind == KindNull && !input.Nullable {
		return Value{}, fmt.Errorf("%s:%d:%s: component input %q must not be null", value.Source.File, value.Source.Line, value.Source.Path, input.Name)
	}
	normalized, err := normalizeValueForType(input.Name, input.TypeSpec, value, "")
	if err != nil {
		return Value{}, err
	}
	if input.Sensitive {
		normalized.Sensitive = true
	}
	if input.Ephemeral {
		normalized.Ephemeral = true
	}
	return normalized, nil
}

func sortedComponentInputNames(inputs map[string]ComponentInput) []string {
	names := make([]string, 0, len(inputs))
	for name := range inputs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

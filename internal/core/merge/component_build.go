package merge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
)

const (
	defaultBuildWorkingDirectory = "."
	defaultBuildMaxOutputBytes   = int64(64 * 1024 * 1024)
	maxInlineBuildInputBytes     = int64(64 * 1024 * 1024)
)

var (
	buildEnvironmentKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	buildInputNamePattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	buildForbiddenEnvironment  = map[string]bool{
		"BASH_ENV": true, "CDPATH": true, "ENV": true, "HOME": true, "IFS": true,
		"LD_LIBRARY_PATH": true, "LD_PRELOAD": true, "PATH": true, "SHELLOPTS": true,
		"TMPDIR": true,
	}
)

func compileComponentBuild(template parser.Component, instance parser.ComponentInstance, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext, install *ir.ComponentArtifactInstallSpec) (*ir.ComponentBuildSpec, error) {
	if template.Build == nil {
		return nil, nil
	}
	build := *template.Build
	if install == nil {
		return nil, buildError(build.Source, "source build requires an install block")
	}
	workingDirectory, err := buildStringDefault(build.Attributes, "working_directory", defaultBuildWorkingDirectory, ctx, build.Source)
	if err != nil {
		return nil, err
	}
	if err := validateBuildRelativePath(workingDirectory, true); err != nil {
		return nil, buildAttributeError(build.Attributes, "working_directory", build.Source, "%v", err)
	}
	output, err := buildStringDefault(build.Attributes, "output", "", ctx, build.Source)
	if err != nil {
		return nil, err
	}
	if output == "" {
		return nil, buildAttributeError(build.Attributes, "output", build.Source, "is required")
	}
	if err := validateBuildRelativePath(output, false); err != nil {
		return nil, buildAttributeError(build.Attributes, "output", build.Source, "%v", err)
	}
	outputSHA, err := buildStringDefault(build.Attributes, "output_sha256", "", ctx, build.Source)
	if err != nil {
		return nil, err
	}
	outputSHA = strings.ToLower(outputSHA)
	if outputSHA != "" && !componentSHA256Pattern.MatchString(outputSHA) {
		return nil, buildAttributeError(build.Attributes, "output_sha256", build.Source, "must be exactly 64 hexadecimal characters")
	}
	maxOutputBytes, err := buildIntDefault(build.Attributes, "max_output_bytes", defaultBuildMaxOutputBytes, ctx, build.Source)
	if err != nil {
		return nil, err
	}
	if maxOutputBytes < 1 || maxOutputBytes > 1024*1024*1024 {
		return nil, buildAttributeError(build.Attributes, "max_output_bytes", build.Source, "must be between 1 and 1073741824")
	}
	network, err := buildStringDefault(build.Attributes, "network", "none", ctx, build.Source)
	if err != nil {
		return nil, err
	}
	if network != "none" {
		return nil, buildAttributeError(build.Attributes, "network", build.Source, "must be \"none\"; network-enabled target builds are unsupported")
	}
	onRemove, err := buildStringDefault(build.Attributes, "on_remove", "forget", ctx, build.Source)
	if err != nil {
		return nil, err
	}
	if onRemove != "forget" && onRemove != "destroy" {
		return nil, buildAttributeError(build.Attributes, "on_remove", build.Source, "must be \"forget\" or \"destroy\"")
	}
	dependencies, err := buildStringList(build.Attributes, "dependencies", ctx, build.Source, true)
	if err != nil {
		return nil, err
	}
	dependencies = sortedDistinct(append(dependencies, "bubblewrap"))
	for _, dependency := range dependencies {
		if !apkPackageNamePattern.MatchString(dependency) {
			return nil, buildAttributeError(build.Attributes, "dependencies", build.Source, "package %q must be an unversioned APK package name", dependency)
		}
	}
	environment, environmentNames, environmentProtected, environmentVersion, environmentSensitive, environmentEphemeral, err := compileBuildEnvironment(build, ctx)
	if err != nil {
		return nil, err
	}
	inputs, err := compileBuildInputs(build, ctx, output)
	if err != nil {
		return nil, err
	}
	commands, commandSensitive, commandEphemeral, err := compileBuildCommands(build, ctx)
	if err != nil {
		return nil, err
	}
	identityDocument := struct {
		Template           string
		Instance           string
		Inputs             []any
		Commands           []any
		WorkingDirectory   string
		Environment        []any
		EnvironmentVersion string
		Output             string
		OutputSHA256       string
		MaxOutputBytes     int64
		Dependencies       []string
		Network            string
		Install            any
		Platform           any
	}{
		Template: template.Name, Instance: instance.Name, WorkingDirectory: workingDirectory,
		EnvironmentVersion: environmentVersion, Output: output, OutputSHA256: outputSHA,
		MaxOutputBytes: maxOutputBytes, Dependencies: dependencies, Network: network,
		Install:  struct{ Path, Owner, Group, Mode string }{install.Path, install.Owner, install.Group, install.Mode},
		Platform: buildPlatformIdentity(host, facts),
	}
	for _, input := range inputs {
		identity := input.SHA256
		if input.Sensitive || input.Ephemeral {
			identity = "version:" + input.ContentVersion
		}
		extractFormat := ""
		stripComponents := 0
		if input.Extract != nil {
			extractFormat = input.Extract.Format
			stripComponents = input.Extract.StripComponents
		}
		identityDocument.Inputs = append(identityDocument.Inputs, struct {
			Name, Kind, Identity, Destination, ExtractFormat string
			StripComponents                                  int
		}{input.Name, input.Kind, identity, input.Destination, extractFormat, stripComponents})
	}
	for _, command := range commands {
		stdinIdentity := command.StdinSHA256
		if command.Sensitive || command.Ephemeral {
			stdinIdentity = "version:" + command.StdinVersion
		}
		identityDocument.Commands = append(identityDocument.Commands, struct {
			Argv          []string
			StdinIdentity string
		}{command.Argv, stdinIdentity})
	}
	for _, name := range environmentNames {
		value := environment[name]
		if environmentProtected[name] {
			value = "<protected>"
		}
		identityDocument.Environment = append(identityDocument.Environment, []string{name, value})
	}
	encoded, _ := json.Marshal(identityDocument)
	digest := sha256.Sum256(encoded)
	return &ir.ComponentBuildSpec{
		Identity: hex.EncodeToString(digest[:]), Inputs: inputs, Commands: commands,
		WorkingDirectory: workingDirectory, Environment: environment, EnvironmentNames: environmentNames,
		EnvironmentVersion: environmentVersion, Output: output, OutputSHA256: outputSHA,
		MaxOutputBytes: maxOutputBytes, Dependencies: dependencies, Network: network, OnRemove: onRemove,
		Sensitive: environmentSensitive || commandSensitive || buildInputsSensitive(inputs),
		Ephemeral: environmentEphemeral || commandEphemeral || buildInputsEphemeral(inputs), Source: build.Source,
	}, nil
}

func compileBuildInputs(build parser.ComponentBuild, ctx parser.EvalContext, output string) ([]ir.ComponentBuildInputSpec, error) {
	if len(build.Inputs) == 0 {
		return nil, buildError(build.Source, "requires at least one checksummed input")
	}
	inputs := make([]ir.ComponentBuildInputSpec, 0, len(build.Inputs))
	destinations := map[string]ir.SourceRef{}
	for _, declaration := range build.Inputs {
		if !buildInputNamePattern.MatchString(declaration.Name) {
			return nil, buildError(declaration.Source, "input name %q must contain only letters, digits, dot, underscore, or hyphen", declaration.Name)
		}
		destination, err := buildStringDefault(declaration.Attributes, "destination", "", ctx, declaration.Source)
		if err != nil {
			return nil, err
		}
		if destination == "" {
			return nil, buildAttributeError(declaration.Attributes, "destination", declaration.Source, "is required")
		}
		if err := validateBuildRelativePath(destination, false); err != nil {
			return nil, buildAttributeError(declaration.Attributes, "destination", declaration.Source, "%v", err)
		}
		if buildPathsOverlap(destination, output) {
			return nil, buildAttributeError(declaration.Attributes, "destination", declaration.Source, "must not overlap declared output %q", output)
		}
		for existing, previous := range destinations {
			if buildPathsOverlap(destination, existing) {
				return nil, buildError(declaration.Source, "destination %q overlaps input destination %q declared at %s:%d", destination, existing, previous.File, previous.Line)
			}
		}
		destinations[destination] = declaration.Source
		shaValue, err := buildStringDefault(declaration.Attributes, "sha256", "", ctx, declaration.Source)
		if err != nil {
			return nil, err
		}
		shaValue = strings.ToLower(shaValue)
		if !componentSHA256Pattern.MatchString(shaValue) {
			return nil, buildAttributeError(declaration.Attributes, "sha256", declaration.Source, "is required and must be exactly 64 hexadecimal characters")
		}
		sources := []string{"source", "url", "content"}
		selected := ""
		for _, name := range sources {
			if _, exists := declaration.Attributes[name]; exists {
				if selected != "" {
					return nil, buildError(declaration.Source, "input source, url, and content are mutually exclusive")
				}
				selected = name
			}
		}
		if selected == "" {
			return nil, buildError(declaration.Source, "input requires exactly one of source, url, or content")
		}
		spec := ir.ComponentBuildInputSpec{Name: declaration.Name, Kind: selected, Destination: destination, SHA256: shaValue, PayloadSHA256: shaValue, Source: declaration.Source}
		if declaration.Extract != nil {
			format, formatErr := buildStringDefault(declaration.Extract.Attributes, "format", "tar.gz", ctx, declaration.Extract.Source)
			if formatErr != nil {
				return nil, formatErr
			}
			if format != "tar.gz" {
				return nil, buildAttributeError(declaration.Extract.Attributes, "format", declaration.Extract.Source, "must be \"tar.gz\"")
			}
			strip, stripErr := buildIntDefault(declaration.Extract.Attributes, "strip_components", 0, ctx, declaration.Extract.Source)
			if stripErr != nil {
				return nil, stripErr
			}
			if strip < 0 || strip > 1024 {
				return nil, buildAttributeError(declaration.Extract.Attributes, "strip_components", declaration.Extract.Source, "must be between 0 and 1024")
			}
			spec.Extract = &ir.ComponentBuildInputExtractSpec{Format: format, StripComponents: int(strip), Source: declaration.Extract.Source}
		}
		switch selected {
		case "url":
			value, valueErr := buildStringDefault(declaration.Attributes, "url", "", ctx, declaration.Source)
			if valueErr != nil {
				return nil, valueErr
			}
			parsed, parseErr := url.Parse(value)
			if parseErr != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.Fragment != "" {
				return nil, buildAttributeError(declaration.Attributes, "url", declaration.Source, "must be an absolute http(s) URL without credentials or a fragment")
			}
			spec.URL = value
		case "source":
			value, valueErr := buildStringDefault(declaration.Attributes, "source", "", ctx, declaration.Source)
			if valueErr != nil {
				return nil, valueErr
			}
			content, resolved, readErr := readBuildSource(declaration.Source, value)
			if readErr != nil {
				return nil, readErr
			}
			spec.SourcePath, spec.Content = resolved, content
		case "content":
			value, valueErr := evaluateBuildAttribute(declaration.Attributes, "content", ctx, declaration.Source)
			if valueErr != nil {
				return nil, valueErr
			}
			if value.Kind != parser.KindString {
				return nil, buildAttributeError(declaration.Attributes, "content", declaration.Source, "must evaluate to a string")
			}
			spec.Content = []byte(value.String)
			spec.Sensitive, spec.Ephemeral = value.ContainsSensitive(), value.ContainsEphemeral()
			if spec.Sensitive || spec.Ephemeral {
				version, versionErr := buildStringDefault(declaration.Attributes, "content_version", "", ctx, declaration.Source)
				if versionErr != nil {
					return nil, versionErr
				}
				if version == "" {
					return nil, buildAttributeError(declaration.Attributes, "content_version", declaration.Source, "is required for protected content")
				}
				spec.ContentVersion = version
				spec.SHA256 = ""
			}
		}
		if int64(len(spec.Content)) > maxInlineBuildInputBytes {
			return nil, buildError(declaration.Source, "input content exceeds %d bytes", maxInlineBuildInputBytes)
		}
		if len(spec.Content) != 0 || selected == "content" || selected == "source" {
			actual := sha256.Sum256(spec.Content)
			if hex.EncodeToString(actual[:]) != shaValue {
				return nil, buildError(declaration.Source, "input checksum mismatch before execution")
			}
		}
		inputs = append(inputs, spec)
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Name < inputs[j].Name })
	return inputs, nil
}

func compileBuildCommands(build parser.ComponentBuild, ctx parser.EvalContext) ([]ir.ComponentBuildCommandSpec, bool, bool, error) {
	if len(build.Commands) == 0 {
		return nil, false, false, buildError(build.Source, "requires at least one command block")
	}
	commands := make([]ir.ComponentBuildCommandSpec, 0, len(build.Commands))
	var sensitive, ephemeral bool
	for _, declaration := range build.Commands {
		value, err := evaluateBuildAttribute(declaration.Attributes, "argv", ctx, declaration.Source)
		if err != nil {
			return nil, false, false, err
		}
		if value.ContainsSensitive() || value.ContainsEphemeral() {
			return nil, false, false, buildAttributeError(declaration.Attributes, "argv", declaration.Source, "must not contain sensitive or ephemeral values")
		}
		argv, err := buildValueStringList(value, "argv", false)
		if err != nil {
			return nil, false, false, buildAttributeError(declaration.Attributes, "argv", declaration.Source, "%v", err)
		}
		if err := validateBuildExecutable(argv[0]); err != nil {
			return nil, false, false, buildAttributeError(declaration.Attributes, "argv", declaration.Source, "%v", err)
		}
		command := ir.ComponentBuildCommandSpec{Argv: argv, Source: declaration.Source}
		if _, exists := declaration.Attributes["stdin"]; exists {
			stdin, stdinErr := evaluateBuildAttribute(declaration.Attributes, "stdin", ctx, declaration.Source)
			if stdinErr != nil {
				return nil, false, false, stdinErr
			}
			if stdin.Kind != parser.KindString {
				return nil, false, false, buildAttributeError(declaration.Attributes, "stdin", declaration.Source, "must evaluate to a string")
			}
			command.Stdin = []byte(stdin.String)
			command.Sensitive, command.Ephemeral = stdin.ContainsSensitive(), stdin.ContainsEphemeral()
			digest := sha256.Sum256(command.Stdin)
			command.StdinSHA256 = hex.EncodeToString(digest[:])
			if command.Sensitive || command.Ephemeral {
				version, versionErr := buildStringDefault(declaration.Attributes, "stdin_version", "", ctx, declaration.Source)
				if versionErr != nil {
					return nil, false, false, versionErr
				}
				if version == "" {
					return nil, false, false, buildAttributeError(declaration.Attributes, "stdin_version", declaration.Source, "is required for protected stdin")
				}
				command.StdinVersion, command.StdinSHA256 = version, ""
			}
		} else if _, exists := declaration.Attributes["stdin_version"]; exists {
			return nil, false, false, buildAttributeError(declaration.Attributes, "stdin_version", declaration.Source, "requires stdin")
		}
		sensitive = sensitive || command.Sensitive
		ephemeral = ephemeral || command.Ephemeral
		commands = append(commands, command)
	}
	return commands, sensitive, ephemeral, nil
}

func compileBuildEnvironment(build parser.ComponentBuild, ctx parser.EvalContext) (map[string]string, []string, map[string]bool, string, bool, bool, error) {
	environment := map[string]string{}
	protected := map[string]bool{}
	var sensitive, ephemeral bool
	if _, exists := build.Attributes["environment"]; exists {
		value, err := evaluateBuildAttribute(build.Attributes, "environment", ctx, build.Source)
		if err != nil {
			return nil, nil, nil, "", false, false, err
		}
		if value.Kind != parser.KindMap {
			return nil, nil, nil, "", false, false, buildAttributeError(build.Attributes, "environment", build.Source, "must evaluate to a map of strings")
		}
		for name, item := range value.Map {
			if !buildEnvironmentKeyPattern.MatchString(name) || buildForbiddenEnvironment[name] {
				return nil, nil, nil, "", false, false, buildAttributeError(build.Attributes, "environment", build.Source, "key %q is not allowed", name)
			}
			if item.Kind != parser.KindString || strings.ContainsRune(item.String, '\x00') || strings.ContainsAny(item.String, "\r\n") {
				return nil, nil, nil, "", false, false, buildAttributeError(build.Attributes, "environment", build.Source, "value for %q must be a single-line string", name)
			}
			environment[name] = item.String
			sensitive = sensitive || item.ContainsSensitive()
			ephemeral = ephemeral || item.ContainsEphemeral()
			protected[name] = item.ContainsSensitive() || item.ContainsEphemeral()
		}
	}
	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	sort.Strings(names)
	version, err := buildStringDefault(build.Attributes, "environment_version", "", ctx, build.Source)
	if err != nil {
		return nil, nil, nil, "", false, false, err
	}
	if (sensitive || ephemeral) && version == "" {
		return nil, nil, nil, "", false, false, buildAttributeError(build.Attributes, "environment_version", build.Source, "is required for a protected environment")
	}
	if !sensitive && !ephemeral && version != "" {
		return nil, nil, nil, "", false, false, buildAttributeError(build.Attributes, "environment_version", build.Source, "is only valid for a protected environment")
	}
	return environment, names, protected, version, sensitive, ephemeral, nil
}

func readBuildSource(source ir.SourceRef, value string) ([]byte, string, error) {
	if value == "" || filepath.IsAbs(value) || filepath.Clean(value) != value || value == "." || strings.ContainsAny(value, "\x00\r\n") {
		return nil, "", buildError(source, "source must be a clean relative file path")
	}
	base, err := filepath.EvalSymlinks(filepath.Dir(source.File))
	if err != nil {
		return nil, "", buildError(source, "resolve component directory: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(base, value))
	if err != nil {
		return nil, "", buildError(source, "resolve source file: %v", err)
	}
	relative, err := filepath.Rel(base, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, "", buildError(source, "source file escapes the component directory")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", buildError(source, "source must resolve to a regular file")
	}
	if info.Size() > maxInlineBuildInputBytes {
		return nil, "", buildError(source, "source file exceeds %d bytes", maxInlineBuildInputBytes)
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return nil, "", buildError(source, "read source file: %v", err)
	}
	return content, resolved, nil
}

func buildStringDefault(attributes map[string]parser.ResourceAttribute, name, fallback string, ctx parser.EvalContext, parent ir.SourceRef) (string, error) {
	attribute, exists := attributes[name]
	if !exists {
		return fallback, nil
	}
	value, err := parser.EvaluateExpression(attribute.Expression, ctx, attribute.Source)
	if err != nil {
		return "", buildAttributeError(attributes, name, parent, "evaluate: %v", err)
	}
	if value.Kind != parser.KindString || value.ContainsSensitive() || value.ContainsEphemeral() || strings.ContainsRune(value.String, '\x00') {
		return "", buildAttributeError(attributes, name, parent, "must evaluate to a non-protected string")
	}
	return value.String, nil
}

func buildIntDefault(attributes map[string]parser.ResourceAttribute, name string, fallback int64, ctx parser.EvalContext, parent ir.SourceRef) (int64, error) {
	attribute, exists := attributes[name]
	if !exists {
		return fallback, nil
	}
	value, err := parser.EvaluateExpression(attribute.Expression, ctx, attribute.Source)
	if err != nil {
		return 0, buildAttributeError(attributes, name, parent, "evaluate: %v", err)
	}
	if value.Kind != parser.KindNumber || value.ContainsSensitive() || value.ContainsEphemeral() {
		return 0, buildAttributeError(attributes, name, parent, "must evaluate to a non-protected integer")
	}
	parsed, err := strconv.ParseInt(value.Number, 10, 64)
	if err != nil {
		return 0, buildAttributeError(attributes, name, parent, "must evaluate to an integer")
	}
	return parsed, nil
}

func buildStringList(attributes map[string]parser.ResourceAttribute, name string, ctx parser.EvalContext, parent ir.SourceRef, allowEmpty bool) ([]string, error) {
	attribute, exists := attributes[name]
	if !exists {
		return nil, nil
	}
	value, err := parser.EvaluateExpression(attribute.Expression, ctx, attribute.Source)
	if err != nil {
		return nil, buildAttributeError(attributes, name, parent, "evaluate: %v", err)
	}
	values, err := buildValueStringList(value, name, allowEmpty)
	if err != nil {
		return nil, buildAttributeError(attributes, name, parent, "%v", err)
	}
	return values, nil
}

func buildValueStringList(value parser.Value, name string, allowEmpty bool) ([]string, error) {
	if value.Kind != parser.KindList || value.ContainsSensitive() || value.ContainsEphemeral() || (!allowEmpty && len(value.List) == 0) {
		return nil, fmt.Errorf("%s must be a non-protected list containing strings", name)
	}
	out := make([]string, 0, len(value.List))
	for index, item := range value.List {
		if item.Kind != parser.KindString || item.String == "" || strings.ContainsRune(item.String, '\x00') {
			return nil, fmt.Errorf("%s[%d] must be a non-empty string without NUL bytes", name, index)
		}
		out = append(out, item.String)
	}
	return out, nil
}

func evaluateBuildAttribute(attributes map[string]parser.ResourceAttribute, name string, ctx parser.EvalContext, parent ir.SourceRef) (parser.Value, error) {
	attribute, exists := attributes[name]
	if !exists {
		return parser.Value{}, buildAttributeError(attributes, name, parent, "is required")
	}
	value, err := parser.EvaluateExpression(attribute.Expression, ctx, attribute.Source)
	if err != nil {
		return parser.Value{}, buildAttributeError(attributes, name, parent, "evaluate: %v", err)
	}
	return value, nil
}

func validateBuildRelativePath(value string, allowDot bool) error {
	if value == "" || filepath.IsAbs(value) || filepath.Clean(value) != value || strings.ContainsAny(value, "\x00\r\n*?[]\\") {
		return fmt.Errorf("must be a clean relative workspace path")
	}
	if !allowDot && value == "." {
		return fmt.Errorf("must name a file below the workspace")
	}
	if value == ".." || strings.HasPrefix(value, ".."+string(filepath.Separator)) {
		return fmt.Errorf("must not escape the workspace")
	}
	return nil
}

func validateBuildExecutable(value string) error {
	if value == "" || filepath.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("executable must be a bare command name or a clean workspace-relative path")
	}
	if strings.Contains(value, "/") {
		if filepath.Clean(value) != value || (value != "." && !strings.HasPrefix(value, "./")) {
			return fmt.Errorf("executable path must be relative to the workspace")
		}
	}
	return nil
}

func buildPathsOverlap(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+string(filepath.Separator)) || strings.HasPrefix(right, left+string(filepath.Separator))
}

func sortedDistinct(values []string) []string {
	sort.Strings(values)
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}

func buildPlatformIdentity(host parser.Host, facts *ir.HostFacts) any {
	if facts != nil {
		return struct{ OSID, Version, Architecture, NativeArchitecture, Libc string }{facts.OSID, facts.Version, facts.Architecture, facts.NativeArchitecture, facts.Libc}
	}
	if host.Platform != nil {
		return struct{ OSID, Version, Architecture, NativeArchitecture, Libc string }{"alpine", host.Platform.Version, host.Platform.Architecture, host.Platform.NativeArchitecture, host.Platform.Libc}
	}
	return struct{}{}
}

func buildInputsSensitive(inputs []ir.ComponentBuildInputSpec) bool {
	for _, input := range inputs {
		if input.Sensitive {
			return true
		}
	}
	return false
}

func buildInputsEphemeral(inputs []ir.ComponentBuildInputSpec) bool {
	for _, input := range inputs {
		if input.Ephemeral {
			return true
		}
	}
	return false
}

func buildError(source ir.SourceRef, format string, args ...any) error {
	return fmt.Errorf("%s:%d:%s: %s", source.File, source.Line, source.Path, fmt.Sprintf(format, args...))
}

func buildAttributeError(attributes map[string]parser.ResourceAttribute, name string, parent ir.SourceRef, format string, args ...any) error {
	if attribute, exists := attributes[name]; exists {
		return buildError(attribute.Source, format, args...)
	}
	source := parent
	source.Path += "." + name
	return buildError(source, format, args...)
}

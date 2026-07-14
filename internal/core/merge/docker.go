package merge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
	"github.com/mofelee/alpineform/internal/product"
)

const (
	dockerCommunityRepositoryName = "alpineform-docker-community"
	dockerCommunityRepositoryURL  = "https://dl-cdn.alpinelinux.org/alpine"
)

var dockerProjectNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

func compileDocker(docker parser.Docker, host parser.Host, facts *ir.HostFacts, apk *ir.APKSpec, ctx parser.EvalContext) (*ir.DockerSpec, error) {
	declaration := parser.ResourceDeclaration{Kind: "docker", Attributes: docker.Attributes, Source: docker.Source}
	ensure, err := resourceStringDefault(declaration, "ensure", "present", host, facts, ctx)
	if err != nil {
		return nil, err
	}
	if ensure != "present" && ensure != "absent" {
		return nil, resourceAttributeError(declaration, "ensure", "must be \"present\" or \"absent\"")
	}
	enabled, err := resourceBoolDefault(declaration, "enable", true, host, facts, ctx)
	if err != nil {
		return nil, err
	}
	if ensure == "absent" {
		enabled = false
	}
	packageSource, err := resourceStringDefault(declaration, "package_source", "alpine", host, facts, ctx)
	if err != nil {
		return nil, err
	}
	if packageSource != "alpine" && packageSource != "none" && packageSource != "custom" {
		return nil, resourceAttributeError(declaration, "package_source", "must be \"alpine\", \"none\", or \"custom\"")
	}
	packageRepository, repositorySet, err := resourceString(declaration, "package_repository", host, facts, ctx)
	if err != nil {
		return nil, err
	}
	if packageSource == "custom" {
		if !repositorySet || packageRepository == "" {
			return nil, resourceAttributeError(declaration, "package_repository", "is required when package_source is \"custom\"")
		}
		if !apkTagPattern.MatchString(packageRepository) || !dockerRepositoryTagExists(apk, packageRepository) {
			return nil, resourceAttributeError(declaration, "package_repository", "must reference a present tagged repository in the host apk block")
		}
	} else if repositorySet {
		return nil, resourceAttributeError(declaration, "package_repository", "is supported only when package_source is \"custom\"")
	}

	members, err := compileDockerMembers(declaration, host, facts, ctx)
	if err != nil {
		return nil, err
	}
	if _, hasConfig := declaration.Attributes["daemon_config"]; !hasConfig {
		for _, attribute := range []string{"daemon_config_version", "daemon_config_sensitive"} {
			if _, exists := declaration.Attributes[attribute]; exists {
				return nil, resourceAttributeError(declaration, attribute, "requires daemon_config")
			}
		}
	}
	out := &ir.DockerSpec{
		Ensure: ensure, Enabled: enabled, PackageSource: packageSource, PackageRepository: packageRepository,
		Members: members, Lifecycle: ir.LifecycleSpec{PreventDestroy: docker.Lifecycle.PreventDestroy, Source: docker.Lifecycle.Source}, Source: docker.Source,
	}
	if config, exists, err := compileDockerDaemonConfig(declaration, host, facts, ctx, ensure); err != nil {
		return nil, err
	} else if exists {
		out.DaemonConfig = config
	}
	seenDirectories := map[string]ir.SourceRef{}
	for _, projectDeclaration := range docker.Projects {
		project, err := compileDockerProject(projectDeclaration, host, facts, ctx)
		if err != nil {
			return nil, err
		}
		if ensure == "absent" && project.State != "absent" {
			return nil, resourceError(project.Source, "Docker project %q must use state = \"absent\" when docker.ensure is absent", project.Name)
		}
		if !enabled && project.State == "running" {
			return nil, resourceError(project.Source, "Docker project %q cannot be running when docker.enable is false", project.Name)
		}
		if previous, exists := seenDirectories[project.Directory]; exists {
			return nil, resourceError(project.Source, "Docker project directory %q duplicates the project declared at %s:%d", project.Directory, previous.File, previous.Line)
		}
		seenDirectories[project.Directory] = project.Source
		out.Projects = append(out.Projects, project)
	}
	return out, nil
}

func compileDockerMembers(declaration parser.ResourceDeclaration, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) ([]string, error) {
	value, exists, err := resourceValue(declaration, "members", host, facts, ctx)
	if err != nil || !exists {
		return nil, err
	}
	if value.Kind != parser.KindList || value.ContainsSensitive() || value.ContainsEphemeral() {
		return nil, resourceAttributeError(declaration, "members", "must evaluate to a non-protected list of Alpine user names")
	}
	seen := map[string]bool{}
	members := make([]string, 0, len(value.List))
	for _, item := range value.List {
		if item.Kind != parser.KindString || !managedAccountNamePattern.MatchString(item.String) {
			return nil, resourceAttributeError(declaration, "members", "must contain only valid Alpine user names")
		}
		if !seen[item.String] {
			seen[item.String] = true
			members = append(members, item.String)
		}
	}
	sort.Strings(members)
	return members, nil
}

func compileDockerDaemonConfig(declaration parser.ResourceDeclaration, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext, ensure string) (*ir.DockerDaemonConfigSpec, bool, error) {
	value, exists, err := resourceValue(declaration, "daemon_config", host, facts, ctx)
	if err != nil || !exists {
		return nil, false, err
	}
	if ensure == "absent" {
		return nil, false, resourceAttributeError(declaration, "daemon_config", "must not be set when docker.ensure is absent")
	}
	if value.Kind != parser.KindString {
		return nil, false, resourceAttributeError(declaration, "daemon_config", "must evaluate to a JSON string")
	}
	content, err := canonicalJSONObject(value.String)
	if err != nil {
		return nil, false, resourceAttributeError(declaration, "daemon_config", "%v", err)
	}
	version, versionSet, err := resourceString(declaration, "daemon_config_version", host, facts, ctx)
	if err != nil {
		return nil, false, err
	}
	if versionSet && version == "" {
		return nil, false, resourceAttributeError(declaration, "daemon_config_version", "must not be empty")
	}
	if value.ContainsEphemeral() && !versionSet {
		return nil, false, resourceAttributeError(declaration, "daemon_config_version", "is required when daemon_config is ephemeral")
	}
	explicitSensitive, err := resourceBoolDefault(declaration, "daemon_config_sensitive", false, host, facts, ctx)
	if err != nil {
		return nil, false, err
	}
	config := &ir.DockerDaemonConfigSpec{
		Content: content, ContentBytes: int64(len(content)), ContentVersion: version,
		ContentWriteOnly: value.ContainsEphemeral(), Sensitive: explicitSensitive || value.ContainsSensitive() || value.ContainsEphemeral(),
		Ephemeral: value.ContainsEphemeral(), Source: declaration.Attributes["daemon_config"].Source,
	}
	if !config.ContentWriteOnly {
		sum := sha256.Sum256([]byte(content))
		config.ContentSHA256 = hex.EncodeToString(sum[:])
	}
	return config, true, nil
}

func compileDockerProject(declaration parser.ResourceDeclaration, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (ir.DockerProjectSpec, error) {
	if !dockerProjectNamePattern.MatchString(declaration.Label) {
		return ir.DockerProjectSpec{}, resourceError(declaration.Source, "Docker project name %q must use lowercase letters, digits, underscore, or hyphen", declaration.Label)
	}
	directory, exists, err := resourceString(declaration, "directory", host, facts, ctx)
	if err != nil {
		return ir.DockerProjectSpec{}, err
	}
	if !exists || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || directory == "/" || directory == "/etc/docker" || pathIsWithin("/etc/docker", directory) || strings.ContainsAny(directory, "\x00\r\n") {
		return ir.DockerProjectSpec{}, resourceAttributeError(declaration, "directory", "must be a clean absolute non-root path outside /etc/docker")
	}
	compose, exists, err := resourceValue(declaration, "compose", host, facts, ctx)
	if err != nil {
		return ir.DockerProjectSpec{}, err
	}
	if !exists || compose.Kind != parser.KindString || strings.TrimSpace(compose.String) == "" || strings.ContainsRune(compose.String, '\x00') {
		return ir.DockerProjectSpec{}, resourceAttributeError(declaration, "compose", "must evaluate to a non-empty string without NUL bytes")
	}
	composeVersion, composeVersionSet, err := resourceString(declaration, "compose_version", host, facts, ctx)
	if err != nil {
		return ir.DockerProjectSpec{}, err
	}
	if composeVersionSet && composeVersion == "" {
		return ir.DockerProjectSpec{}, resourceAttributeError(declaration, "compose_version", "must not be empty")
	}
	if compose.ContainsEphemeral() && !composeVersionSet {
		return ir.DockerProjectSpec{}, resourceAttributeError(declaration, "compose_version", "is required when compose is ephemeral")
	}
	env, hasEnv, err := resourceValue(declaration, "env", host, facts, ctx)
	if err != nil {
		return ir.DockerProjectSpec{}, err
	}
	if hasEnv && (env.Kind != parser.KindString || strings.ContainsRune(env.String, '\x00')) {
		return ir.DockerProjectSpec{}, resourceAttributeError(declaration, "env", "must evaluate to a string without NUL bytes")
	}
	envVersion, envVersionSet, err := resourceString(declaration, "env_version", host, facts, ctx)
	if err != nil {
		return ir.DockerProjectSpec{}, err
	}
	if envVersionSet && envVersion == "" {
		return ir.DockerProjectSpec{}, resourceAttributeError(declaration, "env_version", "must not be empty")
	}
	if hasEnv && env.ContainsEphemeral() && !envVersionSet {
		return ir.DockerProjectSpec{}, resourceAttributeError(declaration, "env_version", "is required when env is ephemeral")
	}
	if !hasEnv && envVersionSet {
		return ir.DockerProjectSpec{}, resourceAttributeError(declaration, "env_version", "requires env")
	}
	state, err := resourceStringDefault(declaration, "state", "running", host, facts, ctx)
	if err != nil {
		return ir.DockerProjectSpec{}, err
	}
	if state != "running" && state != "stopped" && state != "absent" {
		return ir.DockerProjectSpec{}, resourceAttributeError(declaration, "state", "must be \"running\", \"stopped\", or \"absent\"")
	}
	onRemove, err := resourceStringDefault(declaration, "on_remove", "forget", host, facts, ctx)
	if err != nil {
		return ir.DockerProjectSpec{}, err
	}
	if onRemove != "forget" && onRemove != "destroy" {
		return ir.DockerProjectSpec{}, resourceAttributeError(declaration, "on_remove", "must be \"forget\" or \"destroy\"")
	}
	explicitSensitive, err := resourceBoolDefault(declaration, "sensitive", false, host, facts, ctx)
	if err != nil {
		return ir.DockerProjectSpec{}, err
	}
	project := ir.DockerProjectSpec{
		Name: declaration.Label, Directory: directory, Compose: compose.String, ComposeBytes: int64(len(compose.String)), ComposeVersion: composeVersion,
		ComposeWriteOnly: compose.ContainsEphemeral(), Env: env.String, EnvBytes: int64(len(env.String)), EnvVersion: envVersion,
		EnvWriteOnly: hasEnv && env.ContainsEphemeral(), HasEnv: hasEnv, State: state, OnRemove: onRemove,
		Sensitive: explicitSensitive || compose.ContainsSensitive() || compose.ContainsEphemeral() || env.ContainsSensitive() || env.ContainsEphemeral(),
		Ephemeral: compose.ContainsEphemeral() || env.ContainsEphemeral(),
		Lifecycle: ir.LifecycleSpec{PreventDestroy: declaration.Lifecycle.PreventDestroy, Source: declaration.Lifecycle.Source}, Source: declaration.Source,
	}
	if !project.ComposeWriteOnly {
		sum := sha256.Sum256([]byte(project.Compose))
		project.ComposeSHA256 = hex.EncodeToString(sum[:])
	}
	if project.HasEnv && !project.EnvWriteOnly {
		sum := sha256.Sum256([]byte(project.Env))
		project.EnvSHA256 = hex.EncodeToString(sum[:])
	}
	return project, nil
}

func canonicalJSONObject(content string) (string, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("must contain valid JSON: %w", err)
	}
	if _, ok := value.(map[string]any); !ok {
		return "", fmt.Errorf("must contain a JSON object")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", fmt.Errorf("must contain exactly one JSON value")
	}
	canonical, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", fmt.Errorf("canonicalize JSON: %w", err)
	}
	return string(canonical) + "\n", nil
}

func dockerRepositoryTagExists(apk *ir.APKSpec, tag string) bool {
	if apk == nil {
		return false
	}
	for _, repository := range apk.Repositories {
		if repository.Tag == tag && repository.Ensure == "present" {
			return true
		}
	}
	return false
}

func ensureDockerAPK(apk *ir.APKSpec, docker *ir.DockerSpec, host parser.Host, facts *ir.HostFacts) (*ir.APKSpec, error) {
	if docker == nil || docker.PackageSource != "alpine" {
		return apk, nil
	}
	branch := strings.TrimPrefix(targetAPKBranch(host, facts), "v")
	if branch == "" {
		return nil, resourceError(docker.Source, "docker.package_source = \"alpine\" requires detected target facts or host platform.version")
	}
	if branch != strings.TrimPrefix(product.SupportedBranch, "v") {
		return nil, resourceError(docker.Source, "Docker packages are supported only on Alpine %s", product.SupportedBranch)
	}
	line := dockerCommunityRepositoryURL + "/v" + branch + "/community"
	if apk == nil {
		apk = &ir.APKSpec{Ownership: "managed", Source: docker.Source}
	}
	for _, repository := range apk.Repositories {
		if repository.Ensure == "present" && repository.Line == line {
			return apk, nil
		}
	}
	if apk.Ownership == "authoritative" {
		return nil, resourceError(docker.Source, "authoritative apk ownership must explicitly include %s", line)
	}
	for _, repository := range apk.Repositories {
		if repository.Name == dockerCommunityRepositoryName {
			return nil, resourceError(docker.Source, "reserved Docker APK repository name %q conflicts with a host declaration", dockerCommunityRepositoryName)
		}
	}
	apk.Repositories = append(apk.Repositories, ir.APKRepositorySpec{
		Name: dockerCommunityRepositoryName, URL: dockerCommunityRepositoryURL, Branch: branch, Component: "community", Line: line,
		Ensure: "present", Source: docker.Source,
	})
	return apk, nil
}

func integrateDockerNativeResources(host *ir.HostSpec) error {
	if host.Docker == nil {
		return nil
	}
	docker := host.Docker
	for _, pkg := range host.Packages {
		if pkg.Name == "docker" || pkg.Name == "docker-cli-compose" {
			return resourceError(docker.Source, "Docker owns package %q; remove the duplicate host package declaration", pkg.Name)
		}
	}
	for _, group := range host.Groups {
		if group.Name == "docker" {
			return resourceError(docker.Source, "Docker owns group %q; remove the duplicate host group declaration", group.Name)
		}
	}
	for _, service := range host.Services {
		if service.Name == "docker" {
			return resourceError(docker.Source, "Docker owns OpenRC service %q; remove the duplicate host service declaration", service.Name)
		}
	}
	for _, file := range host.Files {
		if file.Path == "/etc/docker/daemon.json" {
			return resourceError(docker.Source, "Docker owns %s; remove the duplicate host file declaration", file.Path)
		}
		for _, project := range docker.Projects {
			if file.Path == filepath.Join(project.Directory, "compose.yaml") || file.Path == filepath.Join(project.Directory, ".env") {
				return resourceError(docker.Source, "Docker project %q owns %s; remove the duplicate host file declaration", project.Name, file.Path)
			}
		}
	}
	if docker.PackageSource != "none" {
		worldSuffix := ""
		if docker.PackageRepository != "" {
			worldSuffix = "@" + docker.PackageRepository
		}
		for _, name := range []string{"docker", "docker-cli-compose"} {
			host.Packages = append(host.Packages, ir.PackageSpec{
				Name: name, RepositoryTag: docker.PackageRepository, WorldIntent: name + worldSuffix, Ensure: docker.Ensure,
				Lifecycle: docker.Lifecycle, Source: docker.Source,
			})
		}
	}
	if docker.Ensure == "present" {
		host.Groups = append(host.Groups, ir.ManagedGroupSpec{
			Name: "docker", System: true, Ensure: "present", OnRemove: "forget", Lifecycle: docker.Lifecycle, Source: docker.Source,
		})
	}
	for _, member := range docker.Members {
		for _, user := range host.Users {
			if user.Name == member && user.Ensure == "absent" {
				return resourceError(docker.Source, "Docker member %q is declared absent", member)
			}
		}
	}
	return nil
}

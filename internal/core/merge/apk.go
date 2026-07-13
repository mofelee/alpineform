package merge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
	"github.com/mofelee/alpineform/internal/product"
)

var (
	apkRepositoryNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	apkComponentPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	apkTagPattern            = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	apkKeyFilenamePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._+-]{0,127}$`)
	apkSHA256Pattern         = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func compileAPK(declaration parser.APK, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (*ir.APKSpec, error) {
	out := &ir.APKSpec{Ownership: declaration.Ownership, Source: declaration.Source}
	for _, repositoryDeclaration := range declaration.Repositories {
		repository, err := compileAPKRepository(repositoryDeclaration, host, facts, ctx)
		if err != nil {
			return nil, err
		}
		out.Repositories = append(out.Repositories, repository)
	}
	for _, keyDeclaration := range declaration.Keys {
		key, err := compileAPKKey(keyDeclaration, host, facts, ctx)
		if err != nil {
			return nil, err
		}
		out.Keys = append(out.Keys, key)
	}
	return out, nil
}

func compileAPKRepository(declaration parser.ResourceDeclaration, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (ir.APKRepositorySpec, error) {
	if !apkRepositoryNamePattern.MatchString(declaration.Label) {
		return ir.APKRepositorySpec{}, resourceError(declaration.Source, "repository label %q must contain only letters, digits, dot, underscore, and hyphen", declaration.Label)
	}
	ensure, err := resourceStringDefault(declaration, "ensure", "present", host, facts, ctx)
	if err != nil {
		return ir.APKRepositorySpec{}, err
	}
	if ensure != "present" && ensure != "absent" {
		return ir.APKRepositorySpec{}, resourceAttributeError(declaration, "ensure", "must be \"present\" or \"absent\"")
	}
	baseURL, exists, err := resourceString(declaration, "url", host, facts, ctx)
	if err != nil {
		return ir.APKRepositorySpec{}, err
	}
	if !exists || strings.TrimSpace(baseURL) == "" {
		return ir.APKRepositorySpec{}, resourceAttributeError(declaration, "url", "is required")
	}
	baseURL, err = normalizeAPKRepositoryURL(baseURL)
	if err != nil {
		return ir.APKRepositorySpec{}, resourceAttributeError(declaration, "url", "%v", err)
	}
	branch, branchSet, err := resourceString(declaration, "branch", host, facts, ctx)
	if err != nil {
		return ir.APKRepositorySpec{}, err
	}
	if !branchSet {
		branch = targetAPKBranch(host, facts)
	}
	branch = strings.TrimPrefix(branch, "v")
	wantBranch := strings.TrimPrefix(product.SupportedBranch, "v")
	if branch == "" {
		return ir.APKRepositorySpec{}, resourceAttributeError(declaration, "branch", "requires detected target facts or host platform.version")
	}
	if branch != wantBranch {
		return ir.APKRepositorySpec{}, resourceAttributeError(declaration, "branch", "must match supported target branch %q, got %q", wantBranch, branch)
	}
	component, err := resourceStringDefault(declaration, "component", declaration.Label, host, facts, ctx)
	if err != nil {
		return ir.APKRepositorySpec{}, err
	}
	if !apkComponentPattern.MatchString(component) {
		return ir.APKRepositorySpec{}, resourceAttributeError(declaration, "component", "must be a valid APK repository path component")
	}
	tag, err := resourceStringDefault(declaration, "tag", "", host, facts, ctx)
	if err != nil {
		return ir.APKRepositorySpec{}, err
	}
	if tag != "" && !apkTagPattern.MatchString(tag) {
		return ir.APKRepositorySpec{}, resourceAttributeError(declaration, "tag", "must contain only letters, digits, dot, underscore, and hyphen")
	}
	line := baseURL + "/v" + branch + "/" + component
	if tag != "" {
		line = "@" + tag + " " + line
	}
	return ir.APKRepositorySpec{
		Name: declaration.Label, URL: baseURL, Branch: branch, Component: component, Tag: tag,
		Line: line, Ensure: ensure,
		Lifecycle: ir.LifecycleSpec{PreventDestroy: declaration.Lifecycle.PreventDestroy, Source: declaration.Lifecycle.Source},
		Source:    declaration.Source,
	}, nil
}

func compileAPKKey(declaration parser.ResourceDeclaration, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (ir.APKKeySpec, error) {
	filename := declaration.Label
	if !apkKeyFilenamePattern.MatchString(filename) || filepath.Base(filename) != filename {
		return ir.APKKeySpec{}, resourceError(declaration.Source, "APK key filename %q must be a safe basename", filename)
	}
	ensure, err := resourceStringDefault(declaration, "ensure", "present", host, facts, ctx)
	if err != nil {
		return ir.APKKeySpec{}, err
	}
	if ensure != "present" && ensure != "absent" {
		return ir.APKKeySpec{}, resourceAttributeError(declaration, "ensure", "must be \"present\" or \"absent\"")
	}
	key := ir.APKKeySpec{
		Filename: filename, Ensure: ensure,
		Lifecycle: ir.LifecycleSpec{PreventDestroy: declaration.Lifecycle.PreventDestroy, Source: declaration.Lifecycle.Source},
		Source:    declaration.Source,
	}
	if ensure == "absent" {
		return key, nil
	}
	source, sourceSet, err := resourceString(declaration, "source", host, facts, ctx)
	if err != nil {
		return ir.APKKeySpec{}, err
	}
	if !sourceSet || source == "" {
		return ir.APKKeySpec{}, resourceAttributeError(declaration, "source", "is required for a present key")
	}
	if filepath.IsAbs(source) || filepath.Clean(source) != source || source == "." || source == ".." || strings.HasPrefix(source, ".."+string(filepath.Separator)) {
		return ir.APKKeySpec{}, resourceAttributeError(declaration, "source", "must be a clean relative path within the declaring module")
	}
	digest, digestSet, err := resourceString(declaration, "sha256", host, facts, ctx)
	if err != nil {
		return ir.APKKeySpec{}, err
	}
	if !digestSet || !apkSHA256Pattern.MatchString(digest) {
		return ir.APKKeySpec{}, resourceAttributeError(declaration, "sha256", "must be a required lowercase 64-character SHA-256 digest for a present key")
	}
	path := filepath.Join(filepath.Dir(declaration.Source.File), source)
	content, err := os.ReadFile(path)
	if err != nil {
		return ir.APKKeySpec{}, resourceAttributeError(declaration, "source", "read APK key: %v", err)
	}
	sum := sha256.Sum256(content)
	actual := hex.EncodeToString(sum[:])
	if actual != digest {
		return ir.APKKeySpec{}, resourceAttributeError(declaration, "sha256", "APK key digest mismatch: expected %s, got %s", digest, actual)
	}
	key.SourcePath = source
	key.SHA256 = digest
	key.Content = content
	return key, nil
}

func normalizeAPKRepositoryURL(value string) (string, error) {
	if value != strings.TrimSpace(value) || strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("must be a single HTTPS base URL")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("must be an HTTPS base URL without credentials, query, or fragment")
	}
	if parsed.RawPath != "" || strings.Contains(parsed.EscapedPath(), "%") {
		return "", fmt.Errorf("must not contain an encoded path")
	}
	cleanedPath := filepath.ToSlash(filepath.Clean(parsed.Path))
	if cleanedPath == "." {
		cleanedPath = ""
	}
	if cleanedPath != parsed.Path && cleanedPath+"/" != parsed.Path {
		return "", fmt.Errorf("must contain a clean path")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return parsed.String(), nil
}

func targetAPKBranch(host parser.Host, facts *ir.HostFacts) string {
	if facts != nil {
		return facts.Branch
	}
	if host.Platform != nil {
		return host.Platform.Branch
	}
	return ""
}

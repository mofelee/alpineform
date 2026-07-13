package merge

import (
	"regexp"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
)

var apkPackageNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+_.-]{0,127}$`)

func compilePackage(declaration parser.ResourceDeclaration, apk *ir.APKSpec, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (ir.PackageSpec, error) {
	name := declaration.Label
	if !apkPackageNamePattern.MatchString(name) {
		return ir.PackageSpec{}, resourceError(declaration.Source, "package name %q must be an unversioned APK package name", name)
	}
	ensure, err := resourceStringDefault(declaration, "ensure", "present", host, facts, ctx)
	if err != nil {
		return ir.PackageSpec{}, err
	}
	if ensure != "present" && ensure != "absent" {
		return ir.PackageSpec{}, resourceAttributeError(declaration, "ensure", "must be \"present\" or \"absent\"")
	}
	repositoryTag, err := resourceStringDefault(declaration, "repository", "", host, facts, ctx)
	if err != nil {
		return ir.PackageSpec{}, err
	}
	if repositoryTag != "" {
		if !apkTagPattern.MatchString(repositoryTag) {
			return ir.PackageSpec{}, resourceAttributeError(declaration, "repository", "must be a valid declared APK repository tag")
		}
		if apk == nil {
			return ir.PackageSpec{}, resourceAttributeError(declaration, "repository", "references tag %q, but the host has no apk block", repositoryTag)
		}
		found := false
		for _, repository := range apk.Repositories {
			if repository.Tag == repositoryTag && repository.Ensure == "present" {
				found = true
				break
			}
		}
		if !found {
			return ir.PackageSpec{}, resourceAttributeError(declaration, "repository", "references unknown or absent APK repository tag %q", repositoryTag)
		}
	}
	worldIntent := name
	if repositoryTag != "" {
		worldIntent += "@" + repositoryTag
	}
	return ir.PackageSpec{
		Name: name, RepositoryTag: repositoryTag, WorldIntent: worldIntent, Ensure: ensure,
		Lifecycle: ir.LifecycleSpec{PreventDestroy: declaration.Lifecycle.PreventDestroy, Source: declaration.Lifecycle.Source},
		Source:    declaration.Source,
	}, nil
}

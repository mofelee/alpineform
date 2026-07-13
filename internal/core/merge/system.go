package merge

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
)

var (
	hostnameLabelPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	timezoneTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_+.-]+(?:/[A-Za-z0-9_+.-]+)*$`)
)

func compileSystem(system parser.System, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (*ir.SystemSpec, error) {
	declaration := parser.ResourceDeclaration{Kind: "system", Attributes: system.Attributes, Source: system.Source}
	spec := &ir.SystemSpec{Source: system.Source}
	if hostname, exists, err := resourceString(declaration, "hostname", host, facts, ctx); err != nil {
		return nil, err
	} else if exists {
		if err := validateHostname(hostname); err != nil {
			return nil, resourceError(system.Attributes["hostname"].Source, "%s", err)
		}
		spec.Hostname = hostname
		spec.HostnameSource = system.Attributes["hostname"].Source
	}
	if timezone, exists, err := resourceString(declaration, "timezone", host, facts, ctx); err != nil {
		return nil, err
	} else if exists {
		if err := validateTimezone(timezone); err != nil {
			return nil, resourceError(system.Attributes["timezone"].Source, "%s", err)
		}
		spec.Timezone = timezone
		spec.TimezoneSource = system.Attributes["timezone"].Source
	}
	return spec, nil
}

func validateHostname(value string) error {
	if value == "" || len(value) > 253 || strings.HasSuffix(value, ".") {
		return fmt.Errorf("system.hostname must be a non-empty RFC 1123 hostname of at most 253 characters")
	}
	for _, label := range strings.Split(value, ".") {
		if !hostnameLabelPattern.MatchString(label) {
			return fmt.Errorf("system.hostname contains an invalid RFC 1123 label")
		}
	}
	return nil
}

func validateTimezone(value string) error {
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, "\\") || strings.ContainsAny(value, "\x00\r\n\t ") || !timezoneTokenPattern.MatchString(value) {
		return fmt.Errorf("system.timezone must be a relative zoneinfo name such as UTC or Asia/Shanghai")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("system.timezone must not contain empty, current, or parent path segments")
		}
	}
	return nil
}

func ensureTimezonePackage(packages []ir.PackageSpec, system *ir.SystemSpec) ([]ir.PackageSpec, error) {
	if system == nil || system.Timezone == "" {
		return packages, nil
	}
	for _, pkg := range packages {
		if pkg.Name != "tzdata" {
			continue
		}
		if pkg.Ensure != "present" {
			return nil, resourceError(system.TimezoneSource, "system.timezone requires package tzdata to be declared present")
		}
		return packages, nil
	}
	return append(packages, ir.PackageSpec{
		Name: "tzdata", WorldIntent: "tzdata", Ensure: "present", Lifecycle: ir.LifecycleSpec{}, Source: system.TimezoneSource,
	}), nil
}

package engine

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/product"
)

const (
	osReleaseCommand  = "cat /etc/os-release"
	apkArchCommand    = "apk --print-arch"
	kernelArchCommand = "uname -m"
	supportedBranch   = product.SupportedBranch
)

var (
	osReleaseKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	versionPattern      = regexp.MustCompile(`^([0-9]+)\.([0-9]+)(?:\.([0-9]+))?$`)
)

// FactsReader intentionally exposes only fixed-command reads. A state or lock
// backend is a separate capability so fact validation can precede all writes.
type FactsReader interface {
	Read(ctx context.Context, command string) (string, error)
}

type FactDiscoveryOptions struct {
	Now func() time.Time
}

func DiscoverHostFacts(ctx context.Context, reader FactsReader, options FactDiscoveryOptions) (ir.HostFacts, error) {
	if reader == nil {
		return ir.HostFacts{}, fmt.Errorf("Alpine fact discovery requires a reader")
	}
	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	osReleaseRaw, err := reader.Read(ctx, osReleaseCommand)
	if err != nil {
		return ir.HostFacts{}, fmt.Errorf("read /etc/os-release: %w", err)
	}
	osRelease, err := parseOSRelease(osReleaseRaw)
	if err != nil {
		return ir.HostFacts{}, err
	}
	if osRelease["ID"] != product.TargetOSID {
		return ir.HostFacts{}, fmt.Errorf("unsupported target OS %q; AlpineForm v0.1 requires Alpine Linux", osRelease["ID"])
	}
	version := osRelease["VERSION_ID"]
	branch, err := alpineReleaseBranch(version)
	if err != nil {
		return ir.HostFacts{}, err
	}
	if branch != supportedBranch {
		return ir.HostFacts{}, fmt.Errorf("unsupported Alpine branch %q from version %q; AlpineForm v0.1 supports %s", branch, version, supportedBranch)
	}

	nativeArchitectureRaw, err := reader.Read(ctx, apkArchCommand)
	if err != nil {
		return ir.HostFacts{}, fmt.Errorf("read native APK architecture: %w", err)
	}
	nativeArchitecture, err := singleValue("apk --print-arch", nativeArchitectureRaw)
	if err != nil {
		return ir.HostFacts{}, err
	}
	architecture, canonicalNative, err := normalizeArchitecture(nativeArchitecture)
	if err != nil {
		return ir.HostFacts{}, fmt.Errorf("native APK architecture: %w", err)
	}
	if nativeArchitecture != canonicalNative {
		return ir.HostFacts{}, fmt.Errorf("apk --print-arch returned non-native architecture %q; expected %q", nativeArchitecture, canonicalNative)
	}

	kernelArchitectureRaw, err := reader.Read(ctx, kernelArchCommand)
	if err != nil {
		return ir.HostFacts{}, fmt.Errorf("read kernel architecture: %w", err)
	}
	kernelArchitecture, err := singleValue("uname -m", kernelArchitectureRaw)
	if err != nil {
		return ir.HostFacts{}, err
	}
	kernelNormalized, _, err := normalizeArchitecture(kernelArchitecture)
	if err != nil {
		return ir.HostFacts{}, fmt.Errorf("kernel architecture: %w", err)
	}
	if kernelNormalized != architecture {
		return ir.HostFacts{}, fmt.Errorf("architecture mismatch: apk reports %q (%s), kernel reports %q (%s)", nativeArchitecture, architecture, kernelArchitecture, kernelNormalized)
	}

	return ir.HostFacts{
		OSID:               product.TargetOSID,
		Version:            version,
		Branch:             branch,
		Architecture:       architecture,
		NativeArchitecture: canonicalNative,
		KernelArchitecture: kernelArchitecture,
		Libc:               product.TargetLibc,
		DetectedAt:         now().UTC().Format(time.RFC3339),
	}, nil
}

func parseOSRelease(input string) (map[string]string, error) {
	values := map[string]string{}
	for lineNumber, rawLine := range strings.Split(input, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rawValue, found := strings.Cut(line, "=")
		if !found || !osReleaseKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("/etc/os-release:%d: invalid assignment", lineNumber+1)
		}
		value, err := parseOSReleaseValue(rawValue)
		if err != nil {
			return nil, fmt.Errorf("/etc/os-release:%d:%s: %w", lineNumber+1, key, err)
		}
		values[key] = value
	}
	for _, required := range []string{"ID", "VERSION_ID"} {
		if values[required] == "" {
			return nil, fmt.Errorf("/etc/os-release is missing required %s", required)
		}
	}
	return values, nil
}

func parseOSReleaseValue(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if raw[0] == '\'' {
		if len(raw) < 2 || raw[len(raw)-1] != '\'' || strings.Contains(raw[1:len(raw)-1], "'") {
			return "", fmt.Errorf("invalid quoted value")
		}
		return raw[1 : len(raw)-1], nil
	}
	if raw[0] == '"' {
		value, err := strconv.Unquote(raw)
		if err != nil {
			return "", fmt.Errorf("invalid quoted value")
		}
		return value, nil
	}
	if strings.ContainsAny(raw, " \t\"'") {
		return "", fmt.Errorf("invalid unquoted value")
	}
	return raw, nil
}

func alpineReleaseBranch(version string) (string, error) {
	match := versionPattern.FindStringSubmatch(version)
	if match == nil {
		return "", fmt.Errorf("invalid Alpine VERSION_ID %q; expected a numeric release such as 3.24.1", version)
	}
	return "v" + match[1] + "." + match[2], nil
}

func normalizeArchitecture(value string) (normalized, native string, err error) {
	switch value {
	case "amd64", "x86_64":
		return "amd64", "x86_64", nil
	case "arm64", "aarch64":
		return "arm64", "aarch64", nil
	default:
		return "", "", fmt.Errorf("unsupported architecture %q; v0.1 supports x86_64 and aarch64", value)
	}
}

func singleValue(command, output string) (string, error) {
	value := strings.TrimSpace(output)
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return "", fmt.Errorf("%s returned invalid value %q", command, value)
	}
	return value, nil
}

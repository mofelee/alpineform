package merge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
)

const (
	defaultNftablesRollbackTimeout = 30 * time.Second
	minNftablesRollbackTimeout     = 10 * time.Second
	maxNftablesRollbackTimeout     = 5 * time.Minute
	maxNftablesTableContentBytes   = 1 << 20
)

var nftablesTableNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)

var nftablesFamilies = map[string]bool{
	"arp": true, "bridge": true, "inet": true, "ip": true, "ip6": true, "netdev": true,
}

func compileNftables(nftables parser.Nftables, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (*ir.NftablesSpec, error) {
	spec := &ir.NftablesSpec{Source: nftables.Source}
	seen := map[string]ir.SourceRef{}
	for _, declaration := range nftables.Tables {
		table, err := compileNftablesTable(declaration, host, facts, ctx)
		if err != nil {
			return nil, err
		}
		identity := table.Family + "\x00" + table.Name
		if previous, exists := seen[identity]; exists {
			return nil, resourceError(table.Source, "duplicate nftables table %s %q; first defined at %s:%d", table.Family, table.Name, previous.File, previous.Line)
		}
		seen[identity] = table.Source
		spec.Tables = append(spec.Tables, table)
	}
	sort.Slice(spec.Tables, func(i, j int) bool {
		if spec.Tables[i].Family != spec.Tables[j].Family {
			return spec.Tables[i].Family < spec.Tables[j].Family
		}
		return spec.Tables[i].Name < spec.Tables[j].Name
	})
	return spec, nil
}

func compileNftablesTable(declaration parser.ResourceDeclaration, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (ir.NftablesTableSpec, error) {
	if !nftablesTableNamePattern.MatchString(declaration.Label) {
		return ir.NftablesTableSpec{}, resourceError(declaration.Source, "nftables table name %q must start with a letter or underscore and contain at most 64 letters, digits, underscores, or hyphens", declaration.Label)
	}
	family, err := resourceStringDefault(declaration, "family", "inet", host, facts, ctx)
	if err != nil {
		return ir.NftablesTableSpec{}, err
	}
	if !nftablesFamilies[family] {
		return ir.NftablesTableSpec{}, resourceAttributeError(declaration, "family", "must be one of arp, bridge, inet, ip, ip6, or netdev")
	}
	ensure, err := resourceStringDefault(declaration, "ensure", "present", host, facts, ctx)
	if err != nil {
		return ir.NftablesTableSpec{}, err
	}
	if ensure != "present" && ensure != "absent" {
		return ir.NftablesTableSpec{}, resourceAttributeError(declaration, "ensure", "must be \"present\" or \"absent\"")
	}
	adoptExisting, err := resourceBoolDefault(declaration, "adopt_existing", false, host, facts, ctx)
	if err != nil {
		return ir.NftablesTableSpec{}, err
	}
	if ensure == "absent" && adoptExisting {
		return ir.NftablesTableSpec{}, resourceAttributeError(declaration, "adopt_existing", "cannot be true when ensure is \"absent\"")
	}
	onRemove, err := resourceStringDefault(declaration, "on_remove", "forget", host, facts, ctx)
	if err != nil {
		return ir.NftablesTableSpec{}, err
	}
	if onRemove != "forget" && onRemove != "delete" {
		return ir.NftablesTableSpec{}, resourceAttributeError(declaration, "on_remove", "must be \"forget\" or \"delete\"")
	}
	rollbackTimeout, err := compileNftablesRollbackTimeout(declaration, host, facts, ctx)
	if err != nil {
		return ir.NftablesTableSpec{}, err
	}

	table := ir.NftablesTableSpec{
		Name: declaration.Label, Family: family, Ensure: ensure, AdoptExisting: adoptExisting, OnRemove: onRemove,
		RollbackTimeoutSeconds: int64(rollbackTimeout / time.Second), Sensitive: true,
		Lifecycle: ir.LifecycleSpec{PreventDestroy: declaration.Lifecycle.PreventDestroy, Source: declaration.Lifecycle.Source},
		Source:    declaration.Source,
	}
	content, hasContent, err := resourceValue(declaration, "content", host, facts, ctx)
	if err != nil {
		return ir.NftablesTableSpec{}, err
	}
	version, versionSet, err := resourceString(declaration, "content_version", host, facts, ctx)
	if err != nil {
		return ir.NftablesTableSpec{}, err
	}
	if versionSet && version == "" {
		return ir.NftablesTableSpec{}, resourceAttributeError(declaration, "content_version", "must not be empty")
	}
	if ensure == "absent" {
		if hasContent || versionSet {
			return ir.NftablesTableSpec{}, resourceAttributeError(declaration, "content", "content and content_version must not be set when ensure is \"absent\"")
		}
		return table, nil
	}
	if !hasContent || content.Kind != parser.KindString || strings.TrimSpace(content.String) == "" {
		return ir.NftablesTableSpec{}, resourceAttributeError(declaration, "content", "must be a required non-empty string containing only the owned table body")
	}
	if len(content.String) > maxNftablesTableContentBytes {
		return ir.NftablesTableSpec{}, resourceAttributeError(declaration, "content", "must not exceed %d bytes", maxNftablesTableContentBytes)
	}
	if err := validateNftablesTableBody(content.String); err != nil {
		return ir.NftablesTableSpec{}, resourceAttributeError(declaration, "content", "%v", err)
	}
	if content.ContainsEphemeral() && !versionSet {
		return ir.NftablesTableSpec{}, resourceAttributeError(declaration, "content_version", "is required when content is ephemeral")
	}
	table.Content = content.String
	table.ContentBytes = int64(len(content.String))
	table.ContentVersion = version
	table.ContentWriteOnly = content.ContainsEphemeral()
	table.Ephemeral = content.ContainsEphemeral()
	if !table.ContentWriteOnly {
		sum := sha256.Sum256([]byte(content.String))
		table.ContentSHA256 = hex.EncodeToString(sum[:])
	}
	return table, nil
}

func compileNftablesRollbackTimeout(declaration parser.ResourceDeclaration, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (time.Duration, error) {
	raw, exists, err := resourceString(declaration, "rollback_timeout", host, facts, ctx)
	if err != nil {
		return 0, err
	}
	if !exists {
		return defaultNftablesRollbackTimeout, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration%time.Second != 0 || duration < minNftablesRollbackTimeout || duration > maxNftablesRollbackTimeout {
		return 0, resourceAttributeError(declaration, "rollback_timeout", "must be a whole-second duration from 10s through 5m")
	}
	return duration, nil
}

func validateNftablesTableBody(content string) error {
	depth := 0
	state := byte(0)
	escaped := false
	for index := 0; index < len(content); {
		ch := content[index]
		switch state {
		case '#':
			if ch == '\n' {
				state = 0
			}
			index++
			continue
		case '*':
			if ch == '*' && index+1 < len(content) && content[index+1] == '/' {
				state = 0
				index += 2
				continue
			}
			index++
			continue
		case '\'', '"':
			if escaped {
				escaped = false
				index++
				continue
			}
			if ch == '\\' {
				escaped = true
				index++
				continue
			}
			if ch == state {
				state = 0
			}
			index++
			continue
		}
		if ch == 0 {
			return fmt.Errorf("must not contain NUL bytes")
		}
		if ch == '#' {
			state = '#'
			index++
			continue
		}
		if ch == '/' && index+1 < len(content) && content[index+1] == '*' {
			state = '*'
			index += 2
			continue
		}
		if ch == '\'' || ch == '"' {
			state = ch
			index++
			continue
		}
		if ch == '{' {
			depth++
			index++
			continue
		}
		if ch == '}' {
			if depth == 0 {
				return fmt.Errorf("must not close the AlpineForm-owned table wrapper")
			}
			depth--
			index++
			continue
		}
		if isNftablesIdentifierStart(ch) {
			start := index
			for index < len(content) && isNftablesIdentifierPart(content[index]) {
				index++
			}
			word := strings.ToLower(content[start:index])
			if word == "include" || word == "table" {
				return fmt.Errorf("unsupported %q directive; content must contain only one table body", word)
			}
			if depth == 0 {
				switch word {
				case "add", "delete", "destroy", "export", "flush", "import", "insert", "list", "monitor", "rename", "replace", "reset":
					return fmt.Errorf("unsupported top-level %q command; global or external ruleset mutation is forbidden", word)
				}
			}
			continue
		}
		index++
	}
	if state == '*' {
		return fmt.Errorf("contains an unterminated block comment")
	}
	if state == '\'' || state == '"' {
		return fmt.Errorf("contains an unterminated quoted string")
	}
	if depth != 0 {
		return fmt.Errorf("contains unbalanced braces")
	}
	return nil
}

func isNftablesIdentifierStart(ch byte) bool {
	return ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z'
}

func isNftablesIdentifierPart(ch byte) bool {
	return isNftablesIdentifierStart(ch) || ch >= '0' && ch <= '9' || ch == '-'
}

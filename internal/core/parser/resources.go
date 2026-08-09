package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/zclconf/go-cty/cty"
)

const (
	ResourceFile      = "file"
	ResourceDirectory = "directory"
	ResourceGroup     = "group"
	ResourceUser      = "user"
	ResourcePackage   = "package"
	ResourceService   = "service"
)

type ResourceAttribute struct {
	Expression hcl.Expression
	Source     ir.SourceRef
}

type ResourceReference struct {
	Kind   string
	Label  string
	Source ir.SourceRef
}

type ResourceDeclaration struct {
	Kind       string
	Label      string
	Attributes map[string]ResourceAttribute
	DependsOn  []ResourceReference
	OnChange   *ScriptReference
	Lifecycle  Lifecycle
	Source     ir.SourceRef
}

type resourceCollectionSchema struct {
	block      string
	resource   string
	attributes map[string]struct{}
}

var hostResourceCollections = map[string]resourceCollectionSchema{
	"files": {
		block:    "files",
		resource: ResourceFile,
		attributes: attributeSet(
			"content", "source", "content_version", "owner", "group", "mode",
			"sensitive", "ensure", "on_remove", "on_change", "depends_on",
		),
	},
	"directories": {
		block:    "directories",
		resource: ResourceDirectory,
		attributes: attributeSet(
			"owner", "group", "mode", "ensure", "recursive_delete", "on_remove",
		),
	},
	"groups": {
		block:    "groups",
		resource: ResourceGroup,
		attributes: attributeSet(
			"gid", "system", "ensure", "on_remove",
		),
	},
	"users": {
		block:    "users",
		resource: ResourceUser,
		attributes: attributeSet(
			"uid", "group", "groups", "home", "shell", "system", "ssh_authorized_keys", "ensure", "on_remove",
		),
	},
	"packages": {
		block:      "packages",
		resource:   ResourcePackage,
		attributes: attributeSet("ensure", "repository", "depends_on"),
	},
	"services": {
		block:      "services",
		resource:   ResourceService,
		attributes: attributeSet("enabled", "runlevel", "state", "operation", "package", "user", "group", "depends_on"),
	},
}

func parseHostResourceCollection(file, parentPath string, block *hclsyntax.Block, ctx EvalContext) ([]ResourceDeclaration, error) {
	schema, exists := hostResourceCollections[block.Type]
	if !exists {
		return nil, fmt.Errorf("%s:%d:%s: unsupported resource collection %q", file, block.TypeRange.Start.Line, parentPath, block.Type)
	}
	collectionPath := parentPath + "." + schema.block
	if len(block.Labels) != 0 || len(block.Body.Attributes) != 0 {
		return nil, fmt.Errorf("%s:%d:%s must be an unlabeled block containing only %s blocks", file, block.TypeRange.Start.Line, collectionPath, schema.resource)
	}
	declarations := make([]ResourceDeclaration, 0, len(block.Body.Blocks))
	seen := map[string]ir.SourceRef{}
	for _, child := range block.Body.Blocks {
		if child.Type != schema.resource {
			return nil, fmt.Errorf("%s:%d:%s supports only %s blocks, got %s", file, child.TypeRange.Start.Line, collectionPath, schema.resource, child.Type)
		}
		declaration, err := parseResourceDeclaration(file, collectionPath, child, schema, ctx)
		if err != nil {
			return nil, err
		}
		if previous, exists := seen[declaration.Label]; exists {
			return nil, fmt.Errorf("%s:%d:%s: duplicate %s label %q; first defined at %s:%d", file, declaration.Source.Line, declaration.Source.Path, schema.resource, declaration.Label, previous.File, previous.Line)
		}
		seen[declaration.Label] = declaration.Source
		declarations = append(declarations, declaration)
	}
	return declarations, nil
}

func parseResourceDeclaration(file, collectionPath string, block *hclsyntax.Block, schema resourceCollectionSchema, ctx EvalContext) (ResourceDeclaration, error) {
	if len(block.Labels) != 1 {
		return ResourceDeclaration{}, fmt.Errorf("%s:%d:%s.%s block requires exactly one label", file, block.TypeRange.Start.Line, collectionPath, schema.resource)
	}
	label := block.Labels[0]
	if label == "" || strings.ContainsAny(label, "\x00\r\n") {
		return ResourceDeclaration{}, fmt.Errorf("%s:%d:%s.%s label must be non-empty and contain no control characters", file, block.TypeRange.Start.Line, collectionPath, schema.resource)
	}
	path := collectionPath + "." + schema.resource + "[" + strconv.Quote(label) + "]"
	declaration := ResourceDeclaration{
		Kind:       schema.resource,
		Label:      label,
		Attributes: make(map[string]ResourceAttribute, len(block.Body.Attributes)),
		Source:     ir.SourceRef{File: file, Line: block.TypeRange.Start.Line, Path: path},
	}
	for name, attribute := range block.Body.Attributes {
		if _, allowed := schema.attributes[name]; !allowed {
			return ResourceDeclaration{}, fmt.Errorf("%s:%d: unsupported attribute %s.%s", file, attribute.NameRange.Start.Line, path, name)
		}
		if name == "on_change" {
			reference, err := parseScriptReference(file, path+".on_change", attribute.Expr)
			if err != nil {
				return ResourceDeclaration{}, err
			}
			declaration.OnChange = &reference
			continue
		}
		if name == "depends_on" {
			references, err := parseResourceReferences(file, path+".depends_on", attribute.Expr)
			if err != nil {
				return ResourceDeclaration{}, err
			}
			declaration.DependsOn = references
			continue
		}
		declaration.Attributes[name] = ResourceAttribute{
			Expression: attribute.Expr,
			Source:     ir.SourceRef{File: file, Line: attribute.NameRange.Start.Line, Path: path + "." + name},
		}
	}
	for _, child := range block.Body.Blocks {
		if child.Type != "lifecycle" {
			return ResourceDeclaration{}, fmt.Errorf("%s:%d: unsupported block %s.%s", file, child.TypeRange.Start.Line, path, child.Type)
		}
		if declaration.Lifecycle.Source.Path != "" {
			return ResourceDeclaration{}, fmt.Errorf("%s:%d: duplicate %s.lifecycle block", file, child.TypeRange.Start.Line, path)
		}
		lifecycle, err := parseLifecycle(file, path+".lifecycle", child, ctx)
		if err != nil {
			return ResourceDeclaration{}, err
		}
		declaration.Lifecycle = lifecycle
	}
	return declaration, nil
}

func parseResourceReferences(file, path string, expression hcl.Expression) ([]ResourceReference, error) {
	items, diagnostics := hcl.ExprList(expression)
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("%s:%d:%s: depends_on must be a list of typed resource references", file, expression.Range().Start.Line, path)
	}
	references := make([]ResourceReference, 0, len(items))
	seen := map[string]ir.SourceRef{}
	for index, item := range items {
		source := ir.SourceRef{File: file, Line: item.Range().Start.Line, Path: fmt.Sprintf("%s[%d]", path, index)}
		reference, err := parseResourceReference(item, source)
		if err != nil {
			return nil, err
		}
		key := reference.Kind + "\x00" + reference.Label
		if previous, exists := seen[key]; exists {
			return nil, fmt.Errorf("%s:%d:%s: duplicate depends_on reference %s; first declared at %s:%d:%s", source.File, source.Line, source.Path, formatResourceReference(reference.Kind, reference.Label), previous.File, previous.Line, previous.Path)
		}
		seen[key] = source
		references = append(references, reference)
	}
	return references, nil
}

func parseResourceReference(expression hcl.Expression, source ir.SourceRef) (ResourceReference, error) {
	traversal, diagnostics := hcl.AbsTraversalForExpr(expression)
	if diagnostics.HasErrors() || len(traversal) != 2 {
		return ResourceReference{}, fmt.Errorf("%s:%d:%s: depends_on entry must be a typed package, file, or service reference with a static label", source.File, source.Line, source.Path)
	}
	root, ok := traversal[0].(hcl.TraverseRoot)
	if !ok || (root.Name != ResourcePackage && root.Name != ResourceFile && root.Name != ResourceService) {
		return ResourceReference{}, fmt.Errorf("%s:%d:%s: depends_on reference type is out of scope; supported types are package, file, and service", source.File, source.Line, source.Path)
	}
	label := ""
	switch step := traversal[1].(type) {
	case hcl.TraverseAttr:
		label = step.Name
	case hcl.TraverseIndex:
		if step.Key.IsKnown() && !step.Key.IsNull() && step.Key.Type().Equals(cty.String) {
			label = step.Key.AsString()
		}
	}
	if label == "" || strings.ContainsAny(label, "\x00\r\n") {
		return ResourceReference{}, fmt.Errorf("%s:%d:%s: depends_on resource label must be a non-empty static string", source.File, source.Line, source.Path)
	}
	return ResourceReference{Kind: root.Name, Label: label, Source: source}, nil
}

func formatResourceReference(kind, label string) string {
	if declarationLabelPattern.MatchString(label) {
		return kind + "." + label
	}
	return kind + "[" + strconv.Quote(label) + "]"
}

func appendUniqueResources(existing []ResourceDeclaration, additions []ResourceDeclaration) ([]ResourceDeclaration, error) {
	seen := make(map[string]ir.SourceRef, len(existing)+len(additions))
	for _, resource := range existing {
		seen[resource.Kind+"\x00"+resource.Label] = resource.Source
	}
	for _, resource := range additions {
		key := resource.Kind + "\x00" + resource.Label
		if previous, exists := seen[key]; exists {
			return nil, fmt.Errorf("%s:%d:%s: duplicate %s label %q; first defined at %s:%d", resource.Source.File, resource.Source.Line, resource.Source.Path, resource.Kind, resource.Label, previous.File, previous.Line)
		}
		seen[key] = resource.Source
		existing = append(existing, resource)
	}
	return existing, nil
}

func attributeSet(names ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}

// Package plan renders deterministic, side-effect-free AlpineForm plans.
package plan

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"sort"
	"strings"

	"github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
)

const FormatVersion = "alpineform.plan.alpha1"

type Document struct {
	FormatVersion string      `json:"format_version"`
	Mode          string      `json:"mode"`
	Command       Command     `json:"command"`
	Hosts         []string    `json:"hosts"`
	Summary       Summary     `json:"summary"`
	Graph         []GraphNode `json:"graph"`
	Changes       []Change    `json:"changes"`
}

type Command struct {
	Files []string `json:"files"`
}

type Summary struct {
	Create           int `json:"create"`
	Update           int `json:"update"`
	Delete           int `json:"delete"`
	NoOp             int `json:"no_op"`
	ManagedResources int `json:"managed_resources"`
	GraphNodes       int `json:"graph_nodes"`
}

type GraphNode struct {
	Address   string       `json:"address"`
	Kind      string       `json:"kind"`
	Managed   bool         `json:"managed"`
	DependsOn []string     `json:"depends_on,omitempty"`
	Source    ir.SourceRef `json:"source"`
}

type Change struct {
	Address string         `json:"address"`
	Action  string         `json:"action"`
	Summary string         `json:"summary"`
	Source  ir.SourceRef   `json:"source"`
	Desired map[string]any `json:"desired,omitempty"`
}

type Options struct {
	Files []string
	Hosts []string
}

func New(resourceGraph *graph.ResourceGraph, options Options) Document {
	nodes := append([]graph.Node(nil), resourceGraph.Nodes...)
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].Address < nodes[j].Address })
	document := Document{
		FormatVersion: FormatVersion,
		Mode:          "offline",
		Command:       Command{Files: append([]string(nil), options.Files...)},
		Hosts:         append([]string(nil), options.Hosts...),
		Graph:         make([]GraphNode, 0, len(nodes)),
		Changes:       []Change{},
	}
	sort.Strings(document.Hosts)
	for _, node := range nodes {
		document.Graph = append(document.Graph, GraphNode{
			Address:   node.Address,
			Kind:      node.Kind,
			Managed:   node.Managed,
			DependsOn: append([]string(nil), node.DependsOn...),
			Source:    node.Source,
		})
		if !node.Managed {
			continue
		}
		desired := cloneMap(node.Desired)
		if node.Sensitive || node.Ephemeral {
			desired = map[string]any{"protected": true}
		}
		document.Changes = append(document.Changes, Change{Address: node.Address, Action: "create", Summary: node.Summary, Source: node.Source, Desired: desired})
	}
	document.Summary = Summary{
		Create:           len(document.Changes),
		ManagedResources: resourceGraph.ManagedCount(),
		GraphNodes:       len(nodes),
	}
	return document
}

type TextOptions struct {
	Color bool
}

func PrintText(w io.Writer, document Document, options TextOptions) {
	heading := "Offline plan:"
	if options.Color {
		heading = "\x1b[1;36m" + heading + "\x1b[0m"
	}
	fmt.Fprintln(w, heading)
	fmt.Fprintf(w, "  Configuration graph: %d node(s), %d managed resource(s).\n", document.Summary.GraphNodes, document.Summary.ManagedResources)
	if len(document.Changes) == 0 {
		fmt.Fprintln(w, "  No managed resource changes.")
	} else {
		for _, change := range document.Changes {
			symbol := "+"
			if options.Color {
				symbol = "\x1b[32m+\x1b[0m"
			}
			fmt.Fprintf(w, "  %s %s\n", symbol, change.Address)
			if change.Summary != "" {
				fmt.Fprintf(w, "    %s\n", change.Summary)
			}
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Summary: %d to create, %d to update, %d to delete.\n", document.Summary.Create, document.Summary.Update, document.Summary.Delete)
}

func PrintJSON(w io.Writer, document Document) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(document)
}

func PrintHTML(w io.Writer, document Document) error {
	tmpl, err := template.New("plan").Parse(planHTML)
	if err != nil {
		return err
	}
	return tmpl.Execute(w, document)
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = cloneValue(value)
	}
	return out
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneValue(item)
		}
		return out
	default:
		return typed
	}
}

func SourceText(source ir.SourceRef) string {
	parts := []string{}
	if source.File != "" {
		parts = append(parts, source.File)
	}
	if source.Line > 0 {
		parts = append(parts, fmt.Sprintf("line %d", source.Line))
	}
	if source.Path != "" {
		parts = append(parts, source.Path)
	}
	return strings.Join(parts, ":")
}

const planHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>AlpineForm offline plan</title>
  <style>
    body { margin: 0; color: #17201b; background: #f5f7f5; font: 14px/1.5 ui-sans-serif, system-ui, sans-serif; }
    main { max-width: 960px; margin: 0 auto; padding: 32px 20px; }
    h1 { font-size: 24px; margin: 0 0 8px; }
    .summary { padding: 12px 0 20px; border-bottom: 1px solid #cdd5cf; }
    table { width: 100%; border-collapse: collapse; margin-top: 20px; background: #fff; }
    th, td { padding: 9px 10px; border: 1px solid #d8ded9; text-align: left; vertical-align: top; }
    th { background: #edf2ee; }
    code { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; overflow-wrap: anywhere; }
  </style>
</head>
<body>
<main>
  <h1>AlpineForm offline plan</h1>
  <div class="summary">{{.Summary.GraphNodes}} graph nodes; {{.Summary.ManagedResources}} managed resources; {{.Summary.Create}} to create.</div>
  <table>
    <thead><tr><th>Address</th><th>Kind</th><th>Managed</th><th>Source</th></tr></thead>
    <tbody>{{range .Graph}}<tr><td><code>{{.Address}}</code></td><td>{{.Kind}}</td><td>{{.Managed}}</td><td><code>{{.Source.File}}:{{.Source.Line}}</code></td></tr>{{end}}</tbody>
  </table>
</main>
</body>
</html>
`

// Package plan renders deterministic, side-effect-free AlpineForm plans.
package plan

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"sort"
	"strings"

	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/state"
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
	Create            int `json:"create"`
	Update            int `json:"update"`
	Adopt             int `json:"adopt,omitempty"`
	Delete            int `json:"delete"`
	Destroy           int `json:"destroy,omitempty"`
	Forget            int `json:"forget,omitempty"`
	NoOp              int `json:"no_op"`
	ManagedResources  int `json:"managed_resources"`
	GraphNodes        int `json:"graph_nodes"`
	NetworkDisruption int `json:"network_disruption,omitempty"`
}

type GraphNode struct {
	Address     string       `json:"address"`
	Kind        string       `json:"kind"`
	Managed     bool         `json:"managed"`
	DependsOn   []string     `json:"depends_on,omitempty"`
	TriggeredBy []string     `json:"triggered_by,omitempty"`
	Source      ir.SourceRef `json:"source"`
}

type Change struct {
	Address string         `json:"address"`
	Action  string         `json:"action"`
	Summary string         `json:"summary"`
	Source  ir.SourceRef   `json:"source"`
	Desired map[string]any `json:"desired,omitempty"`
	Risks   []string       `json:"risks,omitempty"`
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
			Address:     node.Address,
			Kind:        node.Kind,
			Managed:     node.Managed,
			DependsOn:   append([]string(nil), node.DependsOn...),
			TriggeredBy: append([]string(nil), node.TriggeredBy...),
			Source:      node.Source,
		})
		if !node.Managed {
			continue
		}
		action := engine.ActionCreate
		if ensure, _ := node.Desired["ensure"].(string); ensure == "absent" {
			action = engine.ActionDelete
		}
		desired := cloneMap(node.Desired)
		if node.Sensitive || node.Ephemeral {
			desired = map[string]any{"protected": true}
		}
		change := Change{Address: node.Address, Action: action, Summary: node.Summary, Source: node.Source, Desired: desired}
		if engine.IsNetworkDisrupting(node.Kind, action) {
			change.Risks = []string{engine.RiskNetworkDisruption}
			document.Summary.NetworkDisruption++
		}
		document.Changes = append(document.Changes, change)
		document.addAction(action)
	}
	document.Summary.ManagedResources = resourceGraph.ManagedCount()
	document.Summary.GraphNodes = len(nodes)
	return document
}

func NewOnline(actionPlan engine.Plan, options Options) Document {
	document := Document{
		FormatVersion: FormatVersion,
		Mode:          "online",
		Command:       Command{Files: append([]string(nil), options.Files...)},
		Graph:         []GraphNode{},
		Changes:       []Change{},
	}
	hosts := append([]engine.HostPlan(nil), actionPlan.Hosts...)
	sort.SliceStable(hosts, func(i, j int) bool { return hosts[i].Host.Name < hosts[j].Host.Name })
	for _, host := range hosts {
		document.Hosts = append(document.Hosts, host.Host.Name)
		for _, step := range host.Steps {
			desired := cloneMap(step.Node.Desired)
			if step.Node.Sensitive || step.Node.Ephemeral || resourceProtected(step.Prior) {
				desired = map[string]any{"protected": true}
			}
			summary := step.Summary
			if step.Node.Kind == "apk_repositories" && stringMapValue(step.Node.Desired, "ownership") == "authoritative" && step.Action != engine.ActionNoOp {
				summary = authoritativeRepositoriesDiff(summary, stringSliceMapValue(step.Observed.Values, "lines"), stringSliceMapValue(step.Node.Desired, "lines"))
			}
			change := Change{
				Address: step.Address,
				Action:  step.Action,
				Summary: summary,
				Source:  step.Node.Source,
				Desired: desired,
			}
			if step.IsNetworkDisrupting() {
				change.Risks = []string{engine.RiskNetworkDisruption}
				document.Summary.NetworkDisruption++
			}
			document.Changes = append(document.Changes, change)
			kind := step.Node.Kind
			if kind == "" && step.Prior != nil {
				kind = step.Prior.Kind
			}
			document.Graph = append(document.Graph, GraphNode{
				Address:     step.Address,
				Kind:        kind,
				Managed:     true,
				DependsOn:   append([]string(nil), step.Node.DependsOn...),
				TriggeredBy: append([]string(nil), step.Node.TriggeredBy...),
				Source:      step.Node.Source,
			})
			document.addAction(step.Action)
		}
	}
	document.Summary.ManagedResources = len(document.Changes)
	document.Summary.GraphNodes = len(document.Graph)
	return document
}

func (document *Document) addAction(action string) {
	switch action {
	case engine.ActionCreate:
		document.Summary.Create++
	case engine.ActionUpdate:
		document.Summary.Update++
	case engine.ActionAdopt:
		document.Summary.Adopt++
	case engine.ActionDelete:
		document.Summary.Delete++
	case engine.ActionDestroy:
		document.Summary.Destroy++
	case engine.ActionForget:
		document.Summary.Forget++
	case engine.ActionNoOp:
		document.Summary.NoOp++
	}
}

func resourceProtected(resource *state.Resource) bool {
	return resource != nil && (resource.Protected || resource.Sensitive || resource.Ephemeral)
}

type TextOptions struct {
	Color bool
}

func PrintText(w io.Writer, document Document, options TextOptions) {
	if document.Mode == "online" {
		printOnlineText(w, document, options)
		return
	}
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
			color := "\x1b[32m"
			if change.Action == engine.ActionDelete {
				symbol = "-"
				color = "\x1b[31m"
			}
			if options.Color {
				symbol = color + symbol + "\x1b[0m"
			}
			fmt.Fprintf(w, "  %s %s\n", symbol, change.Address)
			if change.Summary != "" {
				fmt.Fprintf(w, "    %s\n", strings.ReplaceAll(change.Summary, "\n", "\n    "))
			}
			printRisks(w, change)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Summary: %d to create, %d to update, %d to delete.\n", document.Summary.Create, document.Summary.Update, document.Summary.Delete)
}

func printOnlineText(w io.Writer, document Document, options TextOptions) {
	heading := "Online plan:"
	if options.Color {
		heading = "\x1b[1;36m" + heading + "\x1b[0m"
	}
	fmt.Fprintln(w, heading)
	if document.Summary.Create+document.Summary.Update+document.Summary.Adopt+document.Summary.Delete+document.Summary.Destroy+document.Summary.Forget == 0 {
		fmt.Fprintln(w, "  No remote resource changes.")
	} else {
		for _, change := range document.Changes {
			if change.Action == engine.ActionNoOp {
				continue
			}
			action := change.Action
			if options.Color {
				action = onlineActionColor(change.Action) + change.Action + "\x1b[0m"
			}
			fmt.Fprintf(w, "  %s %s\n", action, change.Address)
			if change.Summary != "" {
				fmt.Fprintf(w, "    %s\n", strings.ReplaceAll(change.Summary, "\n", "\n    "))
			}
			printRisks(w, change)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Summary: %d create, %d update, %d adopt, %d delete, %d destroy, %d forget, %d no-op.\n",
		document.Summary.Create,
		document.Summary.Update,
		document.Summary.Adopt,
		document.Summary.Delete,
		document.Summary.Destroy,
		document.Summary.Forget,
		document.Summary.NoOp,
	)
}

func printRisks(w io.Writer, change Change) {
	for _, risk := range change.Risks {
		if risk == engine.RiskNetworkDisruption {
			fmt.Fprintln(w, "    risk: network disruption")
		}
	}
}

func onlineActionColor(action string) string {
	switch action {
	case engine.ActionCreate, engine.ActionAdopt:
		return "\x1b[32m"
	case engine.ActionUpdate:
		return "\x1b[33m"
	case engine.ActionDelete, engine.ActionDestroy:
		return "\x1b[31m"
	default:
		return "\x1b[2m"
	}
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

func authoritativeRepositoriesDiff(summary string, before, after []string) string {
	var builder strings.Builder
	builder.WriteString(summary)
	builder.WriteString("\n--- observed /etc/apk/repositories")
	builder.WriteString("\n+++ desired /etc/apk/repositories")
	for _, line := range before {
		builder.WriteString("\n- ")
		builder.WriteString(line)
	}
	for _, line := range after {
		builder.WriteString("\n+ ")
		builder.WriteString(line)
	}
	return builder.String()
}

func stringMapValue(input map[string]any, name string) string {
	value, _ := input[name].(string)
	return value
}

func stringSliceMapValue(input map[string]any, name string) []string {
	value, ok := input[name].([]string)
	if ok {
		return append([]string(nil), value...)
	}
	generic, ok := input[name].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(generic))
	for _, item := range generic {
		text, ok := item.(string)
		if ok {
			out = append(out, text)
		}
	}
	return out
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
  <title>AlpineForm {{if eq .Mode "online"}}online{{else}}offline{{end}} plan</title>
  <style>
    body { margin: 0; color: #17201b; background: #f5f7f5; font: 14px/1.5 ui-sans-serif, system-ui, sans-serif; }
    main { max-width: 960px; margin: 0 auto; padding: 32px 20px; }
    h1 { font-size: 24px; margin: 0 0 8px; }
    .summary { padding: 12px 0 20px; border-bottom: 1px solid #cdd5cf; }
    table { width: 100%; border-collapse: collapse; margin-top: 20px; background: #fff; }
    th, td { padding: 9px 10px; border: 1px solid #d8ded9; text-align: left; vertical-align: top; }
    td:nth-child(3) { white-space: pre-wrap; }
    th { background: #edf2ee; }
    code { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; overflow-wrap: anywhere; }
  </style>
</head>
<body>
<main>
  <h1>AlpineForm {{if eq .Mode "online"}}online{{else}}offline{{end}} plan</h1>
  <div class="summary">{{if eq .Mode "online"}}{{.Summary.Create}} create; {{.Summary.Update}} update; {{.Summary.Adopt}} adopt; {{.Summary.Delete}} delete; {{.Summary.Destroy}} destroy; {{.Summary.Forget}} forget; {{.Summary.NoOp}} no-op.{{else}}{{.Summary.GraphNodes}} graph nodes; {{.Summary.ManagedResources}} managed resources; {{.Summary.Create}} to create; {{.Summary.Delete}} to delete.{{end}}{{if .Summary.NetworkDisruption}} Network disruption: {{.Summary.NetworkDisruption}}.{{end}}</div>
{{if eq .Mode "online"}}  <table>
    <thead><tr><th>Address</th><th>Action</th>{{if .Summary.NetworkDisruption}}<th>Risk</th>{{end}}<th>Summary</th><th>Source</th></tr></thead>
    <tbody>{{range .Changes}}<tr><td><code>{{.Address}}</code></td><td>{{.Action}}</td>{{if $.Summary.NetworkDisruption}}<td>{{range .Risks}}{{.}} {{end}}</td>{{end}}<td>{{.Summary}}</td><td><code>{{.Source.File}}:{{.Source.Line}}</code></td></tr>{{end}}</tbody>
  </table>{{else}}  <table>
    <thead><tr><th>Address</th><th>Kind</th><th>Managed</th><th>Source</th></tr></thead>
    <tbody>{{range .Graph}}<tr><td><code>{{.Address}}</code></td><td>{{.Kind}}</td><td>{{.Managed}}</td><td><code>{{.Source.File}}:{{.Source.Line}}</code></td></tr>{{end}}</tbody>
  </table>{{end}}
</main>
</body>
</html>
`

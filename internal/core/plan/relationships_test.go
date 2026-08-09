package plan

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/merge"
	"github.com/mofelee/alpineform/internal/core/parser"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

type renderedDocument struct {
	text []byte
	json []byte
	html []byte
}

func TestOpenRCRelationshipsAcrossPlanFormats(t *testing.T) {
	path := filepath.Join("testdata", "relationships.apf.hcl")
	offline, resourceGraph := compilePlanFixture(t, path)
	online := onlinePlanForGraph(resourceGraph, []string{path})
	secondOffline, secondGraph := compilePlanFixture(t, path)
	secondOnline := onlinePlanForGraph(secondGraph, []string{path})

	serviceAddress := `host.node.services.service["worker"]`
	wantDependsOn := []string{
		"host.node",
		`host.node.files.file["/etc/conf.d/worker"]`,
		`host.node.files.file["/etc/init.d/worker"]`,
		`host.node.packages.package["worker-daemon"]`,
	}
	wantTriggeredBy := []string{
		`host.node.files.file["/etc/conf.d/worker"]`,
		`host.node.files.file["/etc/init.d/worker"]`,
	}
	for _, test := range []struct {
		name           string
		document       Document
		secondDocument Document
	}{
		{name: "offline", document: offline, secondDocument: secondOffline},
		{name: "online", document: online, secondDocument: secondOnline},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertRelationshipsAcrossFormats(t, test.document, serviceAddress, wantDependsOn, wantTriggeredBy)
			assertRenderedDocumentsEqual(t, test.document, test.secondDocument)
		})
	}
}

func TestSharedOnChangeRelationshipsAcrossPlanFormats(t *testing.T) {
	path := filepath.Join("..", "..", "..", "test", "integration", "libvirt", "cases", "components", "1.apf.hcl")
	offline, resourceGraph := compilePlanFixture(t, path)
	online := onlinePlanForGraph(resourceGraph, []string{path})

	scriptAddress := `host.cihost.script["record_component_change"]`
	want := []string{
		`host.cihost.component.tool.artifact.install["/usr/local/bin/apf-ci-tool"]`,
		`host.cihost.component.tool.files.file["/etc/apf-ci-component.conf"]`,
	}
	for _, test := range []struct {
		name     string
		document Document
	}{
		{name: "offline", document: offline},
		{name: "online", document: online},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertRelationshipsAcrossFormats(t, test.document, scriptAddress, want, want)
		})
	}
}

func TestOnlinePlanCarriesOnlyActiveTriggersThroughEveryFormat(t *testing.T) {
	first := graph.Node{Host: "node", Address: "host.node.file.init", Kind: "file", Managed: true, Desired: map[string]any{"value": "init"}}
	second := graph.Node{Host: "node", Address: "host.node.file.conf", Kind: "file", Managed: true, Desired: map[string]any{"value": "conf"}}
	service := graph.Node{
		Host: "node", Address: "host.node.service.worker", Kind: "service", Managed: true,
		Desired:   map[string]any{"state": "running", "operation": "restarted"},
		DependsOn: []string{second.Address, first.Address}, TriggeredBy: []string{second.Address, first.Address},
	}
	prior := corestate.State{
		Product: corestate.Product, SchemaVersion: corestate.SchemaVersion, Host: "node",
		Resources: map[string]corestate.Resource{
			first.Address:   {DesiredDigest: corestate.Digest(first.Desired)},
			second.Address:  {DesiredDigest: corestate.Digest(second.Desired)},
			service.Address: {DesiredDigest: corestate.Digest(service.Desired)},
		},
	}
	provider := relationshipProvider{
		first.Address:   {Exists: true, Digest: "drifted-init"},
		second.Address:  {Exists: true, Digest: corestate.Digest(second.Desired)},
		service.Address: {Exists: true, Digest: corestate.Digest(service.Desired)},
	}
	resourceGraph := &graph.ResourceGraph{Nodes: []graph.Node{first, second, service}}
	actionPlan, err := (engine.Engine{Backend: relationshipBackend{state: prior}, Provider: provider}).Plan(
		context.Background(),
		func(context.Context) (*ir.Program, *graph.ResourceGraph, error) {
			return &ir.Program{Hosts: []ir.HostSpec{{Name: "node"}}}, resourceGraph, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	document := NewOnline(actionPlan, Options{})

	graphNode := graphNodeForAddress(t, document, service.Address)
	wantStructural := []string{second.Address, first.Address}
	sort.Strings(wantStructural)
	if !reflect.DeepEqual(graphNode.TriggeredBy, wantStructural) {
		t.Fatalf("online graph structural triggers = %#v, want %#v", graphNode.TriggeredBy, wantStructural)
	}
	assertRelationshipsAcrossFormats(t, document, service.Address, wantStructural, []string{first.Address})
}

func TestProtectedFeatureRelationshipsRemainStable(t *testing.T) {
	tests := []struct {
		name            string
		path            string
		kind            string
		wantTriggeredBy bool
		forbidden       []string
	}{
		{
			name: "docker", path: filepath.Join("..", "..", "..", "examples", "docker.apf.hcl"), kind: "docker_service", wantTriggeredBy: true,
			forbidden: []string{"APP_MODE=example"},
		},
		{
			name: "nftables", path: filepath.Join("..", "..", "..", "examples", "nftables.apf.hcl"), kind: "nftables_table",
			forbidden: []string{"ct state established,related accept"},
		},
		{
			name: "source-build", path: filepath.Join("..", "..", "..", "examples", "source-build.apf.hcl"), kind: "component_build_workspace", wantTriggeredBy: true,
			forbidden: []string{"this example is intended for Alpine musl"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			firstOffline, firstGraph := compilePlanFixture(t, test.path)
			secondOffline, secondGraph := compilePlanFixture(t, test.path)
			for _, mode := range []struct {
				name   string
				first  Document
				second Document
			}{
				{name: "offline", first: firstOffline, second: secondOffline},
				{name: "online", first: onlinePlanForGraph(firstGraph, []string{test.path}), second: onlinePlanForGraph(secondGraph, []string{test.path})},
			} {
				t.Run(mode.name, func(t *testing.T) {
					first := renderPlanDocument(t, mode.first)
					second := renderPlanDocument(t, mode.second)
					for format, pair := range map[string][2][]byte{
						"text": {first.text, second.text},
						"json": {first.json, second.json},
						"html": {first.html, second.html},
					} {
						if !bytes.Equal(pair[0], pair[1]) {
							t.Fatalf("%s output is not byte-stable", format)
						}
						for _, forbidden := range test.forbidden {
							if bytes.Contains(pair[0], []byte(forbidden)) {
								t.Fatalf("%s output leaked protected fixture data %q", format, forbidden)
							}
						}
					}

					address := graphAddressForKind(t, mode.first, test.kind)
					change := changeForAddress(t, mode.first, address)
					if len(change.DependsOn) == 0 {
						t.Fatalf("%s change has no dependencies", address)
					}
					if test.wantTriggeredBy && len(change.TriggeredBy) == 0 {
						t.Fatalf("%s change has no triggers", address)
					}
					assertRelationshipsAcrossFormats(t, mode.first, address, change.DependsOn, change.TriggeredBy)
				})
			}
		})
	}
}

func TestPlanJSONRemainsCompatibleWithAdditiveConsumers(t *testing.T) {
	document, _ := compilePlanFixture(t, filepath.Join("testdata", "relationships.apf.hcl"))
	rendered := renderPlanDocument(t, document)
	var legacy struct {
		FormatVersion string `json:"format_version"`
		Changes       []struct {
			Address string `json:"address"`
			Action  string `json:"action"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(rendered.json, &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.FormatVersion != "alpineform.plan.alpha1" || len(legacy.Changes) == 0 || legacy.Changes[0].Address == "" || legacy.Changes[0].Action == "" {
		t.Fatalf("legacy consumer projection = %#v", legacy)
	}
}

func compilePlanFixture(t *testing.T, path string) (Document, *graph.ResourceGraph) {
	t.Helper()
	config, err := parser.ParseFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	program, err := merge.Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	resourceGraph, err := graph.Compile(program)
	if err != nil {
		t.Fatal(err)
	}
	hosts := make([]string, 0, len(program.Hosts))
	for _, host := range program.Hosts {
		hosts = append(hosts, host.Name)
	}
	return New(resourceGraph, Options{Files: []string{path}, Hosts: hosts}), resourceGraph
}

func onlinePlanForGraph(resourceGraph *graph.ResourceGraph, files []string) Document {
	stepsByHost := map[string][]engine.Step{}
	for _, node := range resourceGraph.Nodes {
		if !node.Managed {
			continue
		}
		stepsByHost[node.Host] = append(stepsByHost[node.Host], engine.Step{
			Host:        node.Host,
			Address:     node.Address,
			Action:      engine.ActionCreate,
			Summary:     node.Summary,
			Node:        node,
			TriggeredBy: append([]string(nil), node.TriggeredBy...),
		})
	}
	hostNames := make([]string, 0, len(stepsByHost))
	for host := range stepsByHost {
		hostNames = append(hostNames, host)
	}
	sort.Strings(hostNames)
	hostPlans := make([]engine.HostPlan, 0, len(hostNames))
	for _, host := range hostNames {
		hostPlans = append(hostPlans, engine.HostPlan{Host: ir.HostSpec{Name: host}, Steps: stepsByHost[host]})
	}
	return NewOnline(engine.Plan{Hosts: hostPlans}, Options{Files: files})
}

func assertRelationshipsAcrossFormats(t *testing.T, document Document, address string, wantDependsOn, wantTriggeredBy []string) {
	t.Helper()
	change := changeForAddress(t, document, address)
	if !reflect.DeepEqual(change.DependsOn, wantDependsOn) {
		t.Fatalf("%s depends_on = %#v, want %#v", address, change.DependsOn, wantDependsOn)
	}
	if !reflect.DeepEqual(change.TriggeredBy, wantTriggeredBy) {
		t.Fatalf("%s triggered_by = %#v, want %#v", address, change.TriggeredBy, wantTriggeredBy)
	}

	rendered := renderPlanDocument(t, document)
	textBlock := textBlockForChange(t, document, change, string(rendered.text))
	if len(wantDependsOn) > 0 && !strings.Contains(textBlock, "    depends_on: "+strings.Join(wantDependsOn, ", ")) {
		t.Fatalf("text output omitted dependencies for %s:\n%s", address, rendered.text)
	}
	if len(wantTriggeredBy) > 0 && !strings.Contains(textBlock, "    triggered_by: "+strings.Join(wantTriggeredBy, ", ")) {
		t.Fatalf("text output omitted triggers for %s:\n%s", address, rendered.text)
	}

	var decoded Document
	if err := json.Unmarshal(rendered.json, &decoded); err != nil {
		t.Fatal(err)
	}
	decodedChange := changeForAddress(t, decoded, address)
	if !reflect.DeepEqual(decodedChange.DependsOn, wantDependsOn) || !reflect.DeepEqual(decodedChange.TriggeredBy, wantTriggeredBy) {
		t.Fatalf("JSON relationships for %s = %#v / %#v", address, decodedChange.DependsOn, decodedChange.TriggeredBy)
	}

	htmlOutput := string(rendered.html)
	escapedAddress := html.EscapeString(address)
	rowStart := strings.Index(htmlOutput, "<tr><td><code>"+escapedAddress+"</code></td>")
	if rowStart < 0 {
		t.Fatalf("HTML output omitted row for %s", address)
	}
	rowEnd := strings.Index(htmlOutput[rowStart:], "</tr>")
	if rowEnd < 0 {
		t.Fatalf("HTML output has an unterminated row for %s", address)
	}
	row := htmlOutput[rowStart : rowStart+rowEnd]
	if len(wantDependsOn) > 0 && !strings.Contains(row, "depends_on:") {
		t.Fatalf("HTML output omitted depends_on label for %s", address)
	}
	if len(wantTriggeredBy) > 0 && !strings.Contains(row, "triggered_by:") {
		t.Fatalf("HTML output omitted triggered_by label for %s", address)
	}
	for _, relationship := range append(append([]string(nil), wantDependsOn...), wantTriggeredBy...) {
		if !strings.Contains(row, "<code>"+html.EscapeString(relationship)+"</code>") {
			t.Fatalf("HTML output omitted relationship %s", relationship)
		}
	}
}

func textBlockForChange(t *testing.T, document Document, change Change, output string) string {
	t.Helper()
	header := "  " + change.Action + " " + change.Address
	if document.Mode == "offline" {
		symbol := "+"
		if change.Action == engine.ActionDelete {
			symbol = "-"
		}
		header = "  " + symbol + " " + change.Address
	}
	lines := strings.Split(output, "\n")
	for index, line := range lines {
		if line != header {
			continue
		}
		end := index + 1
		for end < len(lines) && strings.HasPrefix(lines[end], "    ") {
			end++
		}
		return strings.Join(lines[index:end], "\n")
	}
	t.Fatalf("text output omitted change block %q", header)
	return ""
}

func renderPlanDocument(t *testing.T, document Document) renderedDocument {
	t.Helper()
	var rendered renderedDocument
	var textOutput, jsonOutput, htmlOutput bytes.Buffer
	PrintText(&textOutput, document, TextOptions{})
	if err := PrintJSON(&jsonOutput, document); err != nil {
		t.Fatal(err)
	}
	if err := PrintHTML(&htmlOutput, document); err != nil {
		t.Fatal(err)
	}
	rendered.text = textOutput.Bytes()
	rendered.json = jsonOutput.Bytes()
	rendered.html = htmlOutput.Bytes()
	return rendered
}

func assertRenderedDocumentsEqual(t *testing.T, firstDocument, secondDocument Document) {
	t.Helper()
	first := renderPlanDocument(t, firstDocument)
	second := renderPlanDocument(t, secondDocument)
	for format, pair := range map[string][2][]byte{
		"text": {first.text, second.text},
		"json": {first.json, second.json},
		"html": {first.html, second.html},
	} {
		if !bytes.Equal(pair[0], pair[1]) {
			t.Fatalf("%s output is not byte-stable", format)
		}
	}
}

func changeForAddress(t *testing.T, document Document, address string) Change {
	t.Helper()
	for _, change := range document.Changes {
		if change.Address == address {
			return change
		}
	}
	t.Fatalf("plan has no change for %s", address)
	return Change{}
}

func graphAddressForKind(t *testing.T, document Document, kind string) string {
	t.Helper()
	for _, node := range document.Graph {
		if node.Kind == kind {
			return node.Address
		}
	}
	t.Fatalf("plan graph has no %s node", kind)
	return ""
}

func graphNodeForAddress(t *testing.T, document Document, address string) GraphNode {
	t.Helper()
	for _, node := range document.Graph {
		if node.Address == address {
			return node
		}
	}
	t.Fatalf("plan graph has no node for %s", address)
	return GraphNode{}
}

type relationshipBackend struct {
	state corestate.State
}

func (backend relationshipBackend) Read(context.Context, ir.HostSpec) (corestate.State, error) {
	return backend.state, nil
}

func (backend relationshipBackend) Write(_ context.Context, _ ir.HostSpec, state corestate.State) (corestate.State, error) {
	return state, nil
}

func (relationshipBackend) WithLease(ctx context.Context, _ ir.HostSpec, _ time.Duration, work func(context.Context) error) error {
	return work(ctx)
}

type relationshipProvider map[string]engine.ObservedResource

func (provider relationshipProvider) Inspect(_ context.Context, node graph.Node) (engine.ObservedResource, error) {
	return provider[node.Address], nil
}

func (relationshipProvider) Apply(_ context.Context, _ engine.Step) (engine.ObservedResource, error) {
	return engine.ObservedResource{}, nil
}

func (relationshipProvider) Delete(_ context.Context, _ engine.Step) error {
	return nil
}

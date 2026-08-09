package ir

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestComponentArtifactSourceJSONRedactsTemplateAndSelectedValues(t *testing.T) {
	const (
		urlSecret = "https://not-a-real-secret.invalid/tool"
		shaSecret = "not-a-real-ephemeral-checksum"
	)
	source := ComponentArtifactSourceSpec{
		URL:             urlSecret,
		SHA256:          shaSecret,
		URLSensitive:    true,
		URLEphemeral:    true,
		SHA256Ephemeral: true,
		URLSource:       SourceRef{File: "main.apf.hcl", Line: 4, Path: "component.tool.source.url"},
		SHA256Source:    SourceRef{File: "main.apf.hcl", Line: 5, Path: "component.tool.source.sha256"},
	}
	program := Program{
		Components: map[string]ComponentTemplateSpec{
			"tool": {Sources: map[string]ComponentArtifactSourceSpec{"": source}},
		},
		Hosts: []HostSpec{{Components: []ComponentInstanceSpec{{SelectedSource: &source}}}},
	}
	encoded, err := json.Marshal(program)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, urlSecret) || strings.Contains(text, shaSecret) {
		t.Fatalf("artifact source JSON leaked protected values: %s", text)
	}
	if strings.Count(text, `"url":"\u003csensitive\u003e"`) != 2 || strings.Count(text, `"sha256":"\u003cephemeral\u003e"`) != 2 {
		t.Fatalf("artifact source JSON markers = %s", text)
	}
	for _, field := range []string{`"url_source"`, `"sha256_source"`, `"url_sensitive":true`, `"url_ephemeral":true`, `"sha256_ephemeral":true`} {
		if !strings.Contains(text, field) {
			t.Fatalf("artifact source JSON = %s, missing %s", text, field)
		}
	}
}

package parser

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestEnvironmentVariableSourcesOnlyReadsAPFPrefix(t *testing.T) {
	got := EnvironmentVariableSources([]string{
		"DBF_VAR_region=foreign",
		"APF_VAR_zone=two",
		"APF_VAR_region=one",
		"APF_VAR_=ignored",
	})
	want := []VariableSource{
		{Name: "region", Value: "one", Kind: VariableEnvironment, Origin: "APF_VAR_region"},
		{Name: "zone", Value: "two", Kind: VariableEnvironment, Origin: "APF_VAR_zone"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EnvironmentVariableSources() = %#v, want %#v", got, want)
	}
}

func TestDiscoverAutomaticVariableFilesUsesAlpineFormNames(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "main.apf.hcl")
	for _, path := range []string{
		config,
		filepath.Join(dir, "alpineform.apfvars"),
		filepath.Join(dir, "alpineform.apfvars.json"),
		filepath.Join(dir, "20.auto.apfvars"),
		filepath.Join(dir, "10.auto.apfvars.json"),
		filepath.Join(dir, "debianform.dbfvars"),
	} {
		touch(t, path)
	}
	got, err := DiscoverAutomaticVariableFiles([]string{config})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(dir, "alpineform.apfvars"),
		filepath.Join(dir, "alpineform.apfvars.json"),
		filepath.Join(dir, "10.auto.apfvars.json"),
		filepath.Join(dir, "20.auto.apfvars"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DiscoverAutomaticVariableFiles() = %#v, want %#v", got, want)
	}
}

func TestResolveVariableSourcesUsesLastValue(t *testing.T) {
	resolved := ResolveVariableSources(
		[]VariableSource{{Name: "region", Value: "default", Kind: VariableDefault}},
		[]VariableSource{{Name: "region", Value: "env", Kind: VariableEnvironment}},
		[]VariableSource{{Name: "region", Value: "auto", Kind: VariableAutomatic}},
		[]VariableSource{{Name: "region", Value: "file", Kind: VariableExplicitFile}},
		[]VariableSource{{Name: "region", Value: "cli", Kind: VariableCLI}},
	)
	if got := resolved["region"]; got.Value != "cli" || got.Kind != VariableCLI {
		t.Fatalf("resolved region = %#v, want CLI value", got)
	}
}

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionUsesAPFProductName(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"version"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "apf ") {
		t.Fatalf("version output = %q", output.String())
	}
}

func TestHelpHasOnlyAlpineFormNames(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"help"}, &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{"AlpineForm", "apf validate", "*.apf.hcl"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help output missing %q:\n%s", want, text)
		}
	}
	for _, forbidden := range []string{"DebianForm", "dbf", ".dbf.hcl"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("help output contains %q:\n%s", forbidden, text)
		}
	}
}

func TestResourceCommandsFailExplicitlyInBootstrap(t *testing.T) {
	err := run([]string{"apply"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not available in the bootstrap build") {
		t.Fatalf("apply error = %v", err)
	}
}

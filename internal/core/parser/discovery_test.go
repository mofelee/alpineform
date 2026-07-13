package parser

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverConfigFilesDefaultIsSortedAndNonRecursive(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "20.apf.hcl"))
	touch(t, filepath.Join(dir, "10.apf.hcl"))
	touch(t, filepath.Join(dir, "ignored.hcl"))
	child := filepath.Join(dir, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(child, "nested.apf.hcl"))

	got, err := DiscoverConfigFiles(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "10.apf.hcl"), filepath.Join(dir, "20.apf.hcl")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DiscoverConfigFiles() = %#v, want %#v", got, want)
	}
}

func TestDiscoverConfigFilesPreservesSourceOrderAndSortsDirectories(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "configs")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(root, "first.apf.hcl")
	touch(t, first)
	touch(t, filepath.Join(dir, "b.apf.hcl"))
	touch(t, filepath.Join(dir, "a.apf.hcl"))

	got, err := DiscoverConfigFiles(root, []string{first, dir, first})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{first, filepath.Join(dir, "a.apf.hcl"), filepath.Join(dir, "b.apf.hcl"), first}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DiscoverConfigFiles() = %#v, want %#v", got, want)
	}
}

func TestDiscoverConfigFilesUsesAlpineFormSuffixInErrors(t *testing.T) {
	_, err := DiscoverConfigFiles(t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "*.apf.hcl") {
		t.Fatalf("error = %v, want AlpineForm suffix", err)
	}
}

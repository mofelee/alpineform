package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mofelee/alpineform/internal/product"
)

type VariableSourceKind string

const (
	VariableDefault      VariableSourceKind = "default"
	VariableEnvironment  VariableSourceKind = "environment"
	VariableAutomatic    VariableSourceKind = "automatic-file"
	VariableExplicitFile VariableSourceKind = "explicit-file"
	VariableCLI          VariableSourceKind = "cli"
)

type VariableSource struct {
	Name   string
	Value  string
	Kind   VariableSourceKind
	Origin string
}

func EnvironmentVariableSources(environ []string) []VariableSource {
	var values []VariableSource
	for _, item := range environ {
		nameValue := strings.SplitN(item, "=", 2)
		if len(nameValue) != 2 || !strings.HasPrefix(nameValue[0], product.EnvironmentPrefix) {
			continue
		}
		name := strings.TrimPrefix(nameValue[0], product.EnvironmentPrefix)
		if name == "" {
			continue
		}
		values = append(values, VariableSource{Name: name, Value: nameValue[1], Kind: VariableEnvironment, Origin: nameValue[0]})
	}
	sort.SliceStable(values, func(i, j int) bool { return values[i].Origin < values[j].Origin })
	return values
}

func DiscoverAutomaticVariableFiles(configFiles []string) ([]string, error) {
	var files []string
	seenDirs := map[string]bool{}
	for _, configFile := range configFiles {
		dir := filepath.Dir(configFile)
		absolute, err := filepath.Abs(filepath.Clean(dir))
		if err != nil {
			return nil, err
		}
		if seenDirs[absolute] {
			continue
		}
		seenDirs[absolute] = true

		for _, name := range []string{product.DefaultVarFile, product.DefaultVarJSONFile} {
			path := filepath.Join(dir, name)
			info, err := os.Stat(path)
			switch {
			case err == nil && !info.Mode().IsRegular():
				return nil, fmt.Errorf("automatic variable source %s is not a regular file", path)
			case err == nil:
				files = append(files, path)
			case !os.IsNotExist(err):
				return nil, err
			}
		}

		plain, err := filepath.Glob(filepath.Join(dir, "*"+product.AutoVarSuffix))
		if err != nil {
			return nil, err
		}
		jsonFiles, err := filepath.Glob(filepath.Join(dir, "*"+product.AutoVarJSONSuffix))
		if err != nil {
			return nil, err
		}
		auto := append(plain, jsonFiles...)
		sort.Strings(auto)
		files = append(files, auto...)
	}
	return files, nil
}

func ResolveVariableSources(layers ...[]VariableSource) map[string]VariableSource {
	resolved := map[string]VariableSource{}
	for _, layer := range layers {
		for _, value := range layer {
			resolved[value.Name] = value
		}
	}
	return resolved
}

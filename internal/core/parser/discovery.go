// Package parser owns configuration source discovery and, in later loops, HCL decoding.
package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mofelee/alpineform/internal/product"
)

func DiscoverConfigFiles(workingDir string, sources []string) ([]string, error) {
	if workingDir == "" {
		workingDir = "."
	}
	if len(sources) == 0 {
		matches, err := filepath.Glob(filepath.Join(workingDir, "*"+product.ConfigSuffix))
		if err != nil {
			return nil, err
		}
		sort.Strings(matches)
		if len(matches) == 0 {
			return nil, fmt.Errorf("no configuration file found; pass -f or create one or more *%s files in the current directory", product.ConfigSuffix)
		}
		return matches, nil
	}

	var files []string
	for _, source := range sources {
		info, err := os.Stat(source)
		if err != nil {
			return nil, fmt.Errorf("configuration source %s: %w", source, err)
		}
		if info.IsDir() {
			matches, err := filepath.Glob(filepath.Join(source, "*"+product.ConfigSuffix))
			if err != nil {
				return nil, err
			}
			sort.Strings(matches)
			if len(matches) == 0 {
				return nil, fmt.Errorf("no *%s configuration file found in directory %s", product.ConfigSuffix, source)
			}
			files = append(files, matches...)
			continue
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("configuration source %s is not a regular file or directory", source)
		}
		if !strings.HasSuffix(info.Name(), product.ConfigSuffix) {
			return nil, fmt.Errorf("configuration file %s must use the *%s suffix", source, product.ConfigSuffix)
		}
		files = append(files, source)
	}
	return files, nil
}

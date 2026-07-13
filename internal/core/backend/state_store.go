package backend

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	corestate "github.com/mofelee/alpineform/internal/core/state"
	"github.com/mofelee/alpineform/internal/product"
)

type StateStore struct {
	Runner Runner
	Path   string
	Now    func() time.Time
}

func (store StateStore) Read(ctx context.Context, host string) (corestate.State, error) {
	path, err := store.statePath()
	if err != nil {
		return corestate.State{}, err
	}
	if store.Runner == nil {
		return corestate.State{}, fmt.Errorf("state store requires a backend runner")
	}
	data, err := store.Runner.Run(ctx, Command{Name: "state.read", Script: stateReadScript(path)})
	if err != nil {
		return corestate.State{}, fmt.Errorf("read remote AlpineForm state for host %q: %w", host, err)
	}
	state, err := corestate.Decode(data, host)
	if err != nil {
		return corestate.State{}, fmt.Errorf("validate remote AlpineForm state for host %q: %w", host, err)
	}
	return state, nil
}

func (store StateStore) Write(ctx context.Context, host string, input corestate.State) (corestate.State, error) {
	path, err := store.statePath()
	if err != nil {
		return corestate.State{}, err
	}
	if store.Runner == nil {
		return corestate.State{}, fmt.Errorf("state store requires a backend runner")
	}
	now := time.Now
	if store.Now != nil {
		now = store.Now
	}
	prepared, err := corestate.PrepareWrite(input, host, now())
	if err != nil {
		return corestate.State{}, err
	}
	data, err := corestate.Encode(prepared)
	if err != nil {
		return corestate.State{}, err
	}
	command := Command{
		Name:        "state.write",
		Script:      stateWriteScript(path),
		Stdin:       append(data, '\n'),
		RedactStdin: true,
	}
	if _, err := store.Runner.Run(ctx, command); err != nil {
		return corestate.State{}, fmt.Errorf("atomically write remote AlpineForm state for host %q: %w", host, err)
	}
	return prepared, nil
}

func (store StateStore) statePath() (string, error) {
	path := store.Path
	if path == "" {
		path = product.DefaultStatePath
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || strings.ContainsAny(path, "\x00\r\n") {
		return "", fmt.Errorf("state path %q must be a clean absolute file path", path)
	}
	return path, nil
}

func stateReadScript(path string) string {
	quotedPath := shellQuote(path)
	return "set -eu\n" +
		"if [ -e " + quotedPath + " ]; then\n" +
		"  [ -f " + quotedPath + " ] || { echo 'state path is not a regular file' >&2; exit 1; }\n" +
		"  cat " + quotedPath + "\n" +
		"fi\n"
}

func stateWriteScript(path string) string {
	directory := filepath.Dir(path)
	template := filepath.Join(directory, ".state.json.tmp.XXXXXX")
	return "set -eu\n" +
		"umask 077\n" +
		"mkdir -p " + shellQuote(directory) + "\n" +
		"chmod 0700 " + shellQuote(directory) + "\n" +
		"tmp=$(mktemp " + shellQuote(template) + ")\n" +
		"cleanup() { rm -f \"$tmp\"; }\n" +
		"trap cleanup EXIT HUP INT TERM\n" +
		"cat >\"$tmp\"\n" +
		"chmod 0600 \"$tmp\"\n" +
		"mv -f \"$tmp\" " + shellQuote(path) + "\n" +
		"trap - EXIT HUP INT TERM\n"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

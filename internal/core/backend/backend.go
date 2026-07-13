// Package backend owns transport-facing state persistence and runtime locks.
package backend

import "context"

type Command struct {
	Name        string
	Script      string
	Stdin       []byte
	RedactStdin bool
	Parameters  map[string]string
}

type Runner interface {
	Run(ctx context.Context, command Command) ([]byte, error)
}

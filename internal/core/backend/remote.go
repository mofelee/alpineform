package backend

import (
	"context"
	"fmt"
	"time"

	"github.com/mofelee/alpineform/internal/core/ir"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

type RunnerFactory func(ir.HostSpec) (Runner, error)

// RemoteBackend binds state and lease operations to a host-specific transport.
// Fact discovery remains separate so target validation can complete first.
type RemoteBackend struct {
	NewRunner     RunnerFactory
	LeaseTTL      time.Duration
	LeasePoll     time.Duration
	StateNow      func() time.Time
	LeaseNow      func() time.Time
	NewLeaseToken func() (string, error)
	LeaseWait     func(context.Context, time.Duration) error
}

func (backend RemoteBackend) Read(ctx context.Context, host ir.HostSpec) (corestate.State, error) {
	runner, err := backend.runner(host)
	if err != nil {
		return corestate.State{}, err
	}
	return (StateStore{Runner: runner, Path: host.State.Path, Now: backend.StateNow}).Read(ctx, host.Name)
}

func (backend RemoteBackend) Write(ctx context.Context, host ir.HostSpec, input corestate.State) (corestate.State, error) {
	runner, err := backend.runner(host)
	if err != nil {
		return corestate.State{}, err
	}
	return (StateStore{Runner: runner, Path: host.State.Path, Now: backend.StateNow}).Write(ctx, host.Name, input)
}

func (backend RemoteBackend) WithLease(ctx context.Context, host ir.HostSpec, timeout time.Duration, work func(context.Context) error) error {
	if work == nil {
		return fmt.Errorf("remote backend lease work function is required")
	}
	runner, err := backend.runner(host)
	if err != nil {
		return err
	}
	locker := LeaseLocker{
		Runner:       runner,
		Path:         host.State.LockPath,
		TTL:          backend.LeaseTTL,
		PollInterval: backend.LeasePoll,
		Now:          backend.LeaseNow,
		NewToken:     backend.NewLeaseToken,
		Wait:         backend.LeaseWait,
	}
	return locker.WithLease(ctx, host.Name, timeout, func(leaseContext context.Context, _ *Lease) error {
		return work(leaseContext)
	})
}

func (backend RemoteBackend) runner(host ir.HostSpec) (Runner, error) {
	if backend.NewRunner == nil {
		return nil, fmt.Errorf("remote backend requires a runner factory")
	}
	runner, err := backend.NewRunner(host)
	if err != nil {
		return nil, fmt.Errorf("create remote backend runner for host %q: %w", host.Name, err)
	}
	if runner == nil {
		return nil, fmt.Errorf("runner factory returned nil for host %q", host.Name)
	}
	return runner, nil
}

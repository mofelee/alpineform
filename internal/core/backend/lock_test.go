package backend

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type simulatedLockRunner struct {
	mu              sync.Mutex
	owner           string
	expires         int64
	failReleaseOnce bool
	failRenewOnce   bool
	commands        []Command
}

func (runner *simulatedLockRunner) Run(_ context.Context, command Command) ([]byte, error) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.commands = append(runner.commands, command)
	now, _ := strconv.ParseInt(command.Parameters["now"], 10, 64)
	switch command.Name {
	case "lock.acquire":
		expires, _ := strconv.ParseInt(command.Parameters["expires"], 10, 64)
		if runner.owner == "" || runner.expires <= now {
			runner.owner = command.Parameters["owner"]
			runner.expires = expires
			return []byte("acquired\n"), nil
		}
		return []byte("busy\n"), nil
	case "lock.renew":
		if runner.failRenewOnce {
			runner.failRenewOnce = false
			return nil, errors.New("renew transport failed")
		}
		if runner.owner != command.Parameters["owner"] || runner.expires <= now {
			return []byte("lost\n"), nil
		}
		runner.expires, _ = strconv.ParseInt(command.Parameters["expires"], 10, 64)
		return []byte("renewed\n"), nil
	case "lock.release":
		if runner.failReleaseOnce {
			runner.failReleaseOnce = false
			return nil, errors.New("unlock transport failed")
		}
		if runner.owner == "" {
			return []byte("missing\n"), nil
		}
		if runner.owner != command.Parameters["owner"] || runner.expires <= now {
			return []byte("lost\n"), nil
		}
		runner.owner = ""
		runner.expires = 0
		return []byte("released\n"), nil
	default:
		return nil, errors.New("unexpected command")
	}
}

func testLocker(runner Runner, now func() time.Time, token string) LeaseLocker {
	return LeaseLocker{
		Runner:       runner,
		TTL:          3 * time.Second,
		PollInterval: time.Second,
		Now:          now,
		NewToken:     func() (string, error) { return token, nil },
	}
}

func TestConcurrentAcquireHasSingleWinner(t *testing.T) {
	runner := &simulatedLockRunner{}
	now := func() time.Time { return time.Unix(100, 0) }
	tokens := []string{"00000000000000000000000000000001", "00000000000000000000000000000002"}
	var winners atomic.Int32
	var leasesMu sync.Mutex
	var leases []*Lease
	var wait sync.WaitGroup
	for _, token := range tokens {
		wait.Add(1)
		go func(token string) {
			defer wait.Done()
			lease, err := testLocker(runner, now, token).Acquire(context.Background(), "node", 0)
			if err == nil {
				winners.Add(1)
				leasesMu.Lock()
				leases = append(leases, lease)
				leasesMu.Unlock()
				return
			}
			if !errors.Is(err, ErrLockTimeout) {
				t.Errorf("Acquire() error = %v", err)
			}
		}(token)
	}
	wait.Wait()
	if winners.Load() != 1 || len(leases) != 1 {
		t.Fatalf("winners = %d, leases = %d", winners.Load(), len(leases))
	}
}

func TestRenewAndReleaseUseOwnerToken(t *testing.T) {
	runner := &simulatedLockRunner{}
	current := time.Unix(100, 0)
	locker := testLocker(runner, func() time.Time { return current }, "00000000000000000000000000000001")
	lease, err := locker.Acquire(context.Background(), "node", 0)
	if err != nil {
		t.Fatal(err)
	}
	firstExpiry := lease.ExpiresAt()
	current = current.Add(time.Second)
	if err := lease.Renew(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !lease.ExpiresAt().After(firstExpiry) {
		t.Fatalf("renewed expiry = %s, first = %s", lease.ExpiresAt(), firstExpiry)
	}
	if err := lease.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(context.Background()); err != nil {
		t.Fatalf("idempotent release: %v", err)
	}
}

func TestStaleLeaseCanBeTakenOver(t *testing.T) {
	runner := &simulatedLockRunner{owner: "00000000000000000000000000000001", expires: 99}
	locker := testLocker(runner, func() time.Time { return time.Unix(100, 0) }, "00000000000000000000000000000002")
	lease, err := locker.Acquire(context.Background(), "node", 0)
	if err != nil {
		t.Fatal(err)
	}
	if runner.owner != lease.token {
		t.Fatalf("owner = %q, want %q", runner.owner, lease.token)
	}
}

func TestAcquireTimeoutAndCancellation(t *testing.T) {
	runner := &simulatedLockRunner{owner: "00000000000000000000000000000001", expires: 1000}
	current := time.Unix(100, 0)
	locker := testLocker(runner, func() time.Time { return current }, "00000000000000000000000000000002")
	locker.Wait = func(_ context.Context, duration time.Duration) error {
		current = current.Add(duration)
		return nil
	}
	_, err := locker.Acquire(context.Background(), "node", 2*time.Second)
	if err == nil || !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("Acquire() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	locker.Wait = func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}
	_, err = locker.Acquire(ctx, "node", 5*time.Second)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Acquire() error = %v", err)
	}
}

func TestWithLeaseCancellationCleansUp(t *testing.T) {
	runner := &simulatedLockRunner{}
	locker := testLocker(runner, time.Now, "00000000000000000000000000000001")
	ctx, cancel := context.WithCancel(context.Background())
	err := locker.WithLease(ctx, "node", 0, func(ctx context.Context, _ *Lease) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("WithLease() error = %v", err)
	}
	runner.mu.Lock()
	owner := runner.owner
	runner.mu.Unlock()
	if owner != "" {
		t.Fatalf("lock owner after cancellation = %q", owner)
	}
}

func TestReleaseFailureCanBeRetried(t *testing.T) {
	runner := &simulatedLockRunner{failReleaseOnce: true}
	locker := testLocker(runner, func() time.Time { return time.Unix(100, 0) }, "00000000000000000000000000000001")
	lease, err := locker.Acquire(context.Background(), "node", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(context.Background()); err == nil || !strings.Contains(err.Error(), "unlock transport failed") {
		t.Fatalf("first Release() error = %v", err)
	}
	if err := lease.Release(context.Background()); err != nil {
		t.Fatalf("retry Release() error = %v", err)
	}
}

func TestRenewFailureCancelsWithLeaseWork(t *testing.T) {
	runner := &simulatedLockRunner{failRenewOnce: true}
	locker := testLocker(runner, time.Now, "00000000000000000000000000000001")
	started := time.Now()
	err := locker.WithLease(context.Background(), "node", 0, func(ctx context.Context, _ *Lease) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if err == nil || !strings.Contains(err.Error(), "renew transport failed") || !errors.Is(err, context.Canceled) {
		t.Fatalf("WithLease() error = %v", err)
	}
	if time.Since(started) > 4*time.Second {
		t.Fatalf("renew failure cancellation took %s", time.Since(started))
	}
	runner.mu.Lock()
	owner := runner.owner
	runner.mu.Unlock()
	if owner != "" {
		t.Fatalf("lock owner after renewal failure = %q", owner)
	}
}

func TestLeaseScriptsWorkAgainstFilesystem(t *testing.T) {
	path := t.TempDir() + "/runtime/lock"
	current := time.Unix(100, 0)
	firstLocker := testLocker(localShellRunner{}, func() time.Time { return current }, "00000000000000000000000000000001")
	firstLocker.Path = path
	first, err := firstLocker.Acquire(context.Background(), "node", 0)
	if err != nil {
		t.Fatal(err)
	}
	secondLocker := testLocker(localShellRunner{}, func() time.Time { return current }, "00000000000000000000000000000002")
	secondLocker.Path = path
	if _, err := secondLocker.Acquire(context.Background(), "node", 0); err == nil || !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("contending Acquire() error = %v", err)
	}
	current = current.Add(time.Second)
	if err := first.Renew(context.Background()); err != nil {
		t.Fatal(err)
	}
	current = current.Add(4 * time.Second)
	second, err := secondLocker.Acquire(context.Background(), "node", 0)
	if err != nil {
		t.Fatalf("stale takeover: %v", err)
	}
	if err := first.Release(context.Background()); err == nil || !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old owner Release() error = %v", err)
	}
	if err := second.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredLeaseCannotBeRenewedOrReleased(t *testing.T) {
	runner := &simulatedLockRunner{}
	current := time.Unix(100, 0)
	locker := testLocker(runner, func() time.Time { return current }, "00000000000000000000000000000001")
	lease, err := locker.Acquire(context.Background(), "node", 0)
	if err != nil {
		t.Fatal(err)
	}
	current = current.Add(4 * time.Second)
	if err := lease.Renew(context.Background()); err == nil || !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired Renew() error = %v", err)
	}
	if err := lease.Release(context.Background()); err == nil || !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired Release() error = %v", err)
	}
}

func TestLockScriptsContainLeaseProtocol(t *testing.T) {
	path := "/run/lock/alpineform/lock"
	owner := "00000000000000000000000000000001"
	acquire := lockAcquireScript(path, owner, 100, 130)
	for _, want := range []string{"mkdir \"$lock\"", "expires_at", "stat -c %Y", ".stale." + owner, "mv \"$lock\" \"$stale\"", "echo acquired", "echo busy"} {
		if !strings.Contains(acquire, want) {
			t.Fatalf("acquire script missing %q:\n%s", want, acquire)
		}
	}
	renew := lockRenewScript(path, owner, 100, 130)
	for _, want := range []string{owner, "current_expiry", "echo lost", "echo renewed", "mv -f"} {
		if !strings.Contains(renew, want) {
			t.Fatalf("renew script missing %q:\n%s", want, renew)
		}
	}
	release := lockReleaseScript(path, owner, 100)
	for _, want := range []string{owner, "current_expiry", "rm -rf \"$lock\"", "echo released", "echo lost"} {
		if !strings.Contains(release, want) {
			t.Fatalf("release script missing %q:\n%s", want, release)
		}
	}
}

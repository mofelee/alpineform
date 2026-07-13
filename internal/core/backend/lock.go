package backend

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mofelee/alpineform/internal/product"
)

var (
	ErrLockTimeout = errors.New("lock acquisition timed out")
	ErrLeaseLost   = errors.New("lock lease is no longer owned")
	ownerPattern   = regexp.MustCompile(`^[a-f0-9]{32}$`)
)

type LeaseLocker struct {
	Runner       Runner
	Path         string
	TTL          time.Duration
	PollInterval time.Duration
	Now          func() time.Time
	NewToken     func() (string, error)
	Wait         func(context.Context, time.Duration) error
}

type Lease struct {
	locker    LeaseLocker
	host      string
	token     string
	mu        sync.Mutex
	expiresAt time.Time
	released  bool
}

func (locker LeaseLocker) Acquire(ctx context.Context, host string, timeout time.Duration) (*Lease, error) {
	configured, err := locker.normalized()
	if err != nil {
		return nil, err
	}
	if host == "" {
		return nil, fmt.Errorf("cannot acquire lock without a host")
	}
	if timeout < 0 {
		return nil, fmt.Errorf("lock timeout must not be negative")
	}
	token, err := configured.NewToken()
	if err != nil {
		return nil, fmt.Errorf("generate lock owner token: %w", err)
	}
	if !ownerPattern.MatchString(token) {
		return nil, fmt.Errorf("generated lock owner token has invalid format")
	}
	started := configured.Now()
	deadline := started.Add(timeout)
	for {
		now := configured.Now()
		expiresAt := now.Add(configured.TTL)
		acquired, err := configured.tryAcquire(ctx, token, now, expiresAt)
		if err != nil {
			return nil, fmt.Errorf("acquire lock for host %q: %w", host, err)
		}
		if acquired {
			return &Lease{locker: configured, host: host, token: token, expiresAt: expiresAt}, nil
		}
		if timeout == 0 || !now.Before(deadline) {
			return nil, fmt.Errorf("%w for host %q after %s", ErrLockTimeout, host, timeout)
		}
		wait := configured.PollInterval
		if remaining := deadline.Sub(now); wait > remaining {
			wait = remaining
		}
		if err := configured.Wait(ctx, wait); err != nil {
			return nil, fmt.Errorf("wait for lock on host %q: %w", host, err)
		}
	}
}

func (locker LeaseLocker) WithLease(ctx context.Context, host string, timeout time.Duration, work func(context.Context, *Lease) error) error {
	if work == nil {
		return fmt.Errorf("lease work function is required")
	}
	lease, err := locker.Acquire(ctx, host, timeout)
	if err != nil {
		return err
	}
	leaseContext, cancel := context.WithCancel(ctx)
	renewResult := make(chan error, 1)
	go func() {
		renewErr := lease.renewLoop(leaseContext)
		if renewErr != nil {
			cancel()
		}
		renewResult <- renewErr
	}()
	workErr := work(leaseContext, lease)
	cancel()
	renewErr := <-renewResult
	cleanupTimeout := lease.locker.TTL
	if cleanupTimeout > 5*time.Second {
		cleanupTimeout = 5 * time.Second
	}
	cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), cleanupTimeout)
	releaseErr := lease.Release(cleanupContext)
	cleanupCancel()
	return errors.Join(workErr, renewErr, releaseErr)
}

func (lease *Lease) Host() string {
	return lease.host
}

func (lease *Lease) ExpiresAt() time.Time {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	return lease.expiresAt
}

func (lease *Lease) Renew(ctx context.Context) error {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released {
		return ErrLeaseLost
	}
	now := lease.locker.Now()
	expiresAt := now.Add(lease.locker.TTL)
	output, err := lease.locker.Runner.Run(ctx, Command{
		Name:   "lock.renew",
		Script: lockRenewScript(lease.locker.Path, lease.token, now.Unix(), expiresAt.Unix()),
		Parameters: map[string]string{
			"owner":   lease.token,
			"now":     strconv.FormatInt(now.Unix(), 10),
			"expires": strconv.FormatInt(expiresAt.Unix(), 10),
		},
	})
	if err != nil {
		return fmt.Errorf("renew lock for host %q: %w", lease.host, err)
	}
	switch strings.TrimSpace(string(output)) {
	case "renewed":
		lease.expiresAt = expiresAt
		return nil
	case "lost":
		return fmt.Errorf("%w for host %q", ErrLeaseLost, lease.host)
	default:
		return fmt.Errorf("renew lock for host %q returned unexpected response %q", lease.host, strings.TrimSpace(string(output)))
	}
}

func (lease *Lease) Release(ctx context.Context) error {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released {
		return nil
	}
	now := lease.locker.Now()
	output, err := lease.locker.Runner.Run(ctx, Command{
		Name:   "lock.release",
		Script: lockReleaseScript(lease.locker.Path, lease.token, now.Unix()),
		Parameters: map[string]string{
			"owner": lease.token,
			"now":   strconv.FormatInt(now.Unix(), 10),
		},
	})
	if err != nil {
		return fmt.Errorf("release lock for host %q: %w", lease.host, err)
	}
	switch strings.TrimSpace(string(output)) {
	case "released", "missing":
		lease.released = true
		return nil
	case "lost":
		return fmt.Errorf("%w for host %q", ErrLeaseLost, lease.host)
	default:
		return fmt.Errorf("release lock for host %q returned unexpected response %q", lease.host, strings.TrimSpace(string(output)))
	}
}

func (lease *Lease) renewLoop(ctx context.Context) error {
	interval := lease.locker.TTL / 3
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := lease.Renew(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}

func (locker LeaseLocker) tryAcquire(ctx context.Context, token string, now, expiresAt time.Time) (bool, error) {
	output, err := locker.Runner.Run(ctx, Command{
		Name:   "lock.acquire",
		Script: lockAcquireScript(locker.Path, token, now.Unix(), expiresAt.Unix()),
		Parameters: map[string]string{
			"owner":   token,
			"now":     strconv.FormatInt(now.Unix(), 10),
			"expires": strconv.FormatInt(expiresAt.Unix(), 10),
		},
	})
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(string(output)) {
	case "acquired":
		return true, nil
	case "busy":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected lock response %q", strings.TrimSpace(string(output)))
	}
}

func (locker LeaseLocker) normalized() (LeaseLocker, error) {
	if locker.Runner == nil {
		return LeaseLocker{}, fmt.Errorf("lease locker requires a backend runner")
	}
	if locker.Path == "" {
		locker.Path = product.DefaultLockPath
	}
	if !filepath.IsAbs(locker.Path) || filepath.Clean(locker.Path) != locker.Path || locker.Path == "/" || strings.ContainsAny(locker.Path, "\x00\r\n") {
		return LeaseLocker{}, fmt.Errorf("lock path %q must be a clean absolute path", locker.Path)
	}
	if locker.TTL == 0 {
		locker.TTL = 30 * time.Second
	}
	if locker.TTL < time.Second {
		return LeaseLocker{}, fmt.Errorf("lock lease TTL must be at least one second")
	}
	if locker.PollInterval == 0 {
		locker.PollInterval = 250 * time.Millisecond
	}
	if locker.PollInterval < 0 {
		return LeaseLocker{}, fmt.Errorf("lock poll interval must be positive")
	}
	if locker.Now == nil {
		locker.Now = time.Now
	}
	if locker.NewToken == nil {
		locker.NewToken = randomOwnerToken
	}
	if locker.Wait == nil {
		locker.Wait = waitContext
	}
	return locker, nil
}

func randomOwnerToken() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func lockAcquireScript(path, owner string, now, expires int64) string {
	parent := filepath.Dir(path)
	stale := path + ".stale." + owner
	writeMetadata := "printf '%s\\n' " + shellQuote(owner) + " >\"$lock/owner\"\n" +
		"printf '%s\\n' " + strconv.FormatInt(expires, 10) + " >\"$lock/expires_at\"\n" +
		"chmod 0600 \"$lock/owner\" \"$lock/expires_at\"\n"
	ttl := expires - now
	return "set -eu\n" +
		"umask 077\n" +
		"lock=" + shellQuote(path) + "\n" +
		"mkdir -p " + shellQuote(parent) + "\n" +
		"chmod 0700 " + shellQuote(parent) + "\n" +
		"if mkdir \"$lock\" 2>/dev/null; then\n" + writeMetadata + "  echo acquired\n  exit 0\nfi\n" +
		"current_expiry=$(cat \"$lock/expires_at\" 2>/dev/null || true)\n" +
		"case \"$current_expiry\" in ''|*[!0-9]*) created=$(stat -c %Y \"$lock\" 2>/dev/null || echo " + strconv.FormatInt(now, 10) + "); current_expiry=$((created + " + strconv.FormatInt(ttl, 10) + "));; esac\n" +
		"if [ \"$current_expiry\" -gt " + strconv.FormatInt(now, 10) + " ]; then echo busy; exit 0; fi\n" +
		"stale=" + shellQuote(stale) + "\n" +
		"rm -rf \"$stale\"\n" +
		"if mv \"$lock\" \"$stale\" 2>/dev/null && mkdir \"$lock\" 2>/dev/null; then\n" + writeMetadata + "  rm -rf \"$stale\"\n  echo acquired\n  exit 0\nfi\n" +
		"rm -rf \"$stale\"\n" +
		"echo busy\n"
}

func lockRenewScript(path, owner string, now, expires int64) string {
	return "set -eu\n" +
		"lock=" + shellQuote(path) + "\n" +
		"[ -d \"$lock\" ] || { echo lost; exit 0; }\n" +
		"[ \"$(cat \"$lock/owner\" 2>/dev/null || true)\" = " + shellQuote(owner) + " ] || { echo lost; exit 0; }\n" +
		"current_expiry=$(cat \"$lock/expires_at\" 2>/dev/null || true)\n" +
		"case \"$current_expiry\" in ''|*[!0-9]*) echo lost; exit 0;; esac\n" +
		"[ \"$current_expiry\" -gt " + strconv.FormatInt(now, 10) + " ] || { echo lost; exit 0; }\n" +
		"tmp=\"$lock/.expires." + owner + "\"\n" +
		"printf '%s\\n' " + strconv.FormatInt(expires, 10) + " >\"$tmp\"\n" +
		"chmod 0600 \"$tmp\"\n" +
		"mv -f \"$tmp\" \"$lock/expires_at\"\n" +
		"echo renewed\n"
}

func lockReleaseScript(path, owner string, now int64) string {
	return "set -eu\n" +
		"lock=" + shellQuote(path) + "\n" +
		"[ -d \"$lock\" ] || { echo missing; exit 0; }\n" +
		"[ \"$(cat \"$lock/owner\" 2>/dev/null || true)\" = " + shellQuote(owner) + " ] || { echo lost; exit 0; }\n" +
		"current_expiry=$(cat \"$lock/expires_at\" 2>/dev/null || true)\n" +
		"case \"$current_expiry\" in ''|*[!0-9]*) echo lost; exit 0;; esac\n" +
		"[ \"$current_expiry\" -gt " + strconv.FormatInt(now, 10) + " ] || { echo lost; exit 0; }\n" +
		"rm -rf \"$lock\"\n" +
		"echo released\n"
}

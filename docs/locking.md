# Runtime lease lock

Each host uses an exclusive runtime lease at
`/run/lock/alpineform/lock`. The path is under `/run`, so a reboot never
preserves a stale lock.

Acquisition uses atomic directory creation and records a random 128-bit owner
token plus an epoch expiry. Contenders return busy while the lease is active.
An expired directory is renamed out of the lock path before a new owner creates
the lock, so stale takeover still has one winner.

Renew and release operations compare the owner token and reject expired or
replaced leases. The client renews at one third of the TTL. Acquisition honors
the requested timeout and context cancellation; `WithLease` uses a bounded
background context to release after work succeeds, fails, or is canceled.
Transport failures during unlock are returned and the same lease can retry.

package services

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// loginThrottleMaxEntries bounds the throttle map so a flood of failures from
// many sources cannot grow it without limit; expired entries are swept first.
const loginThrottleMaxEntries = 100_000

// loginThrottle tracks failed password logins per (account, client IP) pair.
// Locking the pair instead of the whole account means an attacker hammering the
// well-known "admin" username only locks themselves out, while the real admin
// can still sign in from their own address. State is in memory, so a restart
// is also an out-of-band unlock.
type loginThrottle struct {
	mu      sync.Mutex
	entries map[loginThrottleKey]*loginThrottleEntry
}

type loginThrottleKey struct {
	userID uuid.UUID
	ip     string
}

type loginThrottleEntry struct {
	failures    int
	lockedUntil time.Time
	lastFailure time.Time
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{entries: make(map[loginThrottleKey]*loginThrottleEntry)}
}

// lockedUntil returns the end of the active lock for the pair, or the zero time
// when it is not locked.
func (t *loginThrottle) lockedUntil(userID uuid.UUID, ip string, now time.Time) time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[loginThrottleKey{userID, ip}]
	if !ok || !e.lockedUntil.After(now) {
		return time.Time{}
	}
	return e.lockedUntil
}

// recordFailure counts a failed attempt for the pair and returns the new
// failure count and, once the threshold is reached, the lock expiry.
func (t *loginThrottle) recordFailure(userID uuid.UUID, ip string, now time.Time) (int, time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := loginThrottleKey{userID, ip}
	e, ok := t.entries[key]
	if !ok {
		if len(t.entries) >= loginThrottleMaxEntries {
			t.sweepLocked(now)
		}
		e = &loginThrottleEntry{}
		t.entries[key] = e
	}
	e.failures++
	e.lastFailure = now
	if e.failures >= maxFailedLoginAttempts {
		e.lockedUntil = now.Add(lockoutDuration(e.failures))
	}
	return e.failures, e.lockedUntil
}

// reset clears the pair's state after a successful login.
func (t *loginThrottle) reset(userID uuid.UUID, ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, loginThrottleKey{userID, ip})
}

// sweepLocked drops entries that are no longer locked and have not failed for
// maxLockoutDuration. Caller must hold t.mu.
func (t *loginThrottle) sweepLocked(now time.Time) {
	for k, e := range t.entries {
		if !e.lockedUntil.After(now) && now.Sub(e.lastFailure) > maxLockoutDuration {
			delete(t.entries, k)
		}
	}
}

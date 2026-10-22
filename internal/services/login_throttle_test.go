package services

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLoginThrottleLocksPerAccountAndIP(t *testing.T) {
	th := newLoginThrottle()
	user := uuid.New()
	now := time.Now()

	for i := 1; i < maxFailedLoginAttempts; i++ {
		if _, until := th.recordFailure(user, "203.0.113.9", now); !until.IsZero() {
			t.Fatalf("locked after only %d failures", i)
		}
	}
	_, until := th.recordFailure(user, "203.0.113.9", now)
	if until.IsZero() {
		t.Fatal("expected the pair to be locked after the threshold")
	}
	if th.lockedUntil(user, "203.0.113.9", now).IsZero() {
		t.Fatal("attacker IP should be locked")
	}
	// The same account from a different address is unaffected.
	if !th.lockedUntil(user, "198.51.100.7", now).IsZero() {
		t.Fatal("a different client IP must not be locked out")
	}
	// The lock expires.
	if !th.lockedUntil(user, "203.0.113.9", until.Add(time.Second)).IsZero() {
		t.Fatal("lock should expire")
	}
	th.reset(user, "203.0.113.9")
	if _, ok := th.entries[loginThrottleKey{user, "203.0.113.9"}]; ok {
		t.Fatal("reset should clear the pair")
	}
}

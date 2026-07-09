package middleware

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs goleak after the whole middleware suite so a test that starts a
// background goroutine (the RateLimiter cleanup worker and the AuthCache expiry
// sweeper) without stopping it fails the package rather than leaking silently.
// Every test that constructs a RateLimiter/AuthCache/AuthMiddleware already stops
// it via t.Cleanup/defer, so no IgnoreTopFunction options are needed here.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

package gitops

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestRateLimitStateUnobservedBeforeAnyRecord(t *testing.T) {
	var s rateLimitState
	if _, _, _, observed := s.get(); observed {
		t.Errorf("get() on a fresh rateLimitState reported observed=true, want false")
	}
}

func TestRateLimitStateRecordsValidHeaders(t *testing.T) {
	var s rateLimitState
	resetUnix := time.Now().Add(time.Hour).Unix()
	resp := &http.Response{Header: http.Header{
		"X-Ratelimit-Remaining": {"4200"},
		"X-Ratelimit-Limit":     {"5000"},
		"X-Ratelimit-Reset":     {strconv.FormatInt(resetUnix, 10)},
	}}

	s.record(resp)

	remaining, limit, resetAt, observed := s.get()
	if !observed {
		t.Fatalf("get() after record() reported observed=false, want true")
	}
	if remaining != 4200 || limit != 5000 {
		t.Errorf("get() = (%d, %d), want (4200, 5000)", remaining, limit)
	}
	if resetAt.Unix() != resetUnix {
		t.Errorf("resetAt = %v, want unix %d", resetAt, resetUnix)
	}
}

func TestRateLimitStateIgnoresIncompleteHeaders(t *testing.T) {
	var s rateLimitState
	// Only remaining present: record must leave the state untouched (still
	// unobserved) rather than partially applying it.
	resp := &http.Response{Header: http.Header{"X-Ratelimit-Remaining": {"10"}}}
	s.record(resp)
	if _, _, _, observed := s.get(); observed {
		t.Errorf("get() reported observed=true after a response missing Limit/Reset headers")
	}
}

func TestRateLimitStateDoesNotClobberOnBadSubsequentResponse(t *testing.T) {
	var s rateLimitState
	good := &http.Response{Header: http.Header{
		"X-Ratelimit-Remaining": {"10"},
		"X-Ratelimit-Limit":     {"20"},
		"X-Ratelimit-Reset":     {strconv.FormatInt(time.Now().Unix(), 10)},
	}}
	s.record(good)

	// A malformed follow-up response (unparseable remaining) must not wipe out
	// the last good reading.
	bad := &http.Response{Header: http.Header{
		"X-Ratelimit-Remaining": {"not-a-number"},
		"X-Ratelimit-Limit":     {"20"},
		"X-Ratelimit-Reset":     {"1"},
	}}
	s.record(bad)

	remaining, limit, _, observed := s.get()
	if !observed || remaining != 10 || limit != 20 {
		t.Errorf("get() = (%d, %d, observed=%v) after a malformed follow-up response, want (10, 20, true)", remaining, limit, observed)
	}
}

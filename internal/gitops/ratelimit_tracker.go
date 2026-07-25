package gitops

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// githubRateLimit is the last observed GitHub API rate-limit state, updated
// opportunistically from response headers the metadata fetcher already reads
// (see recordGitHubRateLimit's call sites in github_fetcher.go) rather than
// from any dedicated polling request.
//
//nolint:gochecknoglobals
var githubRateLimit rateLimitState

type rateLimitState struct {
	mu        sync.Mutex
	remaining int
	limit     int
	resetAt   time.Time
	updatedAt time.Time // zero value means "never observed"
}

// record updates the state from a GitHub API response's X-RateLimit-*
// headers, if present. Missing or unparseable headers leave the previous
// state untouched rather than clobbering a good value with zeros. Split out
// as a method on an unexported receiver (rather than operating on the
// package-level githubRateLimit directly) so tests can exercise it against a
// throwaway instance instead of the shared global.
func (s *rateLimitState) record(resp *http.Response) {
	if resp == nil {
		return
	}
	remainingHeader := resp.Header.Get("X-RateLimit-Remaining")
	limitHeader := resp.Header.Get("X-RateLimit-Limit")
	resetHeader := resp.Header.Get("X-RateLimit-Reset")
	if remainingHeader == "" || limitHeader == "" || resetHeader == "" {
		return
	}
	remaining, err := strconv.Atoi(remainingHeader)
	if err != nil {
		return
	}
	limit, err := strconv.Atoi(limitHeader)
	if err != nil {
		return
	}
	resetUnix, err := strconv.ParseInt(resetHeader, 10, 64)
	if err != nil {
		return
	}

	s.mu.Lock()
	s.remaining = remaining
	s.limit = limit
	s.resetAt = time.Unix(resetUnix, 0)
	s.updatedAt = time.Now()
	s.mu.Unlock()
}

// get returns the last-observed rate limit. observed is false until record
// has successfully parsed at least one response, distinguishing "no data
// yet" from a genuine 0-remaining state.
func (s *rateLimitState) get() (remaining, limit int, resetAt time.Time, observed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remaining, s.limit, s.resetAt, !s.updatedAt.IsZero()
}

// recordGitHubRateLimit updates the package-level last-observed GitHub API
// rate limit state. See rateLimitState.record.
func recordGitHubRateLimit(resp *http.Response) {
	githubRateLimit.record(resp)
}

// GetGitHubRateLimit returns the last-observed GitHub API rate limit.
// observed is false until at least one GitHub API response has been read
// this run, distinguishing "no data yet" from a genuine 0-remaining state.
func GetGitHubRateLimit() (remaining, limit int, resetAt time.Time, observed bool) {
	return githubRateLimit.get()
}

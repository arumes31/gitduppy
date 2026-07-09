package middleware

import (
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// rateLimiter is a simple rate limiter using token bucket algorithm.
type rateLimiter struct {
	mu         sync.Mutex
	tokens     float64
	maxTokens  float64 // maximum tokens in bucket
	refillRate float64 // tokens per second
	lastRefill time.Time
}

func newRateLimiter(rps float64, burst int) *rateLimiter {
	return &rateLimiter{
		tokens:     float64(burst),
		maxTokens:  float64(burst),
		refillRate: rps,
		lastRefill: time.Now(),
	}
}

func (rl *rateLimiter) allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(rl.lastRefill).Seconds()
	rl.tokens += elapsed * rl.refillRate
	if rl.tokens > rl.maxTokens {
		rl.tokens = rl.maxTokens
	}
	rl.lastRefill = now

	// Check if we have tokens available
	if rl.tokens >= 1 {
		rl.tokens--
		return true
	}
	return false
}

// RateLimiter handles rate limiting per client.
type RateLimiter struct {
	limiters sync.Map // map[string]*rateLimiter
	rps      float64
	burst    int
	done     chan struct{}
	stopOnce sync.Once
}

// NewRateLimiter creates a new rate limiter.
func NewRateLimiter(requestsPerSecond float64, burstSize int) *RateLimiter {
	rl := &RateLimiter{
		rps:   requestsPerSecond,
		burst: burstSize,
		done:  make(chan struct{}),
	}

	// Start cleanup goroutine to remove inactive limiters
	go rl.cleanupWorker(5 * time.Minute)
	return rl
}

// Stop terminates the background cleanup goroutine. It is safe to call more than
// once, and a no-op for a limiter created without NewRateLimiter (e.g. in tests).
func (rl *RateLimiter) Stop() {
	if rl.done == nil {
		return
	}
	rl.stopOnce.Do(func() { close(rl.done) })
}

// cleanupWorker removes inactive limiters periodically until Stop is called.
func (rl *RateLimiter) cleanupWorker(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-rl.done:
			return
		case <-ticker.C:
			rl.limiters.Range(func(key, value interface{}) bool {
				limiter := value.(*rateLimiter)
				// Read lastRefill under the limiter's lock to avoid racing allow().
				limiter.mu.Lock()
				idle := time.Since(limiter.lastRefill)
				limiter.mu.Unlock()
				// Remove limiters that haven't been used in the last 10 minutes
				if idle > 10*time.Minute {
					rl.limiters.Delete(key)
				}
				return true
			})
		}
	}
}

// getLimiter returns or creates a rate limiter for a key with the given params.
func (rl *RateLimiter) getLimiter(key string, rps float64, burst int) *rateLimiter {
	value, ok := rl.limiters.Load(key)
	if ok {
		return value.(*rateLimiter)
	}

	// Create new limiter
	limiter := newRateLimiter(rps, burst)
	// Use LoadOrStore to prevent race condition
	value, _ = rl.limiters.LoadOrStore(key, limiter)
	return value.(*rateLimiter)
}

// limitFor picks a rate-limit category (key prefix + params) for a request path.
// Authentication and expensive fan-out endpoints (global search) get tighter
// budgets than ordinary API calls so a single client cannot exhaust the server
// through them.
func (rl *RateLimiter) limitFor(path string) (prefix string, rps float64, burst int) {
	switch {
	case strings.HasPrefix(path, "/api/v1/auth"),
		strings.HasPrefix(path, "/api/v1/oauth"):
		// Auth is the most abuse-prone surface (credential stuffing).
		return "auth:", rl.rps / 6, max(1, rl.burst/6)
	case strings.HasPrefix(path, "/api/v1/search"):
		// Global search fans out across every repository's index, so keep it
		// tight to stop one client from monopolizing that work.
		return "expensive:", rl.rps / 3, max(1, rl.burst/3)
	case strings.HasPrefix(path, "/api/v1/dashboard"):
		// The dashboard renders one view by fanning out to ~4 of these cheap
		// (single-digit-millisecond) read endpoints, and it is meant to be
		// opened and refreshed often. The old "expensive" budget throttled a
		// user after only a handful of views, so give the dashboard its own key
		// with the full API budget: isolated from other API traffic, but large
		// enough that ordinary navigation never trips the limiter.
		return "dashboard:", rl.rps, rl.burst
	default:
		return "api:", rl.rps, rl.burst
	}
}

// Middleware returns a rate limiting middleware function.
func (rl *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		prefix, rps, burst := rl.limitFor(c.Request.URL.Path)
		key := prefix + c.ClientIP()

		limiter := rl.getLimiter(key, rps, burst)

		if !limiter.allow() {
			c.JSON(429, gin.H{
				"success": false,
				"errors": []gin.H{
					{
						"code":    "RATE_LIMITED",
						"message": "Too many requests. Please try again later.",
					},
				},
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

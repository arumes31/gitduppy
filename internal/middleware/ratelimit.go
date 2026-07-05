package middleware

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// rateLimiter is a simple rate limiter using token bucket algorithm.
type rateLimiter struct {
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
}

// NewRateLimiter creates a new rate limiter.
func NewRateLimiter(requestsPerSecond float64, burstSize int) *RateLimiter {
	rl := &RateLimiter{
		rps:   requestsPerSecond,
		burst: burstSize,
	}

	// Start cleanup goroutine to remove inactive limiters
	go rl.cleanupWorker(5 * time.Minute)
	return rl
}

// cleanupWorker removes inactive limiters periodically.
func (rl *RateLimiter) cleanupWorker(interval time.Duration) {
	ticker := time.NewTicker(interval)
	for {
		<-ticker.C
		rl.limiters.Range(func(key, value interface{}) bool {
			limiter := value.(*rateLimiter)
			// Remove limiters that haven't been used in the last 10 minutes
			if time.Since(limiter.lastRefill) > 10*time.Minute {
				rl.limiters.Delete(key)
			}
			return true
		})
	}
}

// getLimiter returns or creates a rate limiter for a key.
func (rl *RateLimiter) getLimiter(key string) *rateLimiter {
	value, ok := rl.limiters.Load(key)
	if ok {
		return value.(*rateLimiter)
	}

	// Create new limiter
	limiter := newRateLimiter(rl.rps, rl.burst)
	// Use LoadOrStore to prevent race condition
	value, _ = rl.limiters.LoadOrStore(key, limiter)
	return value.(*rateLimiter)
}

// Middleware returns a rate limiting middleware function.
func (rl *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Use client IP as the key
		key := c.ClientIP()

		// Stricter limits for auth endpoints
		if len(c.Request.URL.Path) >= 14 && c.Request.URL.Path[:14] == "/api/v1/auth" {
			key = "auth:" + key
		} else {
			key = "api:" + key
		}

		limiter := rl.getLimiter(key)

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

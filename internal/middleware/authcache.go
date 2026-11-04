package middleware

import (
	"sync"
	"time"

	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/google/uuid"
)

// authCacheTTL bounds how long a validated user is served from the in-memory auth
// cache before the backing session/API-key row and the user row are re-checked.
// Every authenticated request otherwise costs two DB queries (credential lookup +
// user fetch); caching the resolved user for a short window removes both on the
// hot path.
//
// Staleness window: within this TTL a request can still be served for a
// credential that has just become invalid — a deactivated user (IsActive=false),
// an expired/deleted session, or a revoked/expired API key — because the cache is
// only revalidated against the database on a miss. 30s is a deliberate ceiling on
// that staleness. Security-sensitive transitions that must not wait it out (logout
// and password change, which invalidates every session) evict eagerly via Evict /
// EvictUser; see auth_service.
const authCacheTTL = 30 * time.Second

// authCacheEntry is a cached, validated user plus the moment the entry expires.
// userID is denormalized from the user so EvictUser can purge every entry for a
// user without dereferencing the (immutable, never-mutated) cached pointer.
type authCacheEntry struct {
	user      *models.User
	userID    uuid.UUID
	expiresAt time.Time
}

// AuthCache is a small, concurrency-safe TTL cache of validated users keyed by the
// at-rest hash of the presented credential (a session token hash or an API-key
// hash — the same value stored in the database, never the raw secret). A single
// cache backs both credential types; because entries carry the user id, a
// password change can purge all of a user's cached entries regardless of type.
//
// It follows the RateLimiter cleanup pattern: a sync.Map for lock-free reads plus
// a background sweeper that drops expired entries, stopped via Stop.
//
// Revocation race: a validator reads the credential + user from the database and
// then publishes the result. If a revocation (logout, password change, key
// revoke, deactivation) deletes the row and evicts the cache entry between that
// read and the publish, a naive store would re-insert the just-revoked identity
// for a full TTL. To close that window every eviction bumps a generation counter
// under mu, validators capture the generation with begin() BEFORE touching the
// database, and store() only publishes — under the same mu — when no eviction has
// happened since. A coarse global generation is deliberate: evictions are rare,
// and the only cost of a spurious mismatch is one uncached validation.
type AuthCache struct {
	entries  sync.Map // map[string]authCacheEntry
	mu       sync.Mutex
	gen      uint64 // eviction generation; guarded by mu
	done     chan struct{}
	stopOnce sync.Once
}

// NewAuthCache creates an auth cache and starts its background expiry sweeper.
func NewAuthCache() *AuthCache {
	ac := &AuthCache{done: make(chan struct{})}
	go ac.sweep(time.Minute)
	return ac
}

// get returns the cached user for a credential hash when present and unexpired.
// An expired entry is dropped and reported as a miss so a stale user is never
// returned past its TTL.
func (ac *AuthCache) get(key string) (*models.User, bool) {
	v, ok := ac.entries.Load(key)
	if !ok {
		return nil, false
	}
	entry := v.(authCacheEntry)
	if time.Now().After(entry.expiresAt) {
		ac.entries.Delete(key)
		return nil, false
	}
	return entry.user, true
}

// begin returns the current eviction generation. Validators call it BEFORE the
// database lookup and pass the result to store, so a revocation that lands while
// the lookup is in flight prevents the (now stale) result from being cached.
func (ac *AuthCache) begin() uint64 {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return ac.gen
}

// store caches a validated user under a credential hash for authCacheTTL, but
// only when no eviction has happened since gen was captured by begin. The check
// and the store happen under the same lock Evict/EvictUser take, so an eviction
// can never interleave between them. It reports whether the entry was stored.
func (ac *AuthCache) store(key string, user *models.User, gen uint64) bool {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	if ac.gen != gen {
		return false
	}
	ac.entries.Store(key, authCacheEntry{
		user:      user,
		userID:    user.ID,
		expiresAt: time.Now().Add(authCacheTTL),
	})
	return true
}

// Evict removes a single cached entry by credential hash. Called on logout so a
// deleted session is not served from cache for up to the TTL. Bumping the
// generation also discards any in-flight validation of the same credential.
func (ac *AuthCache) Evict(credentialHash string) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.gen++
	ac.entries.Delete(credentialHash)
}

// EvictUser removes every cached entry belonging to a user. Called on password
// change (which invalidates all of the user's sessions) so a rotated password —
// or a concurrently applied lockout/deactivation — takes effect immediately
// rather than being masked for up to the TTL.
func (ac *AuthCache) EvictUser(userID uuid.UUID) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.gen++
	ac.entries.Range(func(k, v any) bool {
		if entry, ok := v.(authCacheEntry); ok && entry.userID == userID {
			ac.entries.Delete(k)
		}
		return true
	})
}

// sweep periodically drops expired entries so the map does not retain entries for
// credentials that are never presented again. Reads under get() already discard
// expired entries lazily; this bounds memory for the write-once-never-read case.
func (ac *AuthCache) sweep(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ac.done:
			return
		case <-ticker.C:
			now := time.Now()
			ac.entries.Range(func(k, v any) bool {
				if entry, ok := v.(authCacheEntry); ok && now.After(entry.expiresAt) {
					ac.entries.Delete(k)
				}
				return true
			})
		}
	}
}

// Stop terminates the background sweeper. It is safe to call more than once.
func (ac *AuthCache) Stop() {
	if ac.done == nil {
		return
	}
	ac.stopOnce.Do(func() { close(ac.done) })
}

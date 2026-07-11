package middleware

import (
	"context"
	"testing"

	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/pkg/crypto"
	"github.com/google/uuid"
)

// TestAuthMiddlewareSessionCacheHitAvoidsDB proves a primed cache entry is served
// without touching the database: the middleware is constructed with a nil DB, so
// any code path that reached the DB validators would fail. A successful lookup can
// therefore only have come from the cache.
func TestAuthMiddlewareSessionCacheHitAvoidsDB(t *testing.T) {
	m := NewAuthMiddleware(nil) // nil DB: a DB hit returns an error
	t.Cleanup(m.Stop)

	user := &models.User{ID: uuid.New(), IsActive: true}
	const rawToken = "raw-session-token-value"
	m.cache.set(crypto.HashToken(rawToken), user)

	got, err := m.validateSession(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("cache hit should not error with nil DB, got %v", err)
	}
	if got != user {
		t.Fatalf("expected cached user pointer, got %v", got)
	}

	// A different token is not cached, so it must fall through to the (nil) DB and
	// fail — confirming the hit above genuinely came from the cache.
	if _, err := m.validateSession(context.Background(), "some-other-token"); err == nil {
		t.Fatal("uncached token should fall through to the DB and error with nil DB")
	}
}

// TestAuthCacheAPIKeyHitAvoidsDB is the API-key analogue of the session test.
func TestAuthCacheAPIKeyHitAvoidsDB(t *testing.T) {
	m := NewAuthMiddleware(nil)
	t.Cleanup(m.Stop)

	user := &models.User{ID: uuid.New(), IsActive: true}
	const rawKey = "raw-api-key-value"
	m.cache.set(crypto.HashToken(rawKey), user)

	got, err := m.validateAPIKey(context.Background(), rawKey)
	if err != nil {
		t.Fatalf("cache hit should not error with nil DB, got %v", err)
	}
	if got != user {
		t.Fatalf("expected cached user pointer, got %v", got)
	}
}

// TestAuthCacheEvictForcesDB verifies Evict removes the entry so the next lookup
// must consult the DB again.
func TestAuthCacheEvictForcesDB(t *testing.T) {
	m := NewAuthMiddleware(nil)
	t.Cleanup(m.Stop)

	user := &models.User{ID: uuid.New(), IsActive: true}
	const rawToken = "evict-me"
	hash := crypto.HashToken(rawToken)
	m.cache.set(hash, user)

	if _, err := m.validateSession(context.Background(), rawToken); err != nil {
		t.Fatalf("primed session should hit cache, got %v", err)
	}
	m.cache.Evict(hash)
	if _, err := m.validateSession(context.Background(), rawToken); err == nil {
		t.Fatal("after Evict the lookup should fall through to the DB and error")
	}
}

// TestAuthCacheEvictUser verifies a password-change-style EvictUser purges every
// cached credential for that user (both a session and an API key here) while
// leaving another user's entry intact.
func TestAuthCacheEvictUser(t *testing.T) {
	ac := NewAuthCache()
	t.Cleanup(ac.Stop)

	target := &models.User{ID: uuid.New(), IsActive: true}
	other := &models.User{ID: uuid.New(), IsActive: true}
	ac.set("session-hash", target)
	ac.set("apikey-hash", target)
	ac.set("other-hash", other)

	ac.EvictUser(target.ID)

	if _, ok := ac.get("session-hash"); ok {
		t.Error("target session entry should have been evicted")
	}
	if _, ok := ac.get("apikey-hash"); ok {
		t.Error("target api-key entry should have been evicted")
	}
	if _, ok := ac.get("other-hash"); !ok {
		t.Error("another user's entry must not be evicted")
	}
}

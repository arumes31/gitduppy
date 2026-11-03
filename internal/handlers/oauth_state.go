package handlers

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/google/uuid"
)

// OAuth flow initiation records.
//
// The state parameter alone is not enough to protect the OAuth callback: it was
// previously compared only with an unsigned cookie, and a sibling-domain origin
// can plant both the cookie and a matching state, driving a logged-in victim
// through a callback that links the ATTACKER's provider identity to the victim's
// account. Each flow therefore gets a server-side, one-time record that binds the
// random state to the provider, the initiating browser session (hash of its
// session token) and the initiating user. The callback atomically consumes the
// record and verifies that binding before it links or logs in.
//
// The store is in-memory: GitDuppy runs as a single instance, and losing pending
// records on restart only means a user has to click "sign in" again.
const (
	// oauthStateTTL bounds how long a user may take at the provider's consent
	// screen before the flow must be restarted.
	oauthStateTTL = 10 * time.Minute
	// oauthStateMaxPending caps outstanding records so unauthenticated callers
	// hammering the login endpoint cannot grow the map without bound.
	oauthStateMaxPending = 10000

	// oauthStateCookieBase is the browser-binding cookie name used over plain
	// HTTP (local dev). When the cookie is Secure the __Host- prefixed name is
	// used instead, which browsers only accept with Secure, Path=/ and no Domain
	// — so a sibling subdomain cannot plant or overwrite it.
	oauthStateCookieBase = "oauth_state"
	oauthStateCookieHost = "__Host-oauth_state"
)

// errOAuthStateStoreFull is returned when too many flows are pending.
var errOAuthStateStoreFull = errors.New("too many pending oauth flows")

// oauthStateRecord is the server-side binding for one pending OAuth flow.
type oauthStateRecord struct {
	provider string
	// sessionHash is the at-rest hash of the initiating browser's session token,
	// or "" when the flow was started without a session.
	sessionHash string
	// userID is the initiating (logged-in) user, or uuid.Nil for an anonymous
	// login flow. Only a record with a user may link an identity to an account.
	userID    uuid.UUID
	expiresAt time.Time
}

// oauthStateStore is a mutex-guarded, TTL-bounded map of pending flows keyed by
// the random state value.
type oauthStateStore struct {
	mu      sync.Mutex
	pending map[string]oauthStateRecord
	now     func() time.Time // injectable for tests
}

func newOAuthStateStore() *oauthStateStore {
	return &oauthStateStore{pending: make(map[string]oauthStateRecord), now: time.Now}
}

// issue generates a fresh random state, stores rec under it and returns it.
func (s *oauthStateStore) issue(rec oauthStateRecord) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	state := base64.RawURLEncoding.EncodeToString(buf)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if len(s.pending) >= oauthStateMaxPending {
		s.pruneLocked(now)
		if len(s.pending) >= oauthStateMaxPending {
			return "", errOAuthStateStoreFull
		}
	}
	rec.expiresAt = now.Add(oauthStateTTL)
	s.pending[state] = rec
	return state, nil
}

// consume removes and returns the record for state. It reports false when the
// state is unknown, already used, or expired, so each state is single-use.
func (s *oauthStateStore) consume(state string) (oauthStateRecord, bool) {
	if state == "" {
		return oauthStateRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.pending[state]
	if !ok {
		return oauthStateRecord{}, false
	}
	delete(s.pending, state)
	if !s.now().Before(rec.expiresAt) {
		return oauthStateRecord{}, false
	}
	return rec, true
}

// pruneLocked drops expired records. Callers hold s.mu.
func (s *oauthStateStore) pruneLocked(now time.Time) {
	for k, rec := range s.pending {
		if !now.Before(rec.expiresAt) {
			delete(s.pending, k)
		}
	}
}

// oauthStateCookie returns the state cookie name and whether it is Secure. The
// __Host- prefix requires Secure, so plain-HTTP deployments (local dev) fall
// back to the unprefixed name.
func oauthStateCookie(c *gin.Context) (name string, secure bool) {
	if cookieSecure || requestIsHTTPS(c) {
		return oauthStateCookieHost, true
	}
	return oauthStateCookieBase, false
}

// setOAuthStateCookie binds the flow to this browser. Path=/ and no Domain, as
// the __Host- prefix requires; SameSite=Lax so it is sent on the provider's
// top-level redirect back to the callback.
func setOAuthStateCookie(c *gin.Context, state string, maxAge int) {
	name, secure := oauthStateCookie(c)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(name, state, maxAge, "/", "", secure, true)
}

// takeOAuthState validates the callback's state against the browser-binding
// cookie and the server-side record for provider, consuming both. It writes a
// 400 response and returns ok=false on any mismatch.
func (h *OAuthHandler) takeOAuthState(c *gin.Context, provider string) (oauthStateRecord, bool) {
	name, _ := oauthStateCookie(c)
	cookieState, cookieErr := c.Cookie(name)
	setOAuthStateCookie(c, "", -1)

	state := c.Query("state")
	if cookieErr != nil || cookieState == "" || state == "" ||
		subtle.ConstantTimeCompare([]byte(state), []byte(cookieState)) != 1 {
		badOAuthState(c)
		return oauthStateRecord{}, false
	}
	rec, ok := h.states.consume(state)
	if !ok || rec.provider != provider {
		badOAuthState(c)
		return oauthStateRecord{}, false
	}
	return rec, true
}

func badOAuthState(c *gin.Context) {
	response.BadRequest(c, "INVALID_STATE", "Invalid, expired or mismatched OAuth state")
}

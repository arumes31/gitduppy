package middleware

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/pkg/response"
)

// CrossOriginProtection returns a middleware that rejects cross-origin,
// state-changing browser requests that carry the session cookie. It relies on
// net/http's CrossOriginProtection (Sec-Fetch-Site, falling back to comparing
// Origin with Host), so it needs no token plumbing in the frontend and also
// blocks sibling subdomains that SameSite=Lax would let through.
//
// Safe methods (GET, HEAD, OPTIONS) are always allowed. Requests without the
// session cookie are skipped: API clients authenticate with a bearer API key,
// which a cross-site page cannot attach. baseURL, when set, is added as a
// trusted origin so deployments behind a proxy that rewrites Host keep working.
func CrossOriginProtection(baseURL string) (gin.HandlerFunc, error) {
	cop := http.NewCrossOriginProtection()
	if origin := originOf(baseURL); origin != "" {
		if err := cop.AddTrustedOrigin(origin); err != nil {
			return nil, fmt.Errorf("invalid trusted origin %q: %w", origin, err)
		}
	}
	return func(c *gin.Context) {
		if cookie, err := c.Cookie("session"); err != nil || cookie == "" {
			c.Next()
			return
		}
		if err := cop.Check(c.Request); err != nil {
			response.Forbidden(c, "Cross-origin request blocked")
			c.Abort()
			return
		}
		c.Next()
	}, nil
}

// originOf reduces a base URL such as "https://git.example.com/" to its origin
// ("https://git.example.com"). It returns "" for an empty or non-HTTP value.
func originOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

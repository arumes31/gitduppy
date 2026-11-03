package handlers

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/middleware"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// wsRevalidateInterval is how often a long-lived WebSocket re-checks the
// credential it was opened with. Authentication otherwise happens only at the
// upgrade, so without this a stream would keep flowing after logout, a password
// change, API-key revocation/expiry or account deactivation.
const wsRevalidateInterval = 30 * time.Second

// wsCloseWriteTimeout bounds the policy-violation close frame write.
const wsCloseWriteTimeout = time.Second

// wsPrincipal is the identity and credential a WebSocket was authenticated with,
// captured from the gin context at upgrade time.
type wsPrincipal struct {
	db             *gorm.DB
	userID         uuid.UUID
	authType       string
	credentialHash string
}

// captureWSPrincipal records the authenticated principal and credential set by
// the auth middleware and validates it once against the database (bypassing the
// auth cache), returning the credential's expiry deadline (zero = none). It must
// run BEFORE the upgrade: on failure it writes a 401 and returns ok=false.
func captureWSPrincipal(c *gin.Context, db *gorm.DB) (*wsPrincipal, time.Time, bool) {
	user, okUser := middleware.GetCurrentUser(c)
	authType, credentialHash, okCred := middleware.GetCredential(c)
	if !okUser || !okCred {
		response.Unauthorized(c, "Authentication required")
		return nil, time.Time{}, false
	}
	p := &wsPrincipal{db: db, userID: user.ID, authType: authType, credentialHash: credentialHash}
	deadline, err := p.revalidate(c.Request.Context())
	if err != nil {
		response.Unauthorized(c, "Invalid or expired authentication credentials")
		return nil, time.Time{}, false
	}
	return p, deadline, true
}

func (p *wsPrincipal) revalidate(ctx context.Context) (time.Time, error) {
	return middleware.RevalidateCredential(ctx, p.db, p.authType, p.credentialHash, p.userID)
}

// watch re-validates the principal every wsRevalidateInterval and at the
// credential's expiry deadline until ctx is done. When the credential is no
// longer valid it sends a policy-violation (1008) close frame and calls cancel so
// the stream loop exits and the socket is closed. Run it in its own goroutine.
func (p *wsPrincipal) watch(ctx context.Context, cancel context.CancelFunc, ws *websocket.Conn, deadline time.Time) {
	for {
		wait := wsRevalidateInterval
		if !deadline.IsZero() {
			// Wake at the expiry deadline (plus a hair so "expiry > now" is false
			// by then) if it comes before the next periodic check.
			if d := time.Until(deadline) + 10*time.Millisecond; d < wait {
				wait = d
			}
		}
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		next, err := p.revalidate(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			zap.L().Named("ws").Info("closing websocket: credential no longer valid",
				zap.String("user_id", p.userID.String()), zap.String("auth_type", p.authType))
			// WriteControl is safe to call concurrently with the stream's writes.
			_ = ws.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication expired or revoked"),
				time.Now().Add(wsCloseWriteTimeout))
			cancel()
			return
		}
		deadline = next // a refreshed session pushes the deadline out
	}
}

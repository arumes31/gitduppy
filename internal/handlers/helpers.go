package handlers

import (
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/gitduppy/gitduppy/pkg/validator"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// genericServerErrorMessage is the client-facing message for any unexpected
// server-side failure. The real error is logged server-side (see logServerError)
// so the client never sees paths, SQL, git stderr, or other internals.
const genericServerErrorMessage = "An internal error occurred"

// Pagination defaults and the hard upper bound applied to page-size / limit
// query params so a request cannot ask for an unbounded result set.
const (
	defaultPerPage = 20
	maxPerPage     = 100
)

// parseUUIDParam parses a UUID path parameter, writing a 400 response and
// returning ok=false when it is malformed. It centralizes the parse-and-400
// dance that every :id / :tagId / :branchId handler otherwise repeats.
func parseUUIDParam(c *gin.Context, param, resource string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid "+resource+" ID format")
		return uuid.Nil, false
	}
	return id, true
}

// respondServiceError maps a service-layer error to an HTTP response. Typed
// sentinel errors (services.ErrNotFound / ErrConflict / ErrValidation /
// ErrForbidden) are translated to their status codes with the error's
// human-readable — and deliberately non-sensitive — detail preserved. Any other
// error is treated as an unexpected internal failure: it is logged in full
// server-side and the client receives only a generic 500 so paths, SQL, and git
// internals never leak through the API.
func respondServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, services.ErrNotFound):
		response.NotFound(c, err.Error())
	case errors.Is(err, services.ErrConflict):
		response.Conflict(c, err.Error())
	case errors.Is(err, services.ErrValidation):
		response.BadRequest(c, "VALIDATION_ERROR", err.Error())
	case errors.Is(err, services.ErrForbidden):
		response.Forbidden(c, err.Error())
	case errors.Is(err, services.ErrNotImplemented):
		response.ErrorResponse(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", err.Error())
	default:
		logServerError(c, err)
		response.InternalError(c, genericServerErrorMessage)
	}
}

// logServerError records the full error server-side, tagged with the request
// method and path, so operators can diagnose failures that clients only ever see
// as a generic 500. The path is sanitised to prevent log injection from
// user-controlled input. It uses the zap global logger, matching the structured
// logging used elsewhere (internal/gitops).
func logServerError(c *gin.Context, err error) {
	// Sanitise the request path: clean traversal sequences, strip control
	// characters (including newlines) so an attacker cannot forge log entries.
	safePath := sanitiseLogValue(c.Request.URL.Path)
	zap.L().Named("handlers").Error("request failed",
		zap.String("method", c.Request.Method),
		zap.String("path", safePath),
		zap.Error(err),
	)
}

// parseLimitParam reads a "limit"-style query param (named by param), applying
// def for missing/non-numeric input and clamping the result to [1, maxLimit].
func parseLimitParam(c *gin.Context, param string, def, maxLimit int) int {
	return clampLimit(c.Query(param), def, maxLimit)
}

// clampLimit parses raw (falling back to def when missing/non-numeric) and
// clamps it into [1, maxLimit]. A parsed value below 1 falls back to def rather
// than to 1 so an explicit "0"/negative behaves like "unset".
func clampLimit(raw string, def, maxLimit int) int {
	v := validator.ParseInt(raw, def)
	if v < 1 {
		v = def
	}
	if v > maxLimit {
		v = maxLimit
	}
	return v
}

// sanitiseLogValue cleans a user-controlled string before it is written to a
// log entry. It applies path.Clean to normalise traversal sequences and strips
// control characters (newlines, carriage returns, tabs, etc.) so an attacker
// cannot forge extra log lines or inject terminal escape sequences.
func sanitiseLogValue(raw string) string {
	cleaned := path.Clean(raw)
	var b strings.Builder
	b.Grow(len(cleaned))
	for _, r := range cleaned {
		if r >= 0x20 && r != 0x7f {
			b.WriteRune(r)
		}
	}
	return b.String()
}

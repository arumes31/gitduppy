package middleware

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // SHA-1 here is a cache validator (ETag), not a security primitive
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// etagResponseWriter buffers the handler's response body so a content hash can be
// computed after the handler runs. Only Write/WriteString are intercepted; status
// and header writes fall through to the wrapped gin writer's recorded state (which
// is not flushed to the client until we choose to flush the buffered body).
type etagResponseWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w *etagResponseWriter) Write(b []byte) (int, error)       { return w.body.Write(b) }
func (w *etagResponseWriter) WriteString(s string) (int, error) { return w.body.WriteString(s) }

// ETag makes a GET endpoint answer conditional requests. It buffers the response,
// derives a weak ETag from a SHA-1 of the marshaled body, and:
//   - on a matching If-None-Match, replies 304 Not Modified with an empty body
//     (the ETag header is preserved);
//   - otherwise sets the ETag header and streams the buffered body unchanged.
//
// It is intended to be applied to a small number of hot, pollable read endpoints
// (the dashboard overview and the repository list), NOT blanket-applied. Non-GET
// requests and non-200 responses pass straight through.
//
// Interaction with gzip: the routes this is applied to are excluded from the gzip
// middleware. That keeps the ETag over the raw marshaled JSON (a stable validator
// independent of transfer encoding) and, more importantly, avoids the 304 path
// being wrapped by the gzip writer — which flushes a stray gzip footer for an
// empty body once WriteHeaderNow marks the response as written.
func ETag() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodGet {
			c.Next()
			return
		}

		orig := c.Writer
		buf := &bytes.Buffer{}
		c.Writer = &etagResponseWriter{ResponseWriter: orig, body: buf}
		c.Next()
		c.Writer = orig

		body := buf.Bytes()
		// Only validate successful, non-empty bodies. Anything else (errors, empty
		// responses, streamed writes) is emitted as the handler produced it.
		if orig.Status() != http.StatusOK || len(body) == 0 {
			if len(body) > 0 {
				_, _ = orig.Write(body)
			} else {
				orig.WriteHeaderNow()
			}
			return
		}

		sum := sha1.Sum(body) //nolint:gosec // cache validator, not security
		etag := `W/"` + hex.EncodeToString(sum[:]) + `"`
		orig.Header().Set("ETag", etag)

		if ifNoneMatchContains(c.GetHeader("If-None-Match"), etag) {
			orig.Header().Del("Content-Length")
			orig.WriteHeader(http.StatusNotModified)
			orig.WriteHeaderNow()
			return
		}
		_, _ = orig.Write(body)
	}
}

// ifNoneMatchContains reports whether an If-None-Match header value matches the
// given ETag. It honors "*" and a comma-separated list, and compares weakly
// (ignoring the W/ prefix) as required for If-None-Match by RFC 7232.
func ifNoneMatchContains(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	target := strings.TrimPrefix(etag, "W/")
	for _, part := range strings.Split(header, ",") {
		p := strings.TrimSpace(part)
		if p == etag || strings.TrimPrefix(p, "W/") == target {
			return true
		}
	}
	return false
}

package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

func ctx() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	return c, w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) Response {
	t.Helper()
	var r Response
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, w.Body.String())
	}
	return r
}

func TestSuccessFamily(t *testing.T) {
	c, w := ctx()
	Success(c, gin.H{"k": "v"})
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d", w.Code)
	}
	if r := decode(t, w); !r.Success {
		t.Error("expected success true")
	}

	c, w = ctx()
	SuccessWithMessage(c, "done", nil)
	if r := decode(t, w); !r.Success || r.Message != "done" {
		t.Errorf("unexpected: %+v", r)
	}

	c, w = ctx()
	SuccessWithMeta(c, []int{1}, &Meta{Page: 1, Total: 1})
	if r := decode(t, w); r.Meta == nil || r.Meta.Page != 1 {
		t.Errorf("meta missing: %+v", r)
	}

	c, w = ctx()
	Created(c, nil)
	if w.Code != http.StatusCreated {
		t.Errorf("created code=%d", w.Code)
	}

	c, w = ctx()
	Accepted(c, nil)
	if w.Code != http.StatusAccepted {
		t.Errorf("accepted code=%d", w.Code)
	}
}

func TestErrorFamily(t *testing.T) {
	tests := []struct {
		name string
		fn   func(*gin.Context)
		code int
	}{
		{"badrequest", func(c *gin.Context) { BadRequest(c, "X", "bad") }, http.StatusBadRequest},
		{"notfound", func(c *gin.Context) { NotFound(c, "") }, http.StatusNotFound},
		{"unauthorized", func(c *gin.Context) { Unauthorized(c, "") }, http.StatusUnauthorized},
		{"forbidden", func(c *gin.Context) { Forbidden(c, "") }, http.StatusForbidden},
		{"internal", func(c *gin.Context) { InternalError(c, "") }, http.StatusInternalServerError},
		{"conflict", func(c *gin.Context) { Conflict(c, "dupe") }, http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := ctx()
			tt.fn(c)
			if w.Code != tt.code {
				t.Fatalf("code=%d want %d", w.Code, tt.code)
			}
			r := decode(t, w)
			if r.Success {
				t.Error("error response should have success=false")
			}
			if len(r.Errors) == 0 {
				t.Error("expected at least one error")
			}
		})
	}
}

func TestErrorWithFieldAndMultiple(t *testing.T) {
	c, w := ctx()
	ErrorWithField(c, http.StatusBadRequest, "V", "msg", "email")
	r := decode(t, w)
	if len(r.Errors) != 1 || r.Errors[0].Field != "email" {
		t.Errorf("field error wrong: %+v", r.Errors)
	}

	c, w = ctx()
	ErrorWithMultipleErrors(c, http.StatusBadRequest, []Error{{Code: "A"}, {Code: "B"}})
	r = decode(t, w)
	if len(r.Errors) != 2 {
		t.Errorf("expected 2 errors, got %d", len(r.Errors))
	}
}

func TestDefaultMessages(t *testing.T) {
	c, w := ctx()
	NotFound(c, "")
	if decode(t, w).Errors[0].Message != "Resource not found" {
		t.Error("default not-found message missing")
	}
}

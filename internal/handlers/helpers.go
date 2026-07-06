package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/google/uuid"
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

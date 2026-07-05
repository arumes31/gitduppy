package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Response represents the standard API response envelope.
type Response struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Errors  []Error     `json:"errors,omitempty"`
	Meta    *Meta       `json:"meta,omitempty"`
}

// Error represents an API error.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

// Meta represents pagination metadata.
type Meta struct {
	Page       int `json:"page,omitempty"`
	PerPage    int `json:"per_page,omitempty"`
	Total      int `json:"total,omitempty"`
	TotalPages int `json:"total_pages,omitempty"`
}

// Success sends a success response.
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    data,
	})
}

// SuccessWithMessage sends a success response with a message.
func SuccessWithMessage(c *gin.Context, message string, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Success: true,
		Message: message,
		Data:    data,
	})
}

// SuccessWithMeta sends a success response with pagination metadata.
func SuccessWithMeta(c *gin.Context, data interface{}, meta *Meta) {
	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    data,
		Meta:    meta,
	})
}

// Created sends a 201 Created response.
func Created(c *gin.Context, data interface{}) {
	c.JSON(http.StatusCreated, Response{
		Success: true,
		Data:    data,
	})
}

// Accepted sends a 202 Accepted response.
func Accepted(c *gin.Context, data interface{}) {
	c.JSON(http.StatusAccepted, Response{
		Success: true,
		Data:    data,
	})
}

// ErrorResponse sends an error response.
func ErrorResponse(c *gin.Context, statusCode int, code string, message string) {
	c.JSON(statusCode, Response{
		Success: false,
		Errors: []Error{
			{Code: code, Message: message},
		},
	})
}

// ErrorWithField sends an error response with a field reference.
func ErrorWithField(c *gin.Context, statusCode int, code string, message string, field string) {
	c.JSON(statusCode, Response{
		Success: false,
		Errors: []Error{
			{Code: code, Message: message, Field: field},
		},
	})
}

// ErrorWithMultipleErrors sends an error response with multiple errors.
func ErrorWithMultipleErrors(c *gin.Context, statusCode int, errors []Error) {
	c.JSON(statusCode, Response{
		Success: false,
		Errors:  errors,
	})
}

// NotFound sends a 404 Not Found response.
func NotFound(c *gin.Context, message string) {
	if message == "" {
		message = "Resource not found"
	}
	ErrorResponse(c, http.StatusNotFound, "NOT_FOUND", message)
}

// BadRequest sends a 400 Bad Request response.
func BadRequest(c *gin.Context, code string, message string) {
	ErrorResponse(c, http.StatusBadRequest, code, message)
}

// Unauthorized sends a 401 Unauthorized response.
func Unauthorized(c *gin.Context, message string) {
	if message == "" {
		message = "Unauthorized"
	}
	ErrorResponse(c, http.StatusUnauthorized, "UNAUTHORIZED", message)
}

// Forbidden sends a 403 Forbidden response.
func Forbidden(c *gin.Context, message string) {
	if message == "" {
		message = "Forbidden"
	}
	ErrorResponse(c, http.StatusForbidden, "FORBIDDEN", message)
}

// InternalError sends a 500 Internal Server Error response.
func InternalError(c *gin.Context, message string) {
	if message == "" {
		message = "Internal server error"
	}
	ErrorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", message)
}

// Conflict sends a 409 Conflict response.
func Conflict(c *gin.Context, message string) {
	ErrorResponse(c, http.StatusConflict, "CONFLICT", message)
}

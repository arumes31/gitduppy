package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/middleware"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/response"
	"github.com/gitduppy/gitduppy/pkg/validator"
	"github.com/google/uuid"
)

// UserHandler handles user management requests.
type UserHandler struct {
	userService *services.UserService
}

// NewUserHandler creates a new user handler.
func NewUserHandler(userService *services.UserService) *UserHandler {
	return &UserHandler{
		userService: userService,
	}
}

// ListUsers handles GET /api/v1/users.
func (h *UserHandler) ListUsers(c *gin.Context) {
	filter := &services.UserFilter{
		Page:    1,
		PerPage: 20,
	}

	if page := c.Query("page"); page != "" {
		filter.Page = validator.ParseInt(page, 1)
	}
	if perPage := c.Query("per_page"); perPage != "" {
		filter.PerPage = validator.ParseInt(perPage, 20)
	}

	role := c.Query("role")
	if role != "" {
		filter.Role = &role
	}

	isActive := c.Query("is_active")
	if isActive != "" {
		active := isActive == "true"
		filter.IsActive = &active
	}

	search := c.Query("search")
	if search != "" {
		filter.Search = search
	}

	users, total, err := h.userService.ListUsers(c, filter)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.SuccessWithMeta(c, users, &response.Meta{
		Page:       filter.Page,
		PerPage:    filter.PerPage,
		Total:      int(total),
		TotalPages: int(total/int64(filter.PerPage)) + 1,
	})
}

// GetUser handles GET /api/v1/users/:id.
func (h *UserHandler) GetUser(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid user ID format")
		return
	}

	user, err := h.userService.GetUserByID(c, id)
	if err != nil {
		response.NotFound(c, "User not found")
		return
	}

	response.Success(c, user)
}

// CreateUser handles POST /api/v1/users.
func (h *UserHandler) CreateUser(c *gin.Context) {
	var req services.CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_REQUEST", err.Error())
		return
	}

	if err := validator.ValidateStruct(&req); err != nil {
		response.BadRequest(c, "VALIDATION_ERROR", err.Error())
		return
	}

	user, err := h.userService.CreateUser(c, &req)
	if err != nil {
		response.BadRequest(c, "CREATE_ERROR", err.Error())
		return
	}

	response.Created(c, user)
}

// UpdateUser handles PUT /api/v1/users/:id.
func (h *UserHandler) UpdateUser(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid user ID format")
		return
	}

	var req services.UpdateUserRequest
	if bindErr := c.ShouldBindJSON(&req); bindErr != nil {
		response.BadRequest(c, "INVALID_REQUEST", bindErr.Error())
		return
	}

	user, err := h.userService.UpdateUser(c, id, &req)
	if err != nil {
		response.BadRequest(c, "UPDATE_ERROR", err.Error())
		return
	}

	response.Success(c, user)
}

// DeleteUser handles DELETE /api/v1/users/:id.
func (h *UserHandler) DeleteUser(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid user ID format")
		return
	}

	if err := h.userService.DeleteUser(c, id); err != nil {
		response.BadRequest(c, "DELETE_ERROR", err.Error())
		return
	}

	response.SuccessWithMessage(c, "User deleted successfully", nil)
}

// SetUserStatus handles PATCH /api/v1/users/:id/status.
func (h *UserHandler) SetUserStatus(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "INVALID_ID", "Invalid user ID format")
		return
	}

	var req struct {
		IsActive bool `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "INVALID_REQUEST", err.Error())
		return
	}

	if err := h.userService.SetUserStatus(c, id, req.IsActive); err != nil {
		response.BadRequest(c, "UPDATE_ERROR", err.Error())
		return
	}

	response.SuccessWithMessage(c, "User status updated", nil)
}

// GetCurrentUser handles GET /api/v1/users/me.
func (h *UserHandler) GetCurrentUser(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok {
		response.Unauthorized(c, "Not authenticated")
		return
	}

	response.Success(c, user)
}

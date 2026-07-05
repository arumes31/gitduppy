package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/middleware"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/response"
)

const formatJSON = "json"

// BackupHandler handles backup and export requests.
type BackupHandler struct {
	backupService *services.BackupService
}

// NewBackupHandler creates a new backup handler.
func NewBackupHandler(backupService *services.BackupService) *BackupHandler {
	return &BackupHandler{
		backupService: backupService,
	}
}

// Export handles GET /api/v1/backup/export.
func (h *BackupHandler) Export(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok || !user.IsAdmin() {
		response.Unauthorized(c, "Admin access required")
		return
	}

	format := c.Query("format")
	if format == "" {
		format = formatJSON
	}

	var exportFormat services.ExportFormat
	switch format {
	case formatJSON:
		exportFormat = services.JSONFormat
	case "yaml", "yml":
		exportFormat = services.YAMLFormat
	default:
		response.BadRequest(c, "INVALID_FORMAT", "Supported formats: json, yaml")
		return
	}

	data, err := h.backupService.ExportData(c, exportFormat)
	if err != nil {
		response.InternalError(c, "Failed to export data: "+err.Error())
		return
	}

	filename := "gitduppy_export"
	if exportFormat == services.JSONFormat {
		filename += ".json"
		c.Header("Content-Type", "application/json")
	} else {
		filename += ".yaml"
		c.Header("Content-Type", "application/yaml")
	}
	c.Header("Content-Disposition", "attachment; filename="+filename)

	c.Data(http.StatusOK, "application/octet-stream", data)
}

// Import handles POST /api/v1/backup/import.
func (h *BackupHandler) Import(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok || !user.IsAdmin() {
		response.Unauthorized(c, "Admin access required")
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		response.BadRequest(c, "NO_FILE", "No file uploaded")
		return
	}

	// Determine format from file extension
	var importFormat services.ExportFormat
	filename := file.Filename
	switch {
	case len(filename) > 4 && filename[len(filename)-5:] == ".json":
		importFormat = services.JSONFormat
	case len(filename) > 4 && filename[len(filename)-5:] == ".yaml":
		importFormat = services.YAMLFormat
	case len(filename) > 3 && filename[len(filename)-4:] == ".yml":
		importFormat = services.YAMLFormat
	default:
		response.BadRequest(c, "INVALID_FORMAT", "File must be .json or .yaml/.yml")
		return
	}

	// Read file content
	data, err := file.Open()
	if err != nil {
		response.BadRequest(c, "FILE_ERROR", "Failed to open file")
		return
	}
	defer data.Close()

	content := make([]byte, file.Size)
	_, err = data.Read(content)
	if err != nil {
		response.BadRequest(c, "FILE_ERROR", "Failed to read file")
		return
	}

	if err := h.backupService.ImportData(c, content, importFormat); err != nil {
		response.BadRequest(c, "IMPORT_ERROR", "Failed to import data: "+err.Error())
		return
	}

	response.SuccessWithMessage(c, "Data imported successfully", nil)
}

// DatabaseBackup handles POST /api/v1/backup/database.
func (h *BackupHandler) DatabaseBackup(c *gin.Context) {
	user, ok := middleware.GetCurrentUser(c)
	if !ok || !user.IsAdmin() {
		response.Unauthorized(c, "Admin access required")
		return
	}

	backupPath, err := h.backupService.DatabaseBackup(c)
	if err != nil {
		response.InternalError(c, "Failed to create database backup: "+err.Error())
		return
	}

	response.Success(c, gin.H{
		"backup_path": backupPath,
		"message":     "Database backup created successfully",
	})
}

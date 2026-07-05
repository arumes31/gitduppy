package services

import (
	"context"
	"errors"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// JobEnqueuer hands a created clone job to the background worker pool. It is
// satisfied by *gitops.CloneWorker; declared here as an interface to avoid an
// import cycle.
type JobEnqueuer interface {
	Enqueue(job *models.CloneJob)
}

// CloneService handles clone job management.
type CloneService struct {
	db       *gorm.DB
	enqueuer JobEnqueuer
}

// NewCloneService creates a new clone service.
func NewCloneService() *CloneService {
	return &CloneService{
		db: database.GetDB(),
	}
}

// SetEnqueuer wires the worker pool so newly created jobs are dispatched
// immediately instead of waiting for the periodic scheduler tick.
func (s *CloneService) SetEnqueuer(e JobEnqueuer) {
	s.enqueuer = e
}

// CloneFilter represents filters for listing clone jobs.
type CloneFilter struct {
	RepositoryID *uuid.UUID
	Status       *string
	TriggerType  *string
	Page         int
	PerPage      int
}

// ListCloneJobs returns a paginated list of clone jobs.
func (s *CloneService) ListCloneJobs(_ context.Context, filter *CloneFilter) ([]models.CloneJob, int64, error) {
	if filter == nil {
		filter = &CloneFilter{Page: 1, PerPage: 20}
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PerPage < 1 {
		filter.PerPage = 20
	}

	query := s.db.Model(&models.CloneJob{}).Preload("Repository")

	// Apply filters
	if filter.RepositoryID != nil {
		query = query.Where("repository_id = ?", *filter.RepositoryID)
	}
	if filter.Status != nil && *filter.Status != "" {
		query = query.Where("status = ?", *filter.Status)
	}
	if filter.TriggerType != nil && *filter.TriggerType != "" {
		query = query.Where("trigger_type = ?", *filter.TriggerType)
	}

	// Get total count
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Get paginated results
	offset := (filter.Page - 1) * filter.PerPage
	var jobs []models.CloneJob
	err := query.Offset(offset).Limit(filter.PerPage).Order("created_at DESC").Find(&jobs).Error
	return jobs, total, err
}

// GetCloneJobByID retrieves a clone job by ID.
func (s *CloneService) GetCloneJobByID(_ context.Context, id uuid.UUID) (*models.CloneJob, error) {
	var job models.CloneJob
	if err := s.db.Preload("Repository").Preload("CloneLogs").First(&job, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("clone job not found")
		}
		return nil, err
	}
	return &job, nil
}

// CreateCloneJob creates a new clone job.
func (s *CloneService) CreateCloneJob(_ context.Context, repoID uuid.UUID, triggerType string) (*models.CloneJob, error) {
	// Verify repository exists
	var repo models.Repository
	if err := s.db.First(&repo, repoID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("repository not found")
		}
		return nil, err
	}

	job := &models.CloneJob{
		ID:           uuid.New(),
		RepositoryID: repoID,
		TriggerType:  triggerType,
		Status:       "pending",
	}

	if err := s.db.Create(job).Error; err != nil {
		return nil, err
	}

	// Dispatch to the worker pool immediately so a manual/API trigger runs now
	// rather than waiting for the next scheduler tick.
	if s.enqueuer != nil {
		s.enqueuer.Enqueue(job)
	}

	return job, nil
}

// UpdateCloneJobStatus updates the status of a clone job.
func (s *CloneService) UpdateCloneJobStatus(_ context.Context, id uuid.UUID, status string, outputLog string, exitCode *int) error {
	updates := map[string]interface{}{
		"status": status,
	}
	if outputLog != "" {
		updates["output_log"] = outputLog
	}
	if exitCode != nil {
		updates["exit_code"] = *exitCode
	}

	if status == "running" {
		now := time.Now()
		updates["started_at"] = now
	}
	if status == "success" || status == "failed" || status == "cancelled" {
		now := time.Now()
		updates["completed_at"] = now
	}

	return s.db.Model(&models.CloneJob{}).Where("id = ?", id).Updates(updates).Error
}

// UpdateCloneJobProgress updates the progress of a clone job.
func (s *CloneService) UpdateCloneJobProgress(_ context.Context, id uuid.UUID, percent int, message string) error {
	return s.db.Model(&models.CloneJob{}).Where("id = ?", id).Updates(map[string]interface{}{
		"progress_percent": percent,
		"output_log":       message,
	}).Error
}

// AddCloneLog adds a log entry to a clone job.
func (s *CloneService) AddCloneLog(_ context.Context, jobID uuid.UUID, level, message string) error {
	log := &models.CloneLog{
		ID:         uuid.New(),
		CloneJobID: jobID,
		LogLevel:   level,
		Message:    message,
	}
	return s.db.Create(log).Error
}

// GetCloneLogs retrieves logs for a clone job.
func (s *CloneService) GetCloneLogs(_ context.Context, jobID uuid.UUID) ([]models.CloneLog, error) {
	var logs []models.CloneLog
	err := s.db.Where("clone_job_id = ?", jobID).Order("created_at ASC").Find(&logs).Error
	return logs, err
}

// GetRepositoryLogs retrieves all clone logs for a repository.
func (s *CloneService) GetRepositoryLogs(_ context.Context, repoID uuid.UUID, limit int) ([]models.CloneLog, error) {
	if limit <= 0 {
		limit = 100
	}

	var logs []models.CloneLog
	err := s.db.
		Joins("JOIN clone_jobs ON clone_jobs.id = clone_logs.clone_job_id").
		Where("clone_jobs.repository_id = ?", repoID).
		Order("clone_logs.created_at DESC").
		Limit(limit).
		Find(&logs).Error
	return logs, err
}

// CancelCloneJob cancels a running clone job.
func (s *CloneService) CancelCloneJob(_ context.Context, id uuid.UUID) error {
	var job models.CloneJob
	if err := s.db.First(&job, id).Error; err != nil {
		return err
	}

	if job.Status != "running" && job.Status != "pending" {
		return errors.New("can only cancel running or pending jobs")
	}

	return s.db.Model(&job).Updates(map[string]interface{}{
		"status":       "cancelled",
		"completed_at": time.Now(),
	}).Error
}

// GetRecentJobs retrieves recent clone jobs.
func (s *CloneService) GetRecentJobs(_ context.Context, limit int) ([]models.CloneJob, error) {
	if limit <= 0 {
		limit = 10
	}

	var jobs []models.CloneJob
	err := s.db.Preload("Repository").Order("created_at DESC").Limit(limit).Find(&jobs).Error
	return jobs, err
}

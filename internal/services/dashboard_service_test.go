package services

import (
	"context"
	"testing"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/google/uuid"
)

// newTestRepo inserts a minimal active repository row and returns its ID.
// Callers that need a specific LastCloneAt/CloneIntervalMinutes pass mutators.
func newTestRepo(t *testing.T, userID uuid.UUID, mutate func(*models.Repository)) uuid.UUID {
	t.Helper()
	repo := &models.Repository{
		ID:                   uuid.New(),
		Name:                 "dash-test-" + uuid.NewString()[:8],
		URL:                  "https://github.com/octocat/Hello-World.git",
		AuthType:             "none",
		StoragePath:          "/srv/data/repos/" + uuid.NewString(),
		IsActive:             true,
		CloneIntervalMinutes: 60,
		CreatedBy:            &userID,
	}
	if mutate != nil {
		mutate(repo)
	}
	if err := database.GetDB().Create(repo).Error; err != nil {
		t.Fatalf("create test repo: %v", err)
	}
	t.Cleanup(func() { database.GetDB().Unscoped().Delete(repo) })
	return repo.ID
}

func newTestCloneJob(t *testing.T, repoID uuid.UUID, mutate func(*models.CloneJob)) uuid.UUID {
	t.Helper()
	job := &models.CloneJob{
		ID:           uuid.New(),
		RepositoryID: repoID,
		TriggerType:  "manual",
		Status:       "pending",
	}
	if mutate != nil {
		mutate(job)
	}
	if err := database.GetDB().Create(job).Error; err != nil {
		t.Fatalf("create test clone job: %v", err)
	}
	t.Cleanup(func() { database.GetDB().Unscoped().Delete(job) })
	return job.ID
}

func TestGetRunningJobCount(t *testing.T) {
	testDBAvailable(t)
	ctx := context.Background()
	userID := createTestUser(t)
	repoID := newTestRepo(t, userID, nil)

	before, err := (&DashboardService{db: database.GetDB()}).GetRunningJobCount(ctx)
	if err != nil {
		t.Fatalf("GetRunningJobCount (before): %v", err)
	}

	newTestCloneJob(t, repoID, func(j *models.CloneJob) { j.Status = "running" })
	newTestCloneJob(t, repoID, func(j *models.CloneJob) { j.Status = "pending" }) // must not count

	svc := &DashboardService{db: database.GetDB()}
	after, err := svc.GetRunningJobCount(ctx)
	if err != nil {
		t.Fatalf("GetRunningJobCount (after): %v", err)
	}
	if after != before+1 {
		t.Errorf("GetRunningJobCount = %d, want %d (before %d + 1 running job)", after, before+1, before)
	}
}

func TestGetNextSyncs(t *testing.T) {
	testDBAvailable(t)
	ctx := context.Background()
	userID := createTestUser(t)

	// Never cloned: sorts as already due (earliest).
	neverClonedID := newTestRepo(t, userID, func(r *models.Repository) {
		r.CloneIntervalMinutes = 60
	})
	// Cloned an hour ago with a 24h interval: due much later.
	farFuture := time.Now().Add(-1 * time.Hour)
	farFutureID := newTestRepo(t, userID, func(r *models.Repository) {
		r.LastCloneAt = &farFuture
		r.CloneIntervalMinutes = 24 * 60
	})
	// Inactive: must be excluded entirely.
	newTestRepo(t, userID, func(r *models.Repository) { r.IsActive = false })

	svc := &DashboardService{db: database.GetDB()}
	syncs, err := svc.GetNextSyncs(ctx, 50)
	if err != nil {
		t.Fatalf("GetNextSyncs: %v", err)
	}

	byID := make(map[uuid.UUID]NextSync, len(syncs))
	for _, s := range syncs {
		byID[s.RepositoryID] = s
	}

	never, ok := byID[neverClonedID]
	if !ok {
		t.Fatalf("never-cloned repo missing from next_syncs")
	}
	future, ok := byID[farFutureID]
	if !ok {
		t.Fatalf("far-future repo missing from next_syncs")
	}
	if !never.NextRunAt.Before(future.NextRunAt) {
		t.Errorf("never-cloned repo (next_run_at=%v) should sort before the far-future repo (next_run_at=%v)",
			never.NextRunAt, future.NextRunAt)
	}
}

func TestGetRecentFailures(t *testing.T) {
	testDBAvailable(t)
	ctx := context.Background()
	userID := createTestUser(t)
	repoID := newTestRepo(t, userID, nil)

	completed := time.Now()
	failedID := newTestCloneJob(t, repoID, func(j *models.CloneJob) {
		j.Status = "failed"
		j.CompletedAt = &completed
		j.OutputLog = "boom"
	})
	newTestCloneJob(t, repoID, func(j *models.CloneJob) { j.Status = "success" }) // must not appear

	svc := &DashboardService{db: database.GetDB()}
	failures, err := svc.GetRecentFailures(ctx, 50)
	if err != nil {
		t.Fatalf("GetRecentFailures: %v", err)
	}

	var found bool
	for _, f := range failures {
		if f.Status != "failed" {
			t.Errorf("GetRecentFailures returned a non-failed job: %+v", f)
		}
		if f.ID == failedID {
			found = true
			if f.Repository == nil || f.Repository.ID != repoID {
				t.Errorf("expected failure %s to preload its repository", failedID)
			}
		}
	}
	if !found {
		t.Errorf("expected failure job %s in GetRecentFailures result", failedID)
	}
}

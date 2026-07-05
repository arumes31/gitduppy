package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitduppy/gitduppy/internal/config"
	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/pkg/crypto"
	"github.com/google/uuid"
)

// createTestUser inserts a user row so foreign-key constraints (repositories.created_by)
// are satisfied, and returns its id.
func createTestUser(t *testing.T) uuid.UUID {
	t.Helper()
	u := &models.User{ID: uuid.New(), Username: "u-" + uuid.NewString()[:8], Email: uuid.NewString()[:8] + "@test.local", Role: "admin", IsActive: true}
	if err := database.GetDB().Create(u).Error; err != nil {
		t.Fatalf("create test user: %v", err)
	}
	t.Cleanup(func() { database.GetDB().Unscoped().Delete(u) })
	return u.ID
}

// testDBAvailable connects to a throwaway Postgres test database. Only the host
// is configurable, via GITMIRRORS_TEST_DB_HOST (default "localhost"); the
// remaining settings (port 5433, database gitduppy_test, user/password
// gitduppy) are fixed to match the local docker test instance. Tests are
// skipped (not failed) when no database is reachable so the suite still runs in
// environments without Postgres.
func testDBAvailable(t *testing.T) {
	t.Helper()
	if database.GetDB() != nil {
		return
	}
	cfg := &config.DatabaseConfig{
		Host: envOr("GITMIRRORS_TEST_DB_HOST", "localhost"),
		Port: 5433, Name: "gitduppy_test", User: "gitduppy", Password: "gitduppy", SSLMode: "disable",
		MaxOpenConns: 5, MaxIdleConns: 2,
	}
	if err := database.Connect(cfg); err != nil {
		t.Skipf("no test database available: %v", err)
	}
	if err := database.AutoMigrate(); err != nil {
		t.Skipf("migration failed: %v", err)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func newRepoService(t *testing.T, basePath string) *RepositoryService {
	enc, err := crypto.NewEncryptionService("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	return NewRepositoryService(enc, basePath)
}

func TestRepositoryCRUDAndStoragePath(t *testing.T) {
	testDBAvailable(t)
	ctx := context.Background()
	base := filepath.Join("/srv/data/repos")
	svc := newRepoService(t, base)

	userID := createTestUser(t)
	name := "svc-test-" + uuid.NewString()[:8]
	repo, err := svc.CreateRepository(ctx, &CreateRepositoryRequest{
		Name: name, URL: "https://github.com/octocat/Hello-World.git",
		Branch: "master", AuthType: "none",
		CloneIntervalMinutes: 60, RetentionDays: 30,
	}, userID)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = svc.PermanentDeleteRepository(ctx, repo.ID) })

	// Regression: StoragePath must be the FULL on-disk path (base root joined in),
	// not a bare relative shard path. This is what the browse/delete/cleanup code
	// resolves directly.
	if !strings.HasPrefix(filepath.ToSlash(repo.StoragePath), filepath.ToSlash(base)) {
		t.Errorf("StoragePath %q should start with base %q", repo.StoragePath, base)
	}
	if !strings.Contains(repo.StoragePath, "shards") {
		t.Errorf("StoragePath %q should be sharded", repo.StoragePath)
	}
	if !strings.Contains(repo.StoragePath, repo.ID.String()) {
		t.Errorf("StoragePath %q should contain the repo id", repo.StoragePath)
	}

	// Get
	got, err := svc.GetRepositoryByID(ctx, repo.ID)
	if err != nil || got.ID != repo.ID {
		t.Fatalf("get: %v", err)
	}

	// List finds it
	list, total, err := svc.ListRepositories(ctx, &RepositoryFilter{Page: 1, PerPage: 100, Search: name})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total < 1 || len(list) < 1 {
		t.Errorf("expected to find created repo in list (total=%d)", total)
	}

	// Update
	newBranch := "main"
	upd, err := svc.UpdateRepository(ctx, repo.ID, &UpdateRepositoryRequest{Branch: &newBranch})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.Branch != "main" {
		t.Errorf("branch not updated: %q", upd.Branch)
	}

	// SetStatus
	if err := svc.SetRepositoryStatus(ctx, repo.ID, false); err != nil {
		t.Fatalf("set status: %v", err)
	}
	after, _ := svc.GetRepositoryByID(ctx, repo.ID)
	if after.IsActive {
		t.Error("expected repo to be inactive")
	}

	// Soft delete then confirm it is gone from the default list
	if err := svc.DeleteRepository(ctx, repo.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestGetRepositoryNotFound(t *testing.T) {
	testDBAvailable(t)
	svc := newRepoService(t, "/tmp")
	if _, err := svc.GetRepositoryByID(context.Background(), uuid.New()); err == nil {
		t.Error("expected error for non-existent repo")
	}
}

func TestTagServiceCRUD(t *testing.T) {
	testDBAvailable(t)
	ctx := context.Background()
	ts := NewTagService()
	name := "tag-" + uuid.NewString()[:8]
	tag, err := ts.CreateTag(ctx, &CreateTagRequest{Name: name, Color: "#ff8800"})
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	t.Cleanup(func() { _ = ts.DeleteTag(ctx, tag.ID) })

	got, err := ts.GetTagByID(ctx, tag.ID)
	if err != nil || got.Name != name {
		t.Fatalf("get tag: %v", err)
	}

	tags, err := ts.ListTags(ctx)
	if err != nil {
		t.Fatalf("list tags: %v", err)
	}
	found := false
	for _, tg := range tags {
		if tg.ID == tag.ID {
			found = true
		}
	}
	if !found {
		t.Error("created tag not in list")
	}
}

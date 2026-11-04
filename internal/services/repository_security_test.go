package services

import (
	"context"
	"errors"
	"testing"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/google/uuid"
)

func TestCreateRepositoryRejectsUnsafeInput(t *testing.T) {
	svc := &RepositoryService{basePath: "/srv/data/repos"}
	cases := map[string]CreateRepositoryRequest{
		"crlf name":       {Name: "evil\r\nBcc: x@y.z", URL: "https://github.com/a/b.git"},
		"blank name":      {Name: "  ", URL: "https://github.com/a/b.git"},
		"url credentials": {Name: "ok", URL: "https://user:token@github.com/a/b.git"},
		"url token user":  {Name: "ok", URL: "https://ghp_x@github.com/a/b.git"},
	}
	for name, req := range cases {
		if _, err := svc.CreateRepository(context.Background(), &req, uuid.New()); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: expected ErrValidation, got %v", name, err)
		}
	}
}

func TestUpdateRepositoryRejectsControlCharsInName(t *testing.T) {
	svc := &RepositoryService{}
	repo := &models.Repository{Name: "ok"}
	bad := "line\nbreak"
	if err := svc.applyUpdateFields(repo, &UpdateRepositoryRequest{Name: &bad}); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
	if repo.Name != "ok" {
		t.Fatalf("name must not change on validation failure, got %q", repo.Name)
	}
}

func TestUpsertGitHubMirrorOnlyOwnerRefreshesCredentials(t *testing.T) {
	testDBAvailable(t)
	ctx := context.Background()
	svc := newRepoService(t, "/srv/data/repos")
	owner := createTestUser(t)
	other := createTestUser(t) // admin role, which must not bypass ownership

	url := "https://github.com/octocat/upsert-" + uuid.NewString()[:8] + ".git"
	created, err := svc.UpsertGitHubMirror(ctx, owner, "upsert", url, "main", true, "owner-token")
	if err != nil || !created {
		t.Fatalf("initial upsert: created=%v err=%v", created, err)
	}
	var repo models.Repository
	if err := database.GetDB().Where("url = ?", url).First(&repo).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.PermanentDeleteRepository(ctx, repo.ID) })

	tokenOf := func() string {
		t.Helper()
		creds, err := svc.GetDecryptedCredentials(ctx, repo.ID)
		if err != nil {
			t.Fatal(err)
		}
		return creds.Token
	}

	if _, err := svc.UpsertGitHubMirror(ctx, other, "upsert", url, "main", true, "attacker-token"); err != nil {
		t.Fatalf("other-user upsert: %v", err)
	}
	if got := tokenOf(); got != "owner-token" {
		t.Fatalf("another user's import replaced the owner's credentials (token=%q)", got)
	}

	if _, err := svc.UpsertGitHubMirror(ctx, owner, "upsert", url, "main", true, "owner-token-2"); err != nil {
		t.Fatalf("owner upsert: %v", err)
	}
	if got := tokenOf(); got != "owner-token-2" {
		t.Fatalf("owner refresh did not update credentials (token=%q)", got)
	}
}

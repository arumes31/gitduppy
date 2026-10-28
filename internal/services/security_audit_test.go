package services

import (
	"errors"
	"testing"

	"github.com/gitduppy/gitduppy/internal/config"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/google/uuid"
)

func TestGitRemoteOrigin(t *testing.T) {
	same := [][2]string{
		{"https://github.com/a/b.git", "https://GitHub.com/c/d.git"},
		{"git@github.com:a/b.git", "deploy@github.com:c/d.git"},
	}
	for _, p := range same {
		if gitRemoteOrigin(p[0]) != gitRemoteOrigin(p[1]) {
			t.Errorf("%q and %q should share an origin", p[0], p[1])
		}
	}
	different := [][2]string{
		{"https://github.com/a/b.git", "https://attacker.test/a/b.git"},
		{"https://github.com/a/b.git", "http://github.com/a/b.git"},
		{"https://git.example.com/a.git", "https://git.example.com:8443/a.git"},
		{"git@github.com:a/b.git", "git@attacker.test:a/b.git"},
	}
	for _, p := range different {
		if gitRemoteOrigin(p[0]) == gitRemoteOrigin(p[1]) {
			t.Errorf("%q and %q must not share an origin", p[0], p[1])
		}
	}
}

func TestApplyUpdateFieldsRequiresCredentialsOnHostChange(t *testing.T) {
	s := &RepositoryService{}
	newRepo := func() *models.Repository {
		return &models.Repository{ID: uuid.New(), URL: "https://github.com/a/b.git", EncryptedCredentials: "ciphertext"}
	}

	// Moving to another host while keeping the stored credentials is refused.
	repo := newRepo()
	evil := "https://attacker.test/x.git"
	if err := s.applyUpdateFields(repo, &UpdateRepositoryRequest{URL: &evil}); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for a host change, got %v", err)
	}
	if repo.URL != "https://github.com/a/b.git" {
		t.Fatalf("URL must be unchanged after a refused update, got %q", repo.URL)
	}

	// Same host, different path is fine.
	repo = newRepo()
	moved := "https://github.com/a/renamed.git"
	if err := s.applyUpdateFields(repo, &UpdateRepositoryRequest{URL: &moved}); err != nil {
		t.Fatalf("same-host URL change should be allowed: %v", err)
	}

	// No stored credentials: nothing to leak.
	repo = newRepo()
	repo.EncryptedCredentials = ""
	if err := s.applyUpdateFields(repo, &UpdateRepositoryRequest{URL: &evil}); err != nil {
		t.Fatalf("host change without stored credentials should be allowed: %v", err)
	}
}

func TestIsValidStoragePath(t *testing.T) {
	base := t.TempDir()
	s := &RepositoryService{basePath: base}
	id := uuid.New()
	idStr := id.String()
	ok := &models.Repository{ID: id, StoragePath: base + "/shards/" + idStr[0:2] + "/" + idStr[2:4] + "/" + idStr}
	if !s.IsValidStoragePath(ok) {
		t.Errorf("canonical shard path rejected: %q", ok.StoragePath)
	}
	for _, p := range []string{base, base + "/shards", base + "/../ssh", "/app"} {
		if s.IsValidStoragePath(&models.Repository{ID: id, StoragePath: p}) {
			t.Errorf("path outside the shard tree accepted: %q", p)
		}
	}
}

func TestOAuthSignupPolicy(t *testing.T) {
	cfg := &config.Config{}
	s := &OAuthService{configService: &ConfigService{config: cfg}}

	if !s.signupAllowed("anyone@example.org") {
		t.Error("empty allowlist should allow any domain")
	}
	if s.autoActivate() {
		t.Error("OAuth sign-ups must default to inactive")
	}

	cfg.OAuth.AllowedEmailDomains = []string{"Example.com", "@corp.test"}
	cfg.OAuth.AutoActivate = true
	for email, want := range map[string]bool{
		"dev@example.com":         true,
		"dev@EXAMPLE.COM":         true,
		"ops@corp.test":           true,
		"dev@evil-example.com":    false,
		"dev@example.com.evil.io": false,
		"no-at-sign":              false,
	} {
		if got := s.signupAllowed(email); got != want {
			t.Errorf("signupAllowed(%q)=%v want %v", email, got, want)
		}
	}
	if !s.autoActivate() {
		t.Error("auto_activate should be honored")
	}
}

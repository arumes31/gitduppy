package services

import (
	"context"
	"testing"
	"time"

	"github.com/gitduppy/gitduppy/internal/config"
	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/pkg/crypto"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestUserCreateWritesExplicitInactive proves, without a database, that GORM's
// INSERT for a User carries an explicit is_active=false. With the former
// `default:true` tag GORM dropped the zero value from the INSERT and the column
// default activated the account (OAuth sign-ups bypassed admin approval).
func TestUserCreateWritesExplicitInactive(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=127.0.0.1 port=1 dbname=none"}), &gorm.Config{
		DryRun:                 true,
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatalf("open dry-run db: %v", err)
	}
	res := db.Create(&models.User{ID: uuid.New(), Username: "u", Email: "u@x", Role: "user", IsActive: false})
	if res.Error != nil {
		t.Fatalf("dry-run create: %v", res.Error)
	}
	stmt := res.Statement

	f := stmt.Schema.LookUpField("is_active")
	if f == nil {
		t.Fatal("is_active field missing from schema")
	}
	if f.HasDefaultValue {
		t.Fatalf("is_active must not have a GORM default (got %q)", f.DefaultValue)
	}
	found := false
	for _, v := range stmt.Vars {
		if b, ok := v.(bool); ok && !b {
			found = true
		}
	}
	if !found {
		t.Fatalf("INSERT must bind is_active=false explicitly; SQL=%s vars=%v", stmt.SQL.String(), stmt.Vars)
	}
}

// TestOAuthSignupWithoutAutoActivatePersistsInactive is the DB-backed check: an
// OAuth sign-up with oauth.auto_activate=false must be stored inactive (the
// callback then refuses to issue a session for !IsActive users).
func TestOAuthSignupWithoutAutoActivatePersistsInactive(t *testing.T) {
	testDBAvailable(t)
	cfg := &config.Config{}
	cfg.OAuth.AutoActivate = false
	svc := NewOAuthService(NewConfigService(cfg, database.GetDB(), nil))

	suffix := uuid.NewString()[:8]
	user, err := svc.CreateOrUpdateUserFromOAuth(context.Background(), GitHubProvider, "sub-"+suffix, "oauth-"+suffix+"@test.local", "oauth-"+suffix)
	if err != nil {
		t.Fatalf("CreateOrUpdateUserFromOAuth: %v", err)
	}
	t.Cleanup(func() { database.GetDB().Unscoped().Delete(&models.User{}, "id = ?", user.ID) })
	if user.IsActive {
		t.Fatal("returned user must be inactive when auto_activate=false")
	}
	var stored models.User
	if err := database.GetDB().First(&stored, "id = ?", user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.IsActive {
		t.Fatal("persisted user must be inactive when auto_activate=false")
	}
}

// TestRefreshSessionDoesNotResurrectDeletedSession verifies a refresh of a
// session deleted by a concurrent logout fails instead of re-inserting it.
func TestRefreshSessionDoesNotResurrectDeletedSession(t *testing.T) {
	testDBAvailable(t)
	db := database.GetDB()
	userID := createTestUser(t)
	svc := &AuthService{db: db, passwordService: crypto.NewPasswordService(), sessionDuration: time.Hour}

	token := uuid.NewString()
	hash := crypto.HashToken(token)
	if err := db.Create(&models.Session{Token: hash, UserID: userID, Data: "{}", Expiry: time.Now().UTC().Add(time.Minute)}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Where("token = ?", hash).Delete(&models.Session{}) })

	got, err := svc.RefreshSession(context.Background(), token)
	if err != nil {
		t.Fatalf("refresh of a live session failed: %v", err)
	}
	if got.Token != hash || got.UserID != userID || !got.Expiry.After(time.Now().Add(30*time.Minute)) {
		t.Fatalf("unexpected refreshed session: %+v", got)
	}

	// Logout deletes the row; a refresh racing behind it must not re-create it.
	if err := db.Where("token = ?", hash).Delete(&models.Session{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RefreshSession(context.Background(), token); err == nil {
		t.Fatal("refresh of a deleted session must fail")
	}
	var n int64
	db.Model(&models.Session{}).Where("token = ?", hash).Count(&n)
	if n != 0 {
		t.Fatal("refresh re-inserted a revoked session")
	}
}

// TestChangePasswordWritesOnlyPassword verifies ChangePassword updates the hash
// and drops sessions without rewriting other columns (is_active/role) from a
// stale in-memory copy.
func TestChangePasswordWritesOnlyPassword(t *testing.T) {
	testDBAvailable(t)
	db := database.GetDB()
	ps := crypto.NewPasswordService()
	oldHash, err := ps.Hash("old-password-123")
	if err != nil {
		t.Fatal(err)
	}
	u := &models.User{ID: uuid.New(), Username: "cp-" + uuid.NewString()[:8], Email: uuid.NewString()[:8] + "@test.local",
		PasswordHash: &oldHash, Role: "user", IsActive: true}
	if err := db.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Unscoped().Delete(u) })
	sessHash := crypto.HashToken(uuid.NewString())
	if err := db.Create(&models.Session{Token: sessHash, UserID: u.ID, Data: "{}", Expiry: time.Now().UTC().Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}

	svc := &AuthService{db: db, passwordService: ps, sessionDuration: time.Hour}
	if err := svc.ChangePassword(context.Background(), u.ID, "wrong", "new-password-456"); err == nil {
		t.Fatal("wrong current password accepted")
	}

	// Simulate an admin disabling the account; ChangePassword must not undo it.
	if err := db.Model(&models.User{}).Where("id = ?", u.ID).Update("is_active", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.ChangePassword(context.Background(), u.ID, "old-password-123", "new-password-456"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	var stored models.User
	if err := db.First(&stored, "id = ?", u.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.IsActive {
		t.Fatal("ChangePassword overwrote a concurrent deactivation")
	}
	if stored.PasswordHash == nil || !ps.Verify(*stored.PasswordHash, "new-password-456") {
		t.Fatal("new password not persisted")
	}
	var n int64
	db.Model(&models.Session{}).Where("user_id = ?", u.ID).Count(&n)
	if n != 0 {
		t.Fatalf("sessions not invalidated: %d remaining", n)
	}
}

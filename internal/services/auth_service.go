package services

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/pkg/crypto"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Account lockout policy for password logins. After maxFailedLoginAttempts
// consecutive failures the account is locked with a progressive backoff: the
// lock starts at baseLockoutDuration and doubles with each further failure,
// capped at maxLockoutDuration. A successful login resets the counter and clears
// the lock.
const (
	maxFailedLoginAttempts = 5
	baseLockoutDuration    = 15 * time.Minute
	maxLockoutDuration     = 24 * time.Hour
)

// AuthCacheInvalidator is the subset of the middleware auth cache this service
// evicts from when a credential is revoked out-of-band of its natural TTL, so a
// logout or password change is not masked by the cache. It is optional: a nil
// invalidator simply falls back to TTL expiry. Kept as a local interface so the
// service does not import the middleware package.
type AuthCacheInvalidator interface {
	Evict(credentialHash string)
	EvictUser(userID uuid.UUID)
}

// AuthService handles authentication logic.
type AuthService struct {
	db              *gorm.DB
	passwordService *crypto.PasswordService
	sessionDuration time.Duration
	auditService    *AuditService
	authCache       AuthCacheInvalidator
}

// NewAuthService creates a new auth service. auditService may be nil, in which
// case security events are simply not recorded.
func NewAuthService(sessionDuration time.Duration, auditService *AuditService) *AuthService {
	return &AuthService{
		db:              database.GetDB(),
		passwordService: crypto.NewPasswordService(),
		sessionDuration: sessionDuration,
		auditService:    auditService,
	}
}

// SetAuthCache wires the middleware auth cache so logout and password change evict
// the affected cached credentials eagerly. Call once at startup.
func (s *AuthService) SetAuthCache(cache AuthCacheInvalidator) {
	s.authCache = cache
}

// audit records a security-relevant event, ignoring logging errors so an audit
// failure never blocks the primary flow. It is a no-op when no audit service is
// wired in.
func (s *AuthService) audit(ctx context.Context, userID *uuid.UUID, action string, details any) {
	if s.auditService == nil {
		return
	}
	_ = s.auditService.Log(ctx, userID, nil, action, details, "", "")
}

// LoginRequest represents a login request.
type LoginRequest struct {
	Username   string `json:"username" validate:"required"`
	Password   string `json:"password" validate:"required"`
	RememberMe bool   `json:"remember_me"`
}

// LoginResponse represents a login response.
type LoginResponse struct {
	User         *models.User `json:"user"`
	SessionToken string       `json:"session_token,omitempty"`
	ExpiresAt    time.Time    `json:"expires_at"`
}

// Login authenticates a user and creates a session.
func (s *AuthService) Login(ctx context.Context, req *LoginRequest) (*LoginResponse, error) {
	// Find the user with a deterministic lookup: an exact username match takes
	// precedence, falling back to an email match only when no username matches.
	// A combined "username = ? OR email = ?" query could return whichever row
	// the database happened to order first and authenticate the wrong account
	// when one user's username equals another user's email.
	var user models.User
	err := s.db.Where("username = ?", req.Username).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = s.db.Where("email = ?", req.Username).First(&user).Error
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("invalid username or password")
		}
		return nil, err
	}

	// Check if user is active
	if !user.IsActive {
		return nil, errors.New("account is disabled")
	}

	// Reject a locked account BEFORE running bcrypt, so a locked-out attacker can
	// neither authenticate nor use the endpoint as a bcrypt oracle. The client is
	// told only "invalid username or password" (the lockout is not disclosed), but
	// it is audited so operators can see the brute-force being throttled.
	if user.LockedUntil != nil && user.LockedUntil.After(time.Now()) {
		s.audit(ctx, &user.ID, "login.locked", map[string]any{
			"username":      user.Username,
			"locked_until":  user.LockedUntil,
			"failed_before": user.FailedLoginAttempts,
		})
		return nil, errors.New("invalid username or password")
	}

	// Verify password
	if user.PasswordHash == nil || !s.passwordService.Verify(*user.PasswordHash, req.Password) {
		// Count the failure atomically: a read-modify-write (read count, ++, write)
		// lets two concurrent failures both persist the same value and undercount
		// toward the lockout threshold, so increment in SQL and read the new count
		// back via RETURNING to decide whether to lock. These writes are best-effort
		// brute-force throttling — on error we log and still return the generic
		// failure (never the DB error) so the security response is unchanged. The
		// generic failure is audited (with client IP) by the caller as
		// "auth.login_failed"; only the lockout transition is recorded here.
		if err := s.db.Model(&user).
			Clauses(clause.Returning{Columns: []clause.Column{{Name: "failed_login_attempts"}}}).
			Update("failed_login_attempts", gorm.Expr("failed_login_attempts + 1")).Error; err != nil {
			zap.L().Named("auth-service").Error("failed to record failed login attempt",
				zap.String("user_id", user.ID.String()), zap.Error(err))
		} else if user.FailedLoginAttempts >= maxFailedLoginAttempts {
			lockedUntil := time.Now().UTC().Add(lockoutDuration(user.FailedLoginAttempts))
			if err := s.db.Model(&user).Update("locked_until", lockedUntil).Error; err != nil {
				zap.L().Named("auth-service").Error("failed to persist account lockout",
					zap.String("user_id", user.ID.String()), zap.Error(err))
			} else {
				s.audit(ctx, &user.ID, "login.locked", map[string]any{
					"username":     user.Username,
					"locked_until": lockedUntil,
					"failed_count": user.FailedLoginAttempts,
				})
			}
		}
		return nil, errors.New("invalid username or password")
	}

	// Successful login: clear any lockout state and record the last-login time.
	// Correct credentials were supplied and the account is not locked (checked
	// above), so a failure to clear this throttle bookkeeping must not block the
	// login — log it and continue; the next successful login clears the stale
	// counter.
	now := time.Now().UTC()
	user.LastLogin = &now
	user.FailedLoginAttempts = 0
	user.LockedUntil = nil
	if err := s.db.Model(&user).Updates(map[string]any{
		"last_login":            now,
		"failed_login_attempts": 0,
		"locked_until":          nil,
	}).Error; err != nil {
		zap.L().Named("auth-service").Error("failed to reset login state after success",
			zap.String("user_id", user.ID.String()), zap.Error(err))
	}

	// Generate session token
	sessionToken, err := s.GenerateSessionToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate session token: %w", err)
	}
	expiresAt := time.Now().UTC().Add(s.sessionDuration)

	// Create session. The raw token is returned to the caller (and set as the
	// cookie) but only its SHA-256 hash is stored, so a database leak cannot yield
	// usable session cookies. See the Session model note: pre-hash sessions stop
	// matching after deploy and users must log in again once.
	session := &models.Session{
		Token:  crypto.HashToken(sessionToken),
		UserID: user.ID,
		Data:   `{"auth_type":"password"}`,
		Expiry: expiresAt,
	}
	if err := s.db.Create(session).Error; err != nil {
		return nil, err
	}

	return &LoginResponse{
		User:         &user,
		SessionToken: sessionToken,
		ExpiresAt:    expiresAt,
	}, nil
}

// lockoutDuration returns the progressive lockout window for a given number of
// consecutive failed attempts: baseLockoutDuration doubled once per failure past
// the threshold, capped at maxLockoutDuration.
func lockoutDuration(failedAttempts int) time.Duration {
	shift := failedAttempts - maxFailedLoginAttempts
	if shift < 0 {
		shift = 0
	}
	// Cap the shift so the bit-shift below cannot overflow; baseLockoutDuration
	// shifted by 10 already dwarfs maxLockoutDuration, so it is clamped anyway.
	if shift > 10 {
		shift = 10
	}
	d := baseLockoutDuration << uint(shift)
	if d <= 0 || d > maxLockoutDuration {
		return maxLockoutDuration
	}
	return d
}

// Logout invalidates a session. The stored key is the hash of the token, so the
// raw cookie value is hashed before deleting. The same hash is evicted from the
// auth cache so the just-deleted session cannot be served from cache for the
// remainder of its TTL.
func (s *AuthService) Logout(_ context.Context, sessionToken string) error {
	tokenHash := crypto.HashToken(sessionToken)
	err := s.db.Where("token = ?", tokenHash).Delete(&models.Session{}).Error
	if err == nil && s.authCache != nil {
		s.authCache.Evict(tokenHash)
	}
	return err
}

// RefreshSession extends a session's expiry. The raw cookie value is hashed
// before lookup to match the stored (hashed) session key.
func (s *AuthService) RefreshSession(_ context.Context, sessionToken string) (*models.Session, error) {
	var session models.Session
	if err := s.db.Where("token = ? AND expiry > ?", crypto.HashToken(sessionToken), time.Now()).First(&session).Error; err != nil {
		return nil, errors.New("invalid or expired session")
	}

	// Extend expiry
	session.Expiry = time.Now().UTC().Add(s.sessionDuration)
	if err := s.db.Save(&session).Error; err != nil {
		return nil, err
	}

	return &session, nil
}

// ValidateSession checks if a session is valid and returns the user. The raw
// cookie value is hashed before lookup to match the stored (hashed) session key.
func (s *AuthService) ValidateSession(_ context.Context, sessionToken string) (*models.User, error) {
	var session models.Session
	if err := s.db.Where("token = ? AND expiry > ?", crypto.HashToken(sessionToken), time.Now()).First(&session).Error; err != nil {
		return nil, errors.New("invalid or expired session")
	}

	var user models.User
	if err := s.db.First(&user, session.UserID).Error; err != nil {
		return nil, err
	}

	if !user.IsActive {
		return nil, errors.New("account is disabled")
	}

	return &user, nil
}

// GenerateSessionToken creates a random session token.
func (s *AuthService) GenerateSessionToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// SessionDuration returns the configured session duration.
func (s *AuthService) SessionDuration() time.Duration {
	return s.sessionDuration
}

// DB returns the database connection.
func (s *AuthService) DB() *gorm.DB {
	return s.db
}

// ChangePassword changes a user's password.
func (s *AuthService) ChangePassword(_ context.Context, userID uuid.UUID, oldPassword, newPassword string) error {
	var user models.User
	if err := s.db.First(&user, userID).Error; err != nil {
		return errors.New("user not found")
	}

	// Verify old password
	if user.PasswordHash == nil || !s.passwordService.Verify(*user.PasswordHash, oldPassword) {
		return errors.New("invalid current password")
	}

	// Hash new password
	newHash, err := s.passwordService.Hash(newPassword)
	if err != nil {
		return err
	}

	// Persist the new password and invalidate every existing session for this
	// user in one transaction. A password change must log out all devices —
	// including any attacker holding a stolen session cookie — otherwise the old
	// sessions stay valid until natural expiry.
	user.PasswordHash = &newHash
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		return tx.Where("user_id = ?", userID).Delete(&models.Session{}).Error
	}); err != nil {
		return err
	}

	// Every session for this user was just deleted; evict all of the user's cached
	// entries too so a stolen session (or a coincident lockout/deactivation) is not
	// served from the auth cache for up to its TTL. Keyed by user id because the
	// cache is keyed by credential hash, not user id.
	if s.authCache != nil {
		s.authCache.EvictUser(userID)
	}
	return nil
}

// GetUserByID retrieves a user by ID.
func (s *AuthService) GetUserByID(_ context.Context, id uuid.UUID) (*models.User, error) {
	var user models.User
	if err := s.db.First(&user, id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

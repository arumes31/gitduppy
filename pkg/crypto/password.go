package crypto

import (
	"golang.org/x/crypto/bcrypt"
)

// PasswordService handles password hashing and verification.
type PasswordService struct {
	cost int
}

// NewPasswordService creates a new password service.
func NewPasswordService() *PasswordService {
	return &PasswordService{
		cost: bcrypt.DefaultCost,
	}
}

// NewPasswordServiceWithCost creates a new password service with custom cost.
func NewPasswordServiceWithCost(cost int) *PasswordService {
	return &PasswordService{
		cost: cost,
	}
}

// Hash hashes a password using bcrypt.
func (s *PasswordService) Hash(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), s.cost)
	return string(bytes), err
}

// Compare compares a password with its hash.
func (s *PasswordService) Compare(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// Verify checks if a password matches a hash.
func (s *PasswordService) Verify(hash, password string) bool {
	return s.Compare(hash, password) == nil
}

// HashPassword is a convenience function for hashing passwords.
func HashPassword(password string) (string, error) {
	s := NewPasswordService()
	return s.Hash(password)
}

// VerifyPassword is a convenience function for verifying passwords.
func VerifyPassword(hash, password string) bool {
	s := NewPasswordService()
	return s.Verify(hash, password)
}

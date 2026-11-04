package validator

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"unicode"
)

// MinPasswordLength is the floor for any configured password policy, and
// MaxPasswordBytes is bcrypt's input limit: longer passwords cannot be hashed.
const (
	MinPasswordLength = 8
	MaxPasswordBytes  = 72
)

// PasswordPolicy describes the complexity rules a new password must satisfy.
// It is configured through security.password_* (see config.SecurityConfig).
type PasswordPolicy struct {
	MinLength        int
	RequireUppercase bool
	RequireLowercase bool
	RequireNumbers   bool
	RequireSpecial   bool
}

// DefaultPasswordPolicy is the policy used when none was configured: a minimum
// length only, matching the historical fixed min=8 rule.
func DefaultPasswordPolicy() PasswordPolicy {
	return PasswordPolicy{MinLength: MinPasswordLength}
}

// Validate returns an error describing every rule the password violates, or nil.
// Length is counted in characters (runes); the bcrypt limit in bytes.
func (p PasswordPolicy) Validate(password string) error {
	minLen := max(p.MinLength, MinPasswordLength)
	var problems []string
	if n := len([]rune(password)); n < minLen {
		problems = append(problems, fmt.Sprintf("at least %d characters", minLen))
	}
	if len(password) > MaxPasswordBytes {
		problems = append(problems, fmt.Sprintf("at most %d bytes", MaxPasswordBytes))
	}

	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsSpace(r):
			hasSpecial = true
		}
	}
	if p.RequireUppercase && !hasUpper {
		problems = append(problems, "an uppercase letter")
	}
	if p.RequireLowercase && !hasLower {
		problems = append(problems, "a lowercase letter")
	}
	if p.RequireNumbers && !hasDigit {
		problems = append(problems, "a number")
	}
	if p.RequireSpecial && !hasSpecial {
		problems = append(problems, "a special character")
	}

	if len(problems) == 0 {
		return nil
	}
	return errors.New("password must contain " + strings.Join(problems, ", "))
}

//nolint:gochecknoglobals
var activePasswordPolicy atomic.Pointer[PasswordPolicy]

// SetPasswordPolicy installs the process-wide password policy. Call once at
// startup from the loaded configuration; until then DefaultPasswordPolicy applies.
func SetPasswordPolicy(p PasswordPolicy) {
	activePasswordPolicy.Store(&p)
}

// ActivePasswordPolicy returns the policy installed by SetPasswordPolicy, or
// DefaultPasswordPolicy when none was set.
func ActivePasswordPolicy() PasswordPolicy {
	if p := activePasswordPolicy.Load(); p != nil {
		return *p
	}
	return DefaultPasswordPolicy()
}

// ValidatePassword checks a new password against the active policy. Every code
// path that sets a password (change, user creation, admin reset) must call it.
func ValidatePassword(password string) error {
	return ActivePasswordPolicy().Validate(password)
}

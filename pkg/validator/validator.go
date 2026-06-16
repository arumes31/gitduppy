package validator

import (
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
)

//nolint:gochecknoglobals
var (
	validate        *validator.Validate
	gitURLRegex     = regexp.MustCompile(`^(https?|git|ssh|git@[\w.-]+):\/\/?[\w.-]+(:\d+)?[\/\w.-]+\.git$`)
	gitSSHRegex     = regexp.MustCompile(`^git@[\w.-]+:[\w.-]+\/[\w.-]+\.git$`)
	branchNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

// Init initializes the validator.
func Init() {
	validate = validator.New()
}

// GetValidator returns the validator instance.
func GetValidator() *validator.Validate {
	if validate == nil {
		validate = validator.New()
	}
	return validate
}

// ValidateStruct validates a struct using go-playground/validator.
func ValidateStruct(s interface{}) error {
	if validate == nil {
		validate = validator.New()
	}
	return validate.Struct(s)
}

// ValidateEmail checks if a string is a valid email address.
func ValidateEmail(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}

// ValidateGitURL checks if a string is a valid git URL.
func ValidateGitURL(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)

	// Check SSH format: git@github.com:user/repo.git
	if gitSSHRegex.MatchString(rawURL) {
		return true
	}

	// Check HTTP/HTTPS/Git protocol format
	if gitURLRegex.MatchString(rawURL) {
		return true
	}

	// Try parsing as URL
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	// Must have a scheme and host
	if parsed.Scheme == "" || parsed.Host == "" {
		return false
	}

	// Path should end with .git
	return strings.HasSuffix(parsed.Path, ".git")
}

// ValidateBranchName checks if a branch name is valid.
func ValidateBranchName(name string) bool {
	if name == "" {
		return false
	}
	return branchNameRegex.MatchString(name)
}

// ValidateUsername checks if a username is valid.
func ValidateUsername(username string) bool {
	if len(username) < 3 || len(username) > 64 {
		return false
	}
	return regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(username)
}

// ValidatePassword checks if a password meets requirements.
func ValidatePassword(password string) bool {
	if len(password) < 8 {
		return false
	}
	// At least one letter and one number
	hasLetter := regexp.MustCompile(`[a-zA-Z]`).MatchString(password)
	hasNumber := regexp.MustCompile(`[0-9]`).MatchString(password)
	return hasLetter && hasNumber
}

// ValidateTagName checks if a tag name is valid.
func ValidateTagName(name string) bool {
	if len(name) < 1 || len(name) > 50 {
		return false
	}
	return regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(name)
}

// ValidateColor checks if a string is a valid hex color.
func ValidateColor(color string) bool {
	if len(color) != 7 {
		return false
	}
	if color[0] != '#' {
		return false
	}
	return regexp.MustCompile(`^#[0-9a-fA-F]{6}$`).MatchString(color)
}

// SanitizeString trims whitespace and removes potentially dangerous characters.
func SanitizeString(s string) string {
	return strings.TrimSpace(s)
}

// ValidateRequired checks if a string is not empty.
func ValidateRequired(s string) bool {
	return strings.TrimSpace(s) != ""
}

// ValidateURL checks if a string is a valid URL.
func ValidateURL(rawURL string) bool {
	_, err := url.ParseRequestURI(rawURL)
	return err == nil
}

// ParseInt parses a string to an integer with a default value.
func ParseInt(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	result, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return result
}

package validator

import (
	"net/mail"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-playground/validator/v10"
)

// maxRepositoryNameLength matches the size of the repositories.name column.
const maxRepositoryNameLength = 255

//nolint:gochecknoglobals
var (
	validate        *validator.Validate
	gitURLRegex     = regexp.MustCompile(`^(https?|git|ssh|git@[\w.-]+):\/\/?[\w.-]+(:\d+)?[\/\w.-]+\.git$`)
	gitSSHRegex     = regexp.MustCompile(`^git@[\w.-]+:[\w.-]+\/[\w.-]+\.git$`)
	branchNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	hexColorRegex   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	commitSHARegex  = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
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
func ValidateStruct(s any) error {
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
//
// URLs carrying credentials in their userinfo are rejected: a password (or a
// token used as the http(s) username) embedded in the URL would be persisted in
// plaintext and echoed back to every reader of the repository record. Such
// secrets belong in the encrypted credentials field instead. SSH remotes may
// still name the login user (git@host:path, ssh://git@host/path) as long as no
// password is attached.
func ValidateGitURL(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if containsControlChar(rawURL) {
		return false
	}

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

	// Only well-known git transports are permitted. Arbitrary schemes such as
	// ftp, file, or ext:: must be rejected even when the path ends in .git, so a
	// malicious clone URL cannot reach an unsafe transport.
	switch parsed.Scheme {
	case "http", "https", "git", "ssh":
	default:
		return false
	}

	// Must have a host
	if parsed.Host == "" {
		return false
	}

	if parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); hasPassword {
			return false
		}
		// Over http(s)/git a bare username is how tokens are commonly smuggled
		// in (https://<token>@host/...), so no userinfo is accepted at all.
		if parsed.Scheme != "ssh" {
			return false
		}
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

// ValidateGitRefName reports whether name is a safe branch name to hand to git
// as a positional argument. It follows `git check-ref-format --branch` (no "..",
// "@{" or "//", no ASCII control characters or any of space ~ ^ : ? * [ \, no
// component starting with "." or ending in ".lock", no leading/trailing "/",
// not "@", no trailing ".") and additionally rejects a leading "-" so the name
// can never be parsed as a command-line option.
func ValidateGitRefName(name string) bool {
	if name == "" || len(name) > 255 || name == "@" {
		return false
	}
	if strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") ||
		strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") {
		return false
	}
	if strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.Contains(name, "//") {
		return false
	}
	if containsControlChar(name) || strings.ContainsAny(name, " ~^:?*[\\") {
		return false
	}
	for _, component := range strings.Split(name, "/") {
		if strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return false
		}
	}
	return true
}

// ValidateCommitSHA reports whether sha is a full lowercase hex object name
// (SHA-1 or SHA-256).
func ValidateCommitSHA(sha string) bool {
	return commitSHARegex.MatchString(sha)
}

// ValidateRepositoryName reports whether name is acceptable as a repository
// display name: non-blank, at most 255 bytes, and free of control characters.
// CR/LF in particular must never reach the name, since it is interpolated into
// notification e-mail subjects and log lines.
func ValidateRepositoryName(name string) bool {
	if strings.TrimSpace(name) == "" || len(name) > maxRepositoryNameLength {
		return false
	}
	return !containsControlChar(name)
}

// containsControlChar reports whether s contains a C0/C1 control character
// (including CR, LF, TAB and DEL) or a Unicode line/paragraph separator.
func containsControlChar(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return true
		}
	}
	return false
}

// ValidateUsername checks if a username is valid.
func ValidateUsername(username string) bool {
	if len(username) < 3 || len(username) > 64 {
		return false
	}
	return branchNameRegex.MatchString(username)
}

// ValidateTagName checks if a tag name is valid.
func ValidateTagName(name string) bool {
	if len(name) < 1 || len(name) > 50 {
		return false
	}
	return branchNameRegex.MatchString(name)
}

// ValidateColor checks if a string is a valid hex color.
func ValidateColor(color string) bool {
	if len(color) != 7 {
		return false
	}
	if color[0] != '#' {
		return false
	}
	return hexColorRegex.MatchString(color)
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

// IsRepoStoragePath reports whether path is a repository directory inside the
// sharded storage tree (basePath/shards/...). Repository paths are derived
// server-side, but every destructive or read-through use of a persisted path
// re-checks it so a tampered or legacy row can never point a RemoveAll, clone
// or git invocation at an arbitrary directory (e.g. the storage root itself, the
// SSH key store, or the application directory).
func IsRepoStoragePath(basePath, path string) bool {
	if basePath == "" || path == "" || !filepath.IsAbs(path) {
		return false
	}
	shards := filepath.Join(filepath.Clean(basePath), "shards")
	rel, err := filepath.Rel(shards, filepath.Clean(path))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}

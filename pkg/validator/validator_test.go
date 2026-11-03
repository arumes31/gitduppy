package validator

import (
	"path/filepath"
	"testing"
)

func TestValidateEmail(t *testing.T) {
	cases := map[string]bool{
		"a@b.com":           true,
		"alice@example.org": true,
		"not-an-email":      false,
		"":                  false,
		"@no-local.com":     false,
		"space in@mail.com": false,
		"tag <t@ag.com>":    true, // mail.ParseAddress accepts display-name form
	}
	for in, want := range cases {
		if got := ValidateEmail(in); got != want {
			t.Errorf("ValidateEmail(%q)=%v want %v", in, got, want)
		}
	}
}

func TestValidateGitURL(t *testing.T) {
	cases := map[string]bool{
		// Allowed transports: http, https, git, ssh (plus scp-like git@ SSH).
		"https://github.com/user/repo.git":  true,
		"http://gitlab.com/a/b.git":         true,
		"git@github.com:user/repo.git":      true,
		"git://host/user/repo.git":          true,
		"ssh://git@host/user/repo.git":      true,
		"https://example.com/deep/path.git": true,
		// Rejected: missing .git suffix / not a URL.
		"https://github.com/user/repo": false, // no .git suffix and not matched by regex
		"not a url":                    false,
		"":                             false,
		// Rejected: unsafe transports, even with a .git path.
		"ftp://host/x.git":       false,
		"file:///etc/passwd.git": false,
		"ext::sh -c whoami":      false,
	}
	for in, want := range cases {
		if got := ValidateGitURL(in); got != want {
			t.Errorf("ValidateGitURL(%q)=%v want %v", in, got, want)
		}
	}
}

func TestValidateBranchName(t *testing.T) {
	cases := map[string]bool{
		"main":       true,
		"feature-1":  true,
		"release_2":  true,
		"":           false,
		"has space":  false,
		"bad/slash":  false,
		"semi;colon": false,
	}
	for in, want := range cases {
		if got := ValidateBranchName(in); got != want {
			t.Errorf("ValidateBranchName(%q)=%v want %v", in, got, want)
		}
	}
}

func TestValidateUsername(t *testing.T) {
	cases := map[string]bool{
		"bob":       true,
		"ab":        false, // too short
		"a_b-c123":  true,
		"has space": false,
		"":          false,
	}
	for in, want := range cases {
		if got := ValidateUsername(in); got != want {
			t.Errorf("ValidateUsername(%q)=%v want %v", in, got, want)
		}
	}
	// too long
	long := ""
	for i := 0; i < 65; i++ {
		long += "a"
	}
	if ValidateUsername(long) {
		t.Error("expected too-long username to be invalid")
	}
}

func TestValidateTagName(t *testing.T) {
	if !ValidateTagName("production") {
		t.Error("valid tag rejected")
	}
	if ValidateTagName("") {
		t.Error("empty tag accepted")
	}
	if ValidateTagName("bad tag") {
		t.Error("tag with space accepted")
	}
}

func TestValidateColor(t *testing.T) {
	cases := map[string]bool{
		"#ffffff": true,
		"#000000": true,
		"#AbC123": true,
		"ffffff":  false, // missing #
		"#fff":    false, // too short
		"#gggggg": false, // non-hex
		"":        false,
	}
	for in, want := range cases {
		if got := ValidateColor(in); got != want {
			t.Errorf("ValidateColor(%q)=%v want %v", in, got, want)
		}
	}
}

func TestSanitizeAndRequired(t *testing.T) {
	if SanitizeString("  hi  ") != "hi" {
		t.Error("SanitizeString should trim")
	}
	if !ValidateRequired(" x ") {
		t.Error("non-empty should be required-valid")
	}
	if ValidateRequired("   ") {
		t.Error("whitespace-only should be invalid")
	}
}

func TestValidateURL(t *testing.T) {
	if !ValidateURL("https://example.com/path") {
		t.Error("valid URL rejected")
	}
	if ValidateURL("not-a-uri") {
		t.Error("invalid URL accepted")
	}
}

func TestParseInt(t *testing.T) {
	if ParseInt("42", 1) != 42 {
		t.Error("valid int")
	}
	if ParseInt("", 7) != 7 {
		t.Error("empty should return default")
	}
	if ParseInt("abc", 3) != 3 {
		t.Error("invalid should return default")
	}
}

func TestValidatorInstance(t *testing.T) {
	Init()
	if GetValidator() == nil {
		t.Fatal("GetValidator returned nil")
	}
	type s struct {
		Name string `validate:"required"`
	}
	if err := ValidateStruct(&s{}); err == nil {
		t.Error("expected validation error for missing required field")
	}
	if err := ValidateStruct(&s{Name: "x"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestIsRepoStoragePath(t *testing.T) {
	base := filepath.Join(t.TempDir(), "repos")
	shards := filepath.Join(base, "shards")
	cases := map[string]bool{
		filepath.Join(shards, "ab", "cd", "abcd-uuid"):           true,
		filepath.Join(shards, "ab", "cd", "abcd-uuid") + ".wiki": true,
		shards:                                   false, // the shard root itself
		base:                                     false, // the storage root
		filepath.Join(base, "pools"):             false,
		filepath.Join(shards, "..", "..", "ssh"): false,
		filepath.Join(filepath.Dir(base), "ssh"): false,
		"relative/shards/ab/cd/x":                false,
		"":                                       false,
	}
	for in, want := range cases {
		if got := IsRepoStoragePath(base, in); got != want {
			t.Errorf("IsRepoStoragePath(%q)=%v want %v", in, got, want)
		}
	}
	if IsRepoStoragePath("", filepath.Join(shards, "ab")) {
		t.Error("empty base path must never validate")
	}
}

func TestValidateGitURLRejectsEmbeddedCredentials(t *testing.T) {
	cases := map[string]bool{
		"https://user:token@github.com/a/b.git": false,
		"https://ghp_abc123@github.com/a/b.git": false, // token as bare username
		"http://user@host/a/b.git":              false,
		"git://user:pw@host/a/b.git":            false,
		"ssh://git:secret@host/a/b.git":         false,
		"ssh://git@host:2222/a/b.git":           true,
		"git@github.com:user/repo.git":          true,
		"https://github.com/a/b.git\r\nX: y":    false,
	}
	for in, want := range cases {
		if got := ValidateGitURL(in); got != want {
			t.Errorf("ValidateGitURL(%q)=%v want %v", in, got, want)
		}
	}
}

func TestValidateGitRefName(t *testing.T) {
	cases := map[string]bool{
		"main":                    true,
		"feature/x-1":             true,
		"release/v1.2":            true,
		"user/ünïcode":            true,
		"":                        false,
		"-f":                      false,
		"--upload-pack=evil":      false,
		"a..b":                    false,
		"a//b":                    false,
		"/a":                      false,
		"a/":                      false,
		"a.":                      false,
		"a/.hidden":               false,
		"x.lock":                  false,
		"a/b.lock/c":              false,
		"@":                       false,
		"a@{1}":                   false,
		"has space":               false,
		"tilde~1":                 false,
		"caret^":                  false,
		"colon:x":                 false,
		"glob*":                   false,
		"q?":                      false,
		"br[ack":                  false,
		`back\slash`:              false,
		"ctrl\x01":                false,
		"new\nline":               false,
		"del\x7f":                 false,
		string(make([]byte, 256)): false,
	}
	for in, want := range cases {
		if got := ValidateGitRefName(in); got != want {
			t.Errorf("ValidateGitRefName(%q)=%v want %v", in, got, want)
		}
	}
}

func TestValidateCommitSHA(t *testing.T) {
	cases := map[string]bool{
		"0123456789abcdef0123456789abcdef01234567":                         true,
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef": true,
		"0123456789ABCDEF0123456789abcdef01234567":                         false,
		"-123456789abcdef0123456789abcdef01234567":                         false,
		"abc": false,
		"":    false,
	}
	for in, want := range cases {
		if got := ValidateCommitSHA(in); got != want {
			t.Errorf("ValidateCommitSHA(%q)=%v want %v", in, got, want)
		}
	}
}

func TestValidateRepositoryName(t *testing.T) {
	cases := map[string]bool{
		"my-repo":                 true,
		"My Repo (fork)":          true,
		"":                        false,
		"   ":                     false,
		"evil\r\nBcc: x@y.z":      false,
		"line\nbreak":             false,
		"tab\tname":               false,
		"sep arator":              false,
		string(make([]byte, 256)): false,
	}
	for in, want := range cases {
		if got := ValidateRepositoryName(in); got != want {
			t.Errorf("ValidateRepositoryName(%q)=%v want %v", in, got, want)
		}
	}
}

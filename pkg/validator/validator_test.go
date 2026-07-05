package validator

import "testing"

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

func TestValidatePassword(t *testing.T) {
	cases := map[string]bool{
		"abcd1234":   true,
		"short1":     false, // < 8
		"allletters": false, // no number
		"12345678":   false, // no letter
		"Passw0rd":   true,
	}
	for in, want := range cases {
		if got := ValidatePassword(in); got != want {
			t.Errorf("ValidatePassword(%q)=%v want %v", in, got, want)
		}
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

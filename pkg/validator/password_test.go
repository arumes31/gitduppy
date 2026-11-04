package validator

import (
	"strings"
	"testing"
)

func TestPasswordPolicyValidate(t *testing.T) {
	strict := PasswordPolicy{MinLength: 12, RequireUppercase: true, RequireLowercase: true, RequireNumbers: true, RequireSpecial: true}
	cases := []struct {
		name   string
		policy PasswordPolicy
		pw     string
		ok     bool
	}{
		{"default accepts 8 chars", DefaultPasswordPolicy(), "abcdefgh", true},
		{"default rejects 7 chars", DefaultPasswordPolicy(), "abcdefg", false},
		{"min below floor is raised to 8", PasswordPolicy{MinLength: 4}, "abcdef", false},
		{"rejects over bcrypt limit", DefaultPasswordPolicy(), strings.Repeat("a", 73), false},
		{"strict accepts complex", strict, "Abcdefghij1!", true},
		{"strict rejects short", strict, "Abcdefg1!", false},
		{"strict rejects missing upper", strict, "abcdefghij1!", false},
		{"strict rejects missing lower", strict, "ABCDEFGHIJ1!", false},
		{"strict rejects missing digit", strict, "Abcdefghijk!", false},
		{"strict rejects missing special", strict, "Abcdefghijk1", false},
		{"length counts runes", PasswordPolicy{MinLength: 8}, "äöüßäöüß", true},
	}
	for _, c := range cases {
		err := c.policy.Validate(c.pw)
		if (err == nil) != c.ok {
			t.Errorf("%s: Validate(%q) err=%v, want ok=%v", c.name, c.pw, err, c.ok)
		}
	}
}

func TestActivePasswordPolicy(t *testing.T) {
	t.Cleanup(func() { SetPasswordPolicy(DefaultPasswordPolicy()) })

	if err := ValidatePassword("abcdefgh"); err != nil {
		t.Fatalf("default policy should accept 8 lowercase chars: %v", err)
	}
	SetPasswordPolicy(PasswordPolicy{MinLength: 10, RequireNumbers: true})
	if err := ValidatePassword("abcdefgh"); err == nil {
		t.Fatal("configured policy should reject a short password without digits")
	}
	if err := ValidatePassword("abcdefghi1"); err != nil {
		t.Fatalf("configured policy should accept a compliant password: %v", err)
	}
}

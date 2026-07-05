package email

import (
	"strings"
	"testing"

	"github.com/gitduppy/gitduppy/internal/config"
)

type repoStub struct {
	Name string
	URL  string
}

func TestIsEnabled(t *testing.T) {
	if NewService(nil).IsEnabled() {
		t.Error("nil config should be disabled")
	}
	if NewService(&config.EmailConfig{Enabled: false}).IsEnabled() {
		t.Error("disabled config reported enabled")
	}
	if !NewService(&config.EmailConfig{Enabled: true}).IsEnabled() {
		t.Error("enabled config reported disabled")
	}
}

func TestSendEmailDisabledNoop(t *testing.T) {
	// Disabled or nil config must silently succeed without attempting a connection.
	if err := NewService(nil).SendEmail([]string{"x@y.com"}, "s", "b"); err != nil {
		t.Errorf("nil config send should be nil, got %v", err)
	}
	if err := NewService(&config.EmailConfig{Enabled: false}).SendEmail([]string{"x@y.com"}, "s", "b"); err != nil {
		t.Errorf("disabled send should be nil, got %v", err)
	}
}

func TestRenderTemplate(t *testing.T) {
	s := NewService(&config.EmailConfig{})
	data := TemplateData{
		AppName:    "GitDuppy",
		Repository: repoStub{Name: "repo1", URL: "https://x/y.git"},
		Error:      "boom",
		Timestamp:  "2026-01-01",
		BaseURL:    "http://localhost",
	}

	cf, err := s.RenderTemplate("clone_failure", data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Clone Failure", "repo1", "https://x/y.git", "boom", "http://localhost"} {
		if !strings.Contains(cf, want) {
			t.Errorf("clone_failure missing %q", want)
		}
	}

	se, err := s.RenderTemplate("system_error", data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(se, "System Error") || !strings.Contains(se, "boom") {
		t.Error("system_error template incomplete")
	}

	if _, err := s.RenderTemplate("does_not_exist", data); err == nil {
		t.Error("expected error for unknown template")
	}
}

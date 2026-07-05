package gitops

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRepoURL(t *testing.T) {
	g := NewGitOperations("/tmp")
	cases := map[string]bool{
		"https://github.com/user/repo.git": true,
		"http://host/a/b.git":              true,
		"git@github.com:user/repo.git":     true,
		"git://host/user/repo.git":         true,
		"not-a-url":                        false,
		"https://github.com/user/repo":     false,
		"":                                 false,
	}
	for in, want := range cases {
		if got := g.ValidateRepoURL(context.Background(), in); got != want {
			t.Errorf("ValidateRepoURL(%q)=%v want %v", in, got, want)
		}
	}
}

func TestGetGitExecutable(t *testing.T) {
	if GetGitExecutable() == "" {
		t.Error("expected a git executable path")
	}
}

func TestRunGitCommandVersion(t *testing.T) {
	out, err := RunGitCommand(context.Background(), ".", "version")
	if err != nil {
		t.Fatalf("git version failed: %v (%s)", err, out)
	}
	if out == "" {
		t.Error("expected version output")
	}
}

func TestIsRepositoryCloned(t *testing.T) {
	dir := t.TempDir()
	if IsRepositoryCloned := (NewGitOperations("")).IsRepositoryCloned; IsRepositoryCloned(dir) {
		t.Error("empty dir should not be a repo")
	}
	// Create a fake .git dir → treated as cloned.
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if !(NewGitOperations("")).IsRepositoryCloned(dir) {
		t.Error("dir with .git should be detected as cloned")
	}
}

func TestGetReferencesOnRealRepo(t *testing.T) {
	// Initialize a tiny repo and assert GetReferences returns without error.
	dir := t.TempDir()
	ctx := context.Background()
	if _, err := RunGitCommand(ctx, dir, "init"); err != nil {
		t.Skipf("git init unavailable: %v", err)
	}
	g := NewGitOperations("")
	refs, err := g.GetReferences(ctx, dir)
	if err != nil {
		t.Fatalf("GetReferences: %v", err)
	}
	if refs == nil {
		t.Error("expected non-nil refs map (empty repo => empty map)")
	}
}

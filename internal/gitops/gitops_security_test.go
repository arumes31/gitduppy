package gitops

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestGoGitRejectsReferencePathTraversal(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	repoDir := filepath.Join(baseDir, "repo")
	repo, err := git.PlainInit(repoDir, false)
	if err != nil {
		t.Fatalf("initialize repository: %v", err)
	}

	escapedPath := filepath.Join(baseDir, "escaped-ref")
	malicious := plumbing.NewHashReference(
		plumbing.ReferenceName("refs/heads/../../../../escaped-ref"),
		plumbing.NewHash("0123456789012345678901234567890123456789"),
	)
	_ = repo.Storer.SetReference(malicious)

	if _, statErr := os.Stat(escapedPath); !os.IsNotExist(statErr) {
		t.Fatalf("crafted reference escaped the repository: %s", escapedPath)
	}
}

func TestGoGitCheckoutDoesNotFollowDirectorySymlink(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	repoDir := filepath.Join(baseDir, "repo")
	outsideDir := filepath.Join(baseDir, "outside")
	if err := os.MkdirAll(filepath.Join(repoDir, "payload"), 0o750); err != nil {
		t.Fatalf("create repository worktree: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0o750); err != nil {
		t.Fatalf("create outside directory: %v", err)
	}

	repo, err := git.PlainInit(repoDir, false)
	if err != nil {
		t.Fatalf("initialize repository: %v", err)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatalf("open worktree: %v", err)
	}
	trackedPath := filepath.Join(repoDir, "payload", "owned.txt")
	if err := os.WriteFile(trackedPath, []byte("repository content"), 0o600); err != nil {
		t.Fatalf("write tracked file: %v", err)
	}
	if _, err := worktree.Add(filepath.Join("payload", "owned.txt")); err != nil {
		t.Fatalf("stage tracked file: %v", err)
	}
	commitHash, err := worktree.Commit("security fixture", &git.CommitOptions{Author: &object.Signature{
		Name: "GitDuppy Security Test", Email: "security@example.invalid", When: time.Unix(1, 0).UTC(),
	}})
	if err != nil {
		t.Fatalf("commit fixture: %v", err)
	}

	if err := os.RemoveAll(filepath.Join(repoDir, "payload")); err != nil {
		t.Fatalf("remove checked-out directory: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(repoDir, "payload")); err != nil {
		t.Skipf("directory symlinks unavailable on this platform: %v", err)
	}

	_ = worktree.Checkout(&git.CheckoutOptions{Hash: commitHash, Force: true})
	outsideFile := filepath.Join(outsideDir, "owned.txt")
	if _, statErr := os.Stat(outsideFile); !os.IsNotExist(statErr) {
		t.Fatalf("checkout followed a worktree symlink outside the repository: %s", outsideFile)
	}
}

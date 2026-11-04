package handlers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gitduppy/gitduppy/internal/gitops"
	gogit "github.com/go-git/go-git/v5"
)

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("héllo", 10); got != "héllo" {
		t.Errorf("short string changed: %q", got)
	}
	got := truncateRunes(strings.Repeat("é", 300), maxSubjectRunes)
	if n := utf8.RuneCountInString(got); n != maxSubjectRunes+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("truncated to %d runes (%q...)", n, got[:10])
	}
}

func TestCommitSummaryCacheSharesAndBudgets(t *testing.T) {
	huge := strings.Repeat("x", 1<<20)
	c := newCommitSummaryCache(1000)
	a := c.get("aaaa", "author", huge)
	if len(a.message) > maxSubjectRunes+len("…") {
		t.Fatalf("message not bounded: %d bytes", len(a.message))
	}
	if b := c.get("aaaa", "other", "other"); b != a {
		t.Fatalf("same commit hash not served from cache: %+v vs %+v", b, a)
	}
	// Spend the remaining budget; later distinct commits get no message.
	for i := 0; i < 10; i++ {
		c.get(strings.Repeat("b", i+1), "a", huge)
	}
	if s := c.get("cccc", "a", huge); s.message != "" {
		t.Fatalf("aggregate budget not enforced: %q", s.message)
	}
}

// initBrowseRepo creates a small repository with one commit on branch main.
func initBrowseRepo(t *testing.T) (string, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	if _, err := gitops.RunGitCommand(ctx, dir, "init", "-b", "main"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	for _, args := range [][]string{{"config", "user.name", "T"}, {"config", "user.email", "t@example.com"}} {
		if out, err := gitops.RunGitCommand(ctx, dir, args...); err != nil {
			t.Fatal(out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "init"}} {
		if out, err := gitops.RunGitCommand(ctx, dir, args...); err != nil {
			t.Fatal(out)
		}
	}
	head, err := gitops.RunGitCommand(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return dir, strings.TrimSpace(head)
}

func TestTreeBlobSizesAndBlobSize(t *testing.T) {
	ctx := context.Background()
	dir, head := initBrowseRepo(t)
	sizes := treeBlobSizes(ctx, dir, head+"^{tree}")
	if sizes["a.txt"] != 5 {
		t.Fatalf("sizes=%v, want a.txt=5", sizes)
	}
	if _, ok := sizes["sub"]; ok {
		t.Fatalf("subtree reported as blob: %v", sizes)
	}
	oid, err := gitops.RunGitCommand(ctx, dir, "rev-parse", head+":a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := blobSize(ctx, dir, strings.TrimSpace(oid)); err != nil || n != 5 {
		t.Fatalf("blobSize=%d err=%v", n, err)
	}
}

func TestResolveCommitParam(t *testing.T) {
	ctx := context.Background()
	dir, head := initBrowseRepo(t)
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{head, head[:7], strings.ToUpper(head[:10]), "main"} {
		if got, ok := resolveCommitParam(ctx, repo, dir, in); !ok || got != head {
			t.Errorf("resolveCommitParam(%q)=%q,%v want %q", in, got, ok, head)
		}
	}
	for _, in := range []string{"", "--output=/tmp/x", "HEAD~1", "main..main", head + ":a.txt", "main^{tree}", "nope", "abc"} {
		if got, ok := resolveCommitParam(ctx, repo, dir, in); ok {
			t.Errorf("resolveCommitParam(%q)=%q, want rejection", in, got)
		}
	}
}

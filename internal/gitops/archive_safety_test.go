package gitops

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gitduppy/gitduppy/internal/config"
	"github.com/gitduppy/gitduppy/internal/database"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type countingTransport struct {
	requests atomic.Int32
	body     string
}

func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.requests.Add(1)
	if req.URL.Fragment != "" {
		panic("fragment must not reach the transport")
	}
	return &http.Response{
		StatusCode:    http.StatusOK,
		Body:          io.NopCloser(strings.NewReader(t.body)),
		ContentLength: int64(len(t.body)),
		Header:        http.Header{},
		Request:       req,
	}, nil
}

func newTestFetcher(rt http.RoundTripper) *GitHubMetadataFetcher {
	return &GitHubMetadataFetcher{logger: zap.NewNop(), client: &http.Client{Transport: rt}}
}

func openTestRoot(t *testing.T, dir string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

func TestArchiveMediaDedupesFragmentVariants(t *testing.T) {
	rt := &countingTransport{body: "img"}
	f := newTestFetcher(rt)
	dir := t.TempDir()
	root := openTestRoot(t, dir)

	in := []byte(`[{"body":"https://user-images.githubusercontent.com/1/a.png#one and https://user-images.githubusercontent.com/1/a.png#two"}]`)
	budget := &mediaBudget{downloadsLeft: 10, bytesLeft: 1 << 20}
	out := string(f.archiveMedia(context.Background(), root, "", in, budget))

	if got := rt.requests.Load(); got != 1 {
		t.Fatalf("expected 1 download for fragment variants, got %d", got)
	}
	want := "media/" + mediaFilename("https://user-images.githubusercontent.com/1/a.png")
	if strings.Count(out, want) != 2 || strings.Contains(out, "githubusercontent") {
		t.Fatalf("both variants should be rewritten to %s, got %s", want, out)
	}
}

func TestArchiveMediaRespectsDownloadBudget(t *testing.T) {
	rt := &countingTransport{body: "img"}
	f := newTestFetcher(rt)
	root := openTestRoot(t, t.TempDir())

	in := []byte(`["https://user-images.githubusercontent.com/1/a.png","https://user-images.githubusercontent.com/1/b.png","https://user-images.githubusercontent.com/1/c.png"]`)
	budget := &mediaBudget{downloadsLeft: 2, bytesLeft: 1 << 20}
	_ = f.archiveMedia(context.Background(), root, "", in, budget)
	if got := rt.requests.Load(); got != 2 {
		t.Fatalf("expected download budget of 2 to be honored, got %d requests", got)
	}

	// Byte budget: an asset larger than what is left of the budget is not kept.
	rt2 := &countingTransport{body: strings.Repeat("x", 100)}
	f2 := newTestFetcher(rt2)
	budget2 := &mediaBudget{downloadsLeft: 10, bytesLeft: 50}
	out := string(f2.archiveMedia(context.Background(), openTestRoot(t, t.TempDir()), "", []byte(`["https://user-images.githubusercontent.com/1/big.png"]`), budget2))
	if !strings.Contains(out, "githubusercontent") {
		t.Fatalf("over-budget asset must keep its remote URL, got %s", out)
	}
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks not supported here: %v", err)
	}
}

func TestOpenBackupRootRefusesSymlink(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	symlinkOrSkip(t, outside, filepath.Join(repo, backupDirName))

	if r, err := openBackupRoot(repo); err == nil {
		_ = r.Close()
		t.Fatal("expected a symlinked github_backup to be refused")
	}
}

func TestWriteFileReplacingDoesNotFollowSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, outside, filepath.Join(dir, "issues.json"))

	root := openTestRoot(t, dir)
	if err := writeFileReplacing(root, "issues.json", []byte("[]")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "original" {
		t.Fatalf("write followed the symlink; victim now %q", b)
	}
	fi, err := os.Lstat(filepath.Join(dir, "issues.json"))
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("expected issues.json to be a regular file, got %v %v", fi, err)
	}
}

func TestReleaseArchivalRefChecksExpectedSHA(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "one"},
	} {
		if out, err := RunGitCommand(ctx, dir, args...); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	sha, err := RunGitCommand(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	sha = strings.TrimSpace(sha)

	older := models.DeletedBranch{ID: uuid.New(), BranchName: "feature", CommitSHA: strings.Repeat("a", len(sha))}
	older.ArchiveRef = models.DeletedBranchEventRef(older.ID)
	newer := models.DeletedBranch{ID: uuid.New(), BranchName: "feature", CommitSHA: sha}
	newer.ArchiveRef = models.DeletedBranchEventRef(newer.ID)
	if older.ArchiveRef == newer.ArchiveRef {
		t.Fatal("per-event archival refs must differ for the same branch")
	}

	// A ref that points at another commit than the row expects is left alone.
	if _, err := RunGitCommand(ctx, dir, "update-ref", older.ArchiveRef, sha); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseArchivalRef(ctx, nil, dir, &older); err != nil {
		t.Fatalf("release mismatched: %v", err)
	}
	if _, err := RunGitCommand(ctx, dir, "rev-parse", "--verify", "--quiet", older.ArchiveRef); err != nil {
		t.Fatal("ref pointing at a different commit must not be deleted")
	}

	// The matching ref is deleted; a missing ref is not an error.
	if _, err := RunGitCommand(ctx, dir, "update-ref", newer.ArchiveRef, sha); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseArchivalRef(ctx, nil, dir, &newer); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := RunGitCommand(ctx, dir, "rev-parse", "--verify", "--quiet", newer.ArchiveRef); err == nil {
		t.Fatal("matching archival ref should have been deleted")
	}
	if err := ReleaseArchivalRef(ctx, nil, dir, &newer); err != nil {
		t.Fatalf("release of missing ref should succeed: %v", err)
	}
}

func TestSanitizeTopics(t *testing.T) {
	got := sanitizeTopics([]string{"go", "go", "has space", "", "ok-topic", strings.Repeat("x", 51)})
	if len(got) != 2 || got[0] != "go" || got[1] != "ok-topic" {
		t.Fatalf("unexpected sanitized topics: %v", got)
	}
}

func TestReleaseArchivalRefLegacySharedRef(t *testing.T) {
	cfg := &config.DatabaseConfig{
		Host: "localhost", Port: 5433, Name: "gitduppy_test", User: "gitduppy", Password: "gitduppy",
		SSLMode: "disable", MaxOpenConns: 2, MaxIdleConns: 1,
	}
	if database.GetDB() == nil {
		if err := database.Connect(cfg); err != nil {
			t.Skipf("no test database available: %v", err)
		}
		if err := database.AutoMigrate(); err != nil {
			t.Skipf("migration failed: %v", err)
		}
		t.Cleanup(func() { _ = database.Close() })
	}
	db := database.GetDB()
	ctx := context.Background()

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "one"},
	} {
		if out, err := RunGitCommand(ctx, dir, args...); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	sha, _ := RunGitCommand(ctx, dir, "rev-parse", "HEAD")
	sha = strings.TrimSpace(sha)

	repo := models.Repository{ID: uuid.New(), Name: "legacy-ref-test", URL: "https://github.com/a/b.git", StoragePath: dir}
	if err := db.Create(&repo).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Where("repository_id = ?", repo.ID).Delete(&models.DeletedBranch{})
		db.Unscoped().Delete(&repo)
	})

	// Two legacy events of the same branch share refs/paperbin/heads/feature.
	first := models.DeletedBranch{ID: uuid.New(), RepositoryID: repo.ID, BranchName: "feature", CommitSHA: sha}
	second := models.DeletedBranch{ID: uuid.New(), RepositoryID: repo.ID, BranchName: "feature", CommitSHA: sha}
	if err := db.Create(&[]models.DeletedBranch{first, second}).Error; err != nil {
		t.Fatal(err)
	}
	legacyRef := first.ArchivalRef()
	if _, err := RunGitCommand(ctx, dir, "update-ref", legacyRef, sha); err != nil {
		t.Fatal(err)
	}

	if err := ReleaseArchivalRef(ctx, db, dir, &first); err != nil {
		t.Fatalf("release first: %v", err)
	}
	if _, err := RunGitCommand(ctx, dir, "rev-parse", "--verify", "--quiet", legacyRef); err != nil {
		t.Fatal("shared legacy ref must survive while another legacy row still uses it")
	}
	db.Delete(&first)

	if err := ReleaseArchivalRef(ctx, db, dir, &second); err != nil {
		t.Fatalf("release second: %v", err)
	}
	if _, err := RunGitCommand(ctx, dir, "rev-parse", "--verify", "--quiet", legacyRef); err == nil {
		t.Fatal("legacy ref should be deleted once its last row is released")
	}
}

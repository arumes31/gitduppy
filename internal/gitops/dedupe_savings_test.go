package gitops

import (
	"os"
	"path/filepath"
	"testing"
)

// writePoolFile creates a file of exactly size bytes under the pool path for
// url, so DirSize(g.GetPoolPath(url)) returns a known value.
func writePoolFile(t *testing.T, g *GitOperations, url string, size int64) {
	t.Helper()
	poolPath := g.GetPoolPath(url)
	if err := os.MkdirAll(poolPath, 0o750); err != nil {
		t.Fatalf("mkdir pool dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(poolPath, "objects.pack"), make([]byte, size), 0o600); err != nil {
		t.Fatalf("write pool file: %v", err)
	}
}

func TestComputeDedupeSavings(t *testing.T) {
	g := &GitOperations{BasePath: t.TempDir()}

	const sharedURL = "https://github.com/example/shared.git"
	const soloURL = "https://github.com/example/solo.git"
	const sharedPoolSize = int64(1000)

	writePoolFile(t, g, sharedURL, sharedPoolSize)
	writePoolFile(t, g, soloURL, 500) // only one repo uses this URL below

	// Two repositories share sharedURL (and therefore its pool); one repository
	// uses soloURL alone.
	urls := []string{sharedURL, sharedURL, soloURL}

	savings := g.computeDedupeSavings(urls)

	if savings.PoolCount != 1 {
		t.Errorf("PoolCount = %d, want 1 (the solo-repo pool contributes no savings)", savings.PoolCount)
	}
	if savings.SharedBytes != sharedPoolSize {
		t.Errorf("SharedBytes = %d, want %d", savings.SharedBytes, sharedPoolSize)
	}
	// 2 repos sharing the pool: 1 "extra" copy avoided.
	if want := sharedPoolSize * 1; savings.EstimatedSavedBytes != want {
		t.Errorf("EstimatedSavedBytes = %d, want %d", savings.EstimatedSavedBytes, want)
	}
	if savings.ComputedAt.IsZero() {
		t.Errorf("ComputedAt should be set")
	}
}

func TestComputeDedupeSavingsNoSharedPools(t *testing.T) {
	g := &GitOperations{BasePath: t.TempDir()}
	writePoolFile(t, g, "https://github.com/example/a.git", 100)
	writePoolFile(t, g, "https://github.com/example/b.git", 200)

	savings := g.computeDedupeSavings([]string{
		"https://github.com/example/a.git",
		"https://github.com/example/b.git",
	})

	if savings.PoolCount != 0 || savings.SharedBytes != 0 || savings.EstimatedSavedBytes != 0 {
		t.Errorf("expected zero savings when no pool is shared by more than one repo, got %+v", savings)
	}
}

func TestComputeDedupeSavingsMissingPoolOnDisk(t *testing.T) {
	// A repo whose pool was never actually written to disk (e.g. dedupe
	// globally disabled) must not be counted as savings even if two repos
	// nominally hash to the same URL.
	g := &GitOperations{BasePath: t.TempDir()}
	savings := g.computeDedupeSavings([]string{
		"https://github.com/example/never-cloned.git",
		"https://github.com/example/never-cloned.git",
	})
	if savings.PoolCount != 0 || savings.EstimatedSavedBytes != 0 {
		t.Errorf("expected zero savings for a pool absent from disk, got %+v", savings)
	}
}

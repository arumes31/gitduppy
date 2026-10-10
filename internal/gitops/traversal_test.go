package gitops

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/require"
)

func TestCheckoutDoesNotWriteThroughExternalSymlink(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)
	worktree, err := repo.Worktree()
	require.NoError(t, err)
	author := &object.Signature{Name: "Test", Email: "test@example.invalid", When: time.Now()}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README"), []byte("initial"), 0600))
	_, err = worktree.Add("README")
	require.NoError(t, err)
	base, err := worktree.Commit("initial", &git.CommitOptions{Author: author})
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "data"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data", "victim"), []byte("checkout"), 0600))
	_, err = worktree.Add("data/victim")
	require.NoError(t, err)
	next, err := worktree.Commit("add data", &git.CommitOptions{Author: author})
	require.NoError(t, err)
	require.NoError(t, worktree.Checkout(&git.CheckoutOptions{Hash: base, Force: true}))

	external := t.TempDir()
	victim := filepath.Join(external, "victim")
	require.NoError(t, os.WriteFile(victim, []byte("untouched"), 0600))
	err = os.Symlink(external, filepath.Join(dir, "data"))
	if err != nil && runtime.GOOS == "windows" {
		t.Skipf("Windows symlink privilege unavailable: %v", err)
	}
	require.NoError(t, err)
	require.NoError(t, worktree.Checkout(&git.CheckoutOptions{Hash: next, Force: true}))
	contents, err := os.ReadFile(victim)
	require.NoError(t, err)
	require.Equal(t, "untouched", string(contents), "checkout must not escape the worktree")
	contents, err = os.ReadFile(filepath.Join(dir, "data", "victim"))
	require.NoError(t, err)
	require.Equal(t, "checkout", string(contents))
}

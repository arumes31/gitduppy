package gitops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/sideband"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

// GitOperations handles git operations.
type GitOperations struct {
	BasePath string
}

// NewGitOperations creates a new git operations instance.
func NewGitOperations(basePath string) *GitOperations {
	return &GitOperations{
		BasePath: basePath,
	}
}

// CloneOptions holds options for cloning a repository.
type CloneOptions struct {
	URL      string
	Path     string
	Branch   string
	Bare     bool
	LFS      bool
	Progress sideband.Progress
	SSHKey   string
	Username string
	Password string
	Token    string
	Dedupe   bool
}

// CloneRepository clones a git repository.
func (g *GitOperations) CloneRepository(ctx context.Context, opts *CloneOptions) error {
	// Create target directory
	if err := os.MkdirAll(filepath.Dir(opts.Path), 0o750); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Set authentication
	auth, err := g.buildAuth(opts)
	if err != nil {
		return err
	}

	if opts.Dedupe {
		poolPath, err := g.updatePool(ctx, opts.URL, auth, opts.Progress)
		if err == nil {
			// Initialize repository
			r, err := git.PlainInit(opts.Path, opts.Bare)
			if err != nil {
				return fmt.Errorf("failed to init repository: %w", err)
			}

			// Write alternates file
			alternatesFile := filepath.Join(opts.Path, ".git", "objects", "info", "alternates")
			if opts.Bare {
				alternatesFile = filepath.Join(opts.Path, "objects", "info", "alternates")
			}
			if err := os.MkdirAll(filepath.Dir(alternatesFile), 0o750); err != nil {
				return err
			}
			poolObjectsPath := filepath.Join(poolPath, "objects")
			absPoolObjectsPath, err := filepath.Abs(poolObjectsPath)
			if err != nil {
				absPoolObjectsPath = poolObjectsPath
			}
			if err := os.WriteFile(alternatesFile, []byte(absPoolObjectsPath+"\n"), 0o600); err != nil {
				return fmt.Errorf("failed to write alternates file: %w", err)
			}

			// Add remote
			_, err = r.CreateRemote(&config.RemoteConfig{
				Name: "origin",
				URLs: []string{opts.URL},
			})
			if err != nil {
				return fmt.Errorf("failed to create remote: %w", err)
			}

			// Fetch remote objects
			fetchOpts := &git.FetchOptions{
				Progress: opts.Progress,
				Auth:     auth,
			}
			err = r.FetchContext(ctx, fetchOpts)
			if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
				return fmt.Errorf("fetch failed: %w", err)
			}

			// Checkout branch if not bare
			if !opts.Bare {
				w, err := r.Worktree()
				if err != nil {
					return fmt.Errorf("failed to get worktree: %w", err)
				}

				var branchName plumbing.ReferenceName
				if opts.Branch != "" {
					branchName = plumbing.NewBranchReferenceName(opts.Branch)
				} else {
					headRef, err := r.Reference(plumbing.HEAD, true)
					if err == nil {
						branchName = headRef.Name()
					}
				}

				if branchName != "" {
					err = w.Checkout(&git.CheckoutOptions{
						Branch: branchName,
						Force:  true,
					})
					if err != nil {
						return fmt.Errorf("checkout failed: %w", err)
					}
				}
			}

			// Handle LFS
			if opts.LFS {
				if err := g.runLFSInstall(ctx, opts.Path); err != nil {
					return fmt.Errorf("git lfs install failed: %w", err)
				}
				if err := g.runLFSPull(ctx, opts.Path); err != nil {
					return fmt.Errorf("git lfs pull failed: %w", err)
				}
			}

			return nil
		}
	}

	// Build clone options
	cloneOpts := &git.CloneOptions{
		URL:      opts.URL,
		Progress: opts.Progress,
	}

	if opts.Branch != "" {
		cloneOpts.ReferenceName = plumbing.NewBranchReferenceName(opts.Branch)
		cloneOpts.SingleBranch = true
	}

	if auth != nil {
		cloneOpts.Auth = auth
	}

	// Perform clone (isBare is a parameter to PlainCloneContext, not a field)
	_, err = git.PlainCloneContext(ctx, opts.Path, opts.Bare, cloneOpts)
	if err != nil {
		return err
	}

	// Handle Git LFS if enabled
	if opts.LFS {
		if err := g.runLFSInstall(ctx, opts.Path); err != nil {
			return fmt.Errorf("git lfs install failed: %w", err)
		}
		if err := g.runLFSPull(ctx, opts.Path); err != nil {
			return fmt.Errorf("git lfs pull failed: %w", err)
		}
	}

	return nil
}

// FetchRepository fetches updates for a repository.
func (g *GitOperations) FetchRepository(ctx context.Context, opts *CloneOptions) error {
	auth, err := g.buildAuth(opts)
	if err != nil {
		return err
	}

	// If dedupe is enabled, update the pool first so new commits are fetched into the pool
	if opts.Dedupe {
		_, _ = g.updatePool(ctx, opts.URL, auth, opts.Progress)
	}

	repo, err := git.PlainOpen(opts.Path)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	// Build fetch options
	fetchOpts := &git.FetchOptions{
		Progress: opts.Progress,
		Prune:    true,
	}

	if auth != nil {
		fetchOpts.Auth = auth
	}

	// Perform fetch
	if err := repo.FetchContext(ctx, fetchOpts); err != nil {
		// Ignore "already up-to-date" errors
		if !errors.Is(err, git.NoErrAlreadyUpToDate) {
			return err
		}
	}

	// Handle Git LFS fetch if enabled
	if opts.LFS {
		if err := g.runLFSFetch(ctx, opts.Path); err != nil {
			return fmt.Errorf("git lfs fetch failed: %w", err)
		}
	}

	return nil
}

// runLFSInstall installs Git LFS hooks in the repository.
func (g *GitOperations) runLFSInstall(ctx context.Context, path string) error {
	// #nosec G204
	cmd := exec.CommandContext(ctx, GetGitExecutable(), "lfs", "install")
	cmd.Dir = path
	return cmd.Run()
}

// runLFSPull pulls Git LFS objects.
func (g *GitOperations) runLFSPull(ctx context.Context, path string) error {
	// #nosec G204
	cmd := exec.CommandContext(ctx, GetGitExecutable(), "lfs", "pull")
	cmd.Dir = path
	return cmd.Run()
}

// runLFSFetch fetches Git LFS objects.
func (g *GitOperations) runLFSFetch(ctx context.Context, path string) error {
	// #nosec G204
	cmd := exec.CommandContext(ctx, GetGitExecutable(), "lfs", "fetch")
	cmd.Dir = path
	return cmd.Run()
}

// PushRepository pushes changes to a repository.
func (g *GitOperations) PushRepository(ctx context.Context, opts *CloneOptions) error {
	repo, err := git.PlainOpen(opts.Path)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	// Build push options
	pushOpts := &git.PushOptions{
		Progress: opts.Progress,
	}

	auth, err := g.buildAuth(opts)
	if err != nil {
		return err
	}
	if auth != nil {
		pushOpts.Auth = auth
	}

	// Perform push
	return repo.PushContext(ctx, pushOpts)
}

// buildAuth builds authentication for git operations.
func (g *GitOperations) buildAuth(opts *CloneOptions) (transport.AuthMethod, error) {
	// SSH key authentication
	if opts.SSHKey != "" {
		return ssh.NewPublicKeys("git", []byte(opts.SSHKey), "")
	}

	// Token authentication
	if opts.Token != "" {
		return &http.BasicAuth{
			Username: "token",
			Password: opts.Token,
		}, nil
	}

	// Username/password authentication
	if opts.Username != "" && opts.Password != "" {
		return &http.BasicAuth{
			Username: opts.Username,
			Password: opts.Password,
		}, nil
	}

	return nil, nil //nolint:nilnil
}

// SetupSSHKey writes an SSH key to a temporary file with proper permissions.
func (g *GitOperations) SetupSSHKey(keyContent string) (string, error) {
	// Create temp directory if needed
	sshDir := filepath.Join(g.BasePath, "ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return "", err
	}

	// Write key to file
	keyPath := filepath.Join(sshDir, fmt.Sprintf("key_%d", time.Now().UnixNano()))
	if err := os.WriteFile(keyPath, []byte(keyContent), 0o600); err != nil {
		return "", err
	}

	return keyPath, nil
}

// CleanupSSHKey removes an SSH key file.
func (g *GitOperations) CleanupSSHKey(path string) error {
	return os.Remove(path)
}

// GetRepoStatus checks if a path is a valid git repository.
func (g *GitOperations) GetRepoStatus(_ context.Context, path string) (bool, error) {
	_, err := git.PlainOpen(path)
	if err != nil {
		return false, err
	}
	return true, nil
}

// ValidateRepoURL validates a git repository URL.
func (g *GitOperations) ValidateRepoURL(_ context.Context, url string) bool {
	url = strings.TrimSpace(url)

	// Check SSH format: git@github.com:user/repo.git
	sshPattern := regexp.MustCompile(`^git@[\w.-]+:[\w.-]+\/[\w.-]+\.git$`)
	if sshPattern.MatchString(url) {
		return true
	}

	// Check HTTP/HTTPS format
	httpPattern := regexp.MustCompile(`^https?://[\w.-]+(:\d+)?[\/\w.-]+\.git$`)
	if httpPattern.MatchString(url) {
		return true
	}

	// Check git:// format
	gitPattern := regexp.MustCompile(`^git://[\w.-]+[\/\w.-]+\.git$`)
	return gitPattern.MatchString(url)
}

// GetRemoteURL gets the remote URL of a repository.
func (g *GitOperations) GetRemoteURL(path string) (string, error) {
	repo, err := git.PlainOpen(path)
	if err != nil {
		return "", err
	}

	remote, err := repo.Remote("origin")
	if err != nil {
		return "", err
	}

	if len(remote.Config().URLs) == 0 {
		return "", fmt.Errorf("no remote URLs configured")
	}

	return remote.Config().URLs[0], nil
}

// GetCurrentBranch gets the current branch of a repository.
func (g *GitOperations) GetCurrentBranch(path string) (string, error) {
	repo, err := git.PlainOpen(path)
	if err != nil {
		return "", err
	}

	head, err := repo.Head()
	if err != nil {
		return "", err
	}

	return head.Name().Short(), nil
}

// GetLastCommit gets the last commit hash of a repository.
func (g *GitOperations) GetLastCommit(path string) (string, error) {
	repo, err := git.PlainOpen(path)
	if err != nil {
		return "", err
	}

	head, err := repo.Head()
	if err != nil {
		return "", err
	}

	return head.Hash().String(), nil
}

// ListRemotes lists all remotes of a repository.
func (g *GitOperations) ListRemotes(path string) ([]*config.RemoteConfig, error) {
	repo, err := git.PlainOpen(path)
	if err != nil {
		return nil, err
	}

	remotes, err := repo.Remotes()
	if err != nil {
		return nil, err
	}

	remoteConfigs := make([]*config.RemoteConfig, len(remotes))
	for i, r := range remotes {
		remoteConfigs[i] = r.Config()
	}

	return remoteConfigs, nil
}

// IsRepositoryCloned checks if a repository is already cloned at the given path.
func (g *GitOperations) IsRepositoryCloned(path string) bool {
	gitDir := filepath.Join(path, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		return true
	}
	// Check for bare repository
	if _, err := os.Stat(filepath.Join(path, "HEAD")); err == nil {
		if _, err := os.Stat(filepath.Join(path, "config")); err == nil {
			return true
		}
	}
	return false
}

// GetGitExecutable returns the path to the git executable.
func GetGitExecutable() string {
	if runtime.GOOS == "windows" {
		// Try common git installation paths on Windows
		paths := []string{
			"git.exe",
			"C:\\Program Files\\Git\\cmd\\git.exe",
			"C:\\Program Files (x86)\\Git\\cmd\\git.exe",
		}
		for _, path := range paths {
			if _, err := exec.LookPath(path); err == nil {
				return path
			}
		}
		return "git.exe"
	}
	return "git"
}

// RunGitCommand runs a git command and returns the output.
func RunGitCommand(ctx context.Context, dir string, args ...string) (string, error) {
	// #nosec G204
	cmd := exec.CommandContext(ctx, GetGitExecutable(), args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// GetReferences returns all references in the repository at the given path.
func (g *GitOperations) GetReferences(ctx context.Context, path string) (map[string]string, error) {
	output, err := RunGitCommand(ctx, path, "show-ref")
	if err != nil {
		// An empty repository (no refs yet) makes show-ref exit with status 1
		// and no output. That case is benign; any other failure (e.g. invalid
		// repo, git error) must be surfaced so deleted-branch detection does
		// not silently treat a real error as "no branches".
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && strings.TrimSpace(output) == "" {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("failed to list references: %w (output: %s)", err, strings.TrimSpace(output))
	}

	refs := make(map[string]string)
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			sha := parts[0]
			refName := parts[1]
			refs[refName] = sha
		}
	}
	return refs, nil
}

// getPoolPath returns the pool path for a remote repository URL.
func (g *GitOperations) getPoolPath(url string) string {
	h := sha256.Sum256([]byte(url))
	hashStr := hex.EncodeToString(h[:])
	return filepath.Join(g.BasePath, "pools", hashStr[0:2], hashStr[2:4], hashStr)
}

// updatePool populates or updates the shared object pool for the remote URL.
func (g *GitOperations) updatePool(ctx context.Context, url string, auth transport.AuthMethod, progress sideband.Progress) (string, error) {
	poolPath := g.getPoolPath(url)

	var r *git.Repository
	var err error
	if _, err = os.Stat(poolPath); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(poolPath), 0o750); err != nil {
			return "", fmt.Errorf("failed to create pool directory: %w", err)
		}
		r, err = git.PlainInit(poolPath, true)
		if err != nil {
			return "", fmt.Errorf("failed to init pool repository: %w", err)
		}
		_, err = r.CreateRemote(&config.RemoteConfig{
			Name: "origin",
			URLs: []string{url},
		})
		if err != nil {
			return "", fmt.Errorf("failed to create pool remote: %w", err)
		}
	} else {
		r, err = git.PlainOpen(poolPath)
		if err != nil {
			return "", fmt.Errorf("failed to open pool repository: %w", err)
		}
	}

	fetchOpts := &git.FetchOptions{
		Progress: progress,
		Auth:     auth,
		RefSpecs: []config.RefSpec{
			"refs/heads/*:refs/heads/*",
			"refs/tags/*:refs/tags/*",
		},
		Force: true,
	}

	err = r.FetchContext(ctx, fetchOpts)
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return "", fmt.Errorf("pool fetch failed: %w", err)
	}

	return poolPath, nil
}

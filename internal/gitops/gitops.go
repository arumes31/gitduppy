package gitops

import (
	"context"
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
}

// CloneRepository clones a git repository.
func (g *GitOperations) CloneRepository(ctx context.Context, opts *CloneOptions) error {
	// Create target directory
	if err := os.MkdirAll(filepath.Dir(opts.Path), 0o750); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
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

	// Set authentication
	auth, err := g.buildAuth(opts)
	if err != nil {
		return err
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
	repo, err := git.PlainOpen(opts.Path)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	// Build fetch options
	fetchOpts := &git.FetchOptions{
		Progress: opts.Progress,
		Prune:    true,
	}

	auth, err := g.buildAuth(opts)
	if err != nil {
		return err
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
		// If there are no references yet, show-ref returns exit status 1
		return nil, nil
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

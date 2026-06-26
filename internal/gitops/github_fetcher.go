package gitops

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.uber.org/zap"
)

// GitHubMetadataFetcher is responsible for fetching GitHub-specific metadata.
type GitHubMetadataFetcher struct {
	logger *zap.Logger
}

// NewGitHubMetadataFetcher creates a new instance.
func NewGitHubMetadataFetcher() *GitHubMetadataFetcher {
	return &GitHubMetadataFetcher{
		logger: zap.L().Named("github-fetcher"),
	}
}

// FetchMetadata fetches all enabled metadata for a repository.
func (f *GitHubMetadataFetcher) FetchMetadata(ctx context.Context, repoURL, storagePath, token string, issues, prs, releases bool) error {
	owner, repoName, err := f.parseGitHubURL(repoURL)
	if err != nil {
		f.logger.Warn("Not a standard GitHub URL, skipping metadata fetch", zap.String("url", repoURL))
		return nil
	}

	backupDir := filepath.Join(storagePath, "github_backup")
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	if issues {
		if err := f.fetchAPI(ctx, owner, repoName, "issues?state=all", filepath.Join(backupDir, "issues.json"), token); err != nil {
			f.logger.Error("Failed to fetch issues", zap.Error(err))
		}
	}
	if prs {
		if err := f.fetchAPI(ctx, owner, repoName, "pulls?state=all", filepath.Join(backupDir, "pull_requests.json"), token); err != nil {
			f.logger.Error("Failed to fetch pull requests", zap.Error(err))
		}
	}
	if releases {
		if err := f.fetchAPI(ctx, owner, repoName, "releases", filepath.Join(backupDir, "releases.json"), token); err != nil {
			f.logger.Error("Failed to fetch releases", zap.Error(err))
		}
	}

	return nil
}

// fetchAPI performs the HTTP GET request to GitHub API and saves the response body to a file.
func (f *GitHubMetadataFetcher) fetchAPI(ctx context.Context, owner, repoName, endpoint, filePath, token string) error {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/%s", owner, repoName, endpoint)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API returned status: %d", resp.StatusCode)
	}

	out, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// parseGitHubURL extracts owner and repo name from a GitHub URL.
func (f *GitHubMetadataFetcher) parseGitHubURL(url string) (string, string, error) {
	// Matches: https://github.com/owner/repo.git or git@github.com:owner/repo.git
	url = strings.TrimSpace(url)

	// Remove .git suffix if present
	url = strings.TrimSuffix(url, ".git")

	httpRegex := regexp.MustCompile(`^https?://github\.com/([^/]+)/([^/]+)$`)
	if matches := httpRegex.FindStringSubmatch(url); len(matches) == 3 {
		return matches[1], matches[2], nil
	}

	sshRegex := regexp.MustCompile(`^git@github\.com:([^/]+)/([^/]+)$`)
	if matches := sshRegex.FindStringSubmatch(url); len(matches) == 3 {
		return matches[1], matches[2], nil
	}

	return "", "", fmt.Errorf("invalid github url format")
}

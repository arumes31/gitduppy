package gitops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
)

// GitHubMetadataFetcher is responsible for fetching GitHub-specific metadata.
type GitHubMetadataFetcher struct {
	logger *zap.Logger
	client *http.Client
}

// NewGitHubMetadataFetcher creates a new instance.
func NewGitHubMetadataFetcher() *GitHubMetadataFetcher {
	return &GitHubMetadataFetcher{
		logger: zap.L().Named("github-fetcher"),
		client: &http.Client{Timeout: 60 * time.Second},
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
		// When both issues and PRs are mirrored, filter out PRs from /issues
		// (GitHub's /issues endpoint returns PRs too via the pull_request key).
		filterPRs := prs
		if err := f.fetchPaginatedJSON(ctx, owner, repoName, "issues?state=all&per_page=100", filepath.Join(backupDir, "issues.json"), token, filterPRs); err != nil {
			f.logger.Error("Failed to fetch issues", zap.Error(err))
		}
	}
	if prs {
		if err := f.fetchPaginatedJSON(ctx, owner, repoName, "pulls?state=all&per_page=100", filepath.Join(backupDir, "pull_requests.json"), token, false); err != nil {
			f.logger.Error("Failed to fetch pull requests", zap.Error(err))
		}
	}
	if releases {
		if err := f.fetchPaginatedJSON(ctx, owner, repoName, "releases?per_page=100", filepath.Join(backupDir, "releases.json"), token, false); err != nil {
			f.logger.Error("Failed to fetch releases", zap.Error(err))
		}
	}

	return nil
}

// FetchRepositoryInfo fetches the description and topics for a GitHub repository.
func (f *GitHubMetadataFetcher) FetchRepositoryInfo(ctx context.Context, repoURL, token string) (string, []string, error) {
	owner, repoName, err := f.parseGitHubURL(repoURL)
	if err != nil {
		return "", nil, fmt.Errorf("not a github url")
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repoName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", nil, err
	}

	// We need the mercy-preview header to get topics, though it's standard in v3 now.
	req.Header.Set("Accept", "application/vnd.github.mercy-preview+json")
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("GitHub API returned status: %d", resp.StatusCode)
	}

	var data struct {
		Description string   `json:"description"`
		Topics      []string `json:"topics"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", nil, fmt.Errorf("failed to decode repo info: %w", err)
	}

	return data.Description, data.Topics, nil
}

// fetchPaginatedJSON fetches all pages from a GitHub API list endpoint and
// writes the combined results to filePath as a JSON array.
// If filterPRs is true, items with a "pull_request" key are excluded (used to
// de-duplicate when mirroring both issues and PRs).
func (f *GitHubMetadataFetcher) fetchPaginatedJSON(ctx context.Context, owner, repoName, endpoint, filePath, token string, filterPRs bool) error {
	var allItems []json.RawMessage

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/%s", owner, repoName, endpoint)

	for apiURL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
		if err != nil {
			return err
		}

		req.Header.Set("Accept", "application/vnd.github.v3+json")
		if token != "" {
			req.Header.Set("Authorization", "token "+token)
		}

		resp, err := f.client.Do(req)
		if err != nil {
			return err
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("GitHub API returned status: %d", resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}

		var page []json.RawMessage
		if err := json.Unmarshal(body, &page); err != nil {
			return fmt.Errorf("failed to decode page: %w", err)
		}

		if filterPRs {
			for _, raw := range page {
				var obj map[string]json.RawMessage
				if err := json.Unmarshal(raw, &obj); err == nil {
					if _, hasPR := obj["pull_request"]; hasPR {
						continue // skip PRs from /issues
					}
				}
				allItems = append(allItems, raw)
			}
		} else {
			allItems = append(allItems, page...)
		}

		// Follow pagination via Link header.
		apiURL = parseNextLink(resp.Header.Get("Link"))
	}

	data, err := json.MarshalIndent(allItems, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filePath, data, 0o640)
}

// parseNextLink extracts the "next" URL from a GitHub Link header.
func parseNextLink(linkHeader string) string {
	if linkHeader == "" {
		return ""
	}
	for _, part := range strings.Split(linkHeader, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, `rel="next"`) {
			start := strings.Index(part, "<")
			end := strings.Index(part, ">")
			if start >= 0 && end > start {
				return part[start+1 : end]
			}
		}
	}
	return ""
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

package gitops

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// maxMediaBytes caps the size of any single archived media asset to keep
// disk usage bounded when mirroring issue/PR attachments.
const maxMediaBytes = 100 << 20 // 100 MB

type cacheEntry struct {
	value      interface{}
	expiration time.Time
}

// sharedGitHubClient is reused across all fetcher instances so that per-job
// fetchers share one connection pool (keep-alives, bounded idle conns) instead
// of each spinning up a fresh client and dialing new TCP/TLS connections.
//
//nolint:gochecknoglobals
var sharedGitHubClient = &http.Client{
	Timeout: 60 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	},
}

// GitHubMetadataFetcher is responsible for fetching GitHub-specific metadata.
type GitHubMetadataFetcher struct {
	logger *zap.Logger
	client *http.Client
	cache  sync.Map
}

// NewGitHubMetadataFetcher creates a new instance backed by the shared,
// connection-pooling HTTP client.
func NewGitHubMetadataFetcher() *GitHubMetadataFetcher {
	return &GitHubMetadataFetcher{
		logger: zap.L().Named("github-fetcher"),
		client: sharedGitHubClient,
	}
}

// FetchMetadata fetches all enabled metadata for a repository.
func (f *GitHubMetadataFetcher) FetchMetadata(ctx context.Context, repoURL, storagePath, token string, issues, prs, releases bool) error {
	owner, repoName, err := f.parseGitHubURL(repoURL)
	if err != nil {
		f.logger.Warn("Not a standard GitHub URL, skipping metadata fetch", zap.String("url", repoURL))
		return nil //nolint:nilerr
	}

	backupDir := filepath.Join(storagePath, "github_backup")
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	var errs []error
	if issues {
		// When both issues and PRs are mirrored, filter out PRs from /issues
		// (GitHub's /issues endpoint returns PRs too via the pull_request key).
		filterPRs := prs
		if err := f.fetchPaginatedJSON(ctx, owner, repoName, "issues?state=all&per_page=100", filepath.Join(backupDir, "issues.json"), token, filterPRs); err != nil {
			f.logger.Error("Failed to fetch issues", zap.Error(err))
			errs = append(errs, fmt.Errorf("failed to fetch issues: %w", err))
		}
	}
	if prs {
		if err := f.fetchPaginatedJSON(ctx, owner, repoName, "pulls?state=all&per_page=100", filepath.Join(backupDir, "pull_requests.json"), token, false); err != nil {
			f.logger.Error("Failed to fetch pull requests", zap.Error(err))
			errs = append(errs, fmt.Errorf("failed to fetch pull requests: %w", err))
		}
	}
	if releases {
		if err := f.fetchPaginatedJSON(ctx, owner, repoName, "releases?per_page=100", filepath.Join(backupDir, "releases.json"), token, false); err != nil {
			f.logger.Error("Failed to fetch releases", zap.Error(err))
			errs = append(errs, fmt.Errorf("failed to fetch releases: %w", err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// RepositoryInfo holds the GitHub metadata fetched for a repository.
type RepositoryInfo struct {
	Description string
	Topics      []string
	Visibility  string // "public" or "private"
}

func (f *GitHubMetadataFetcher) getCache(key string) (interface{}, bool) {
	val, ok := f.cache.Load(key)
	if !ok {
		return nil, false
	}
	entry := val.(cacheEntry)
	if time.Now().After(entry.expiration) {
		f.cache.Delete(key)
		return nil, false
	}
	return entry.value, true
}

func (f *GitHubMetadataFetcher) setCache(key string, val interface{}, ttl time.Duration) {
	f.cache.Store(key, cacheEntry{
		value:      val,
		expiration: time.Now().Add(ttl),
	})
}

// FetchRepositoryInfo fetches the description, topics and visibility for a GitHub repository.
func (f *GitHubMetadataFetcher) FetchRepositoryInfo(ctx context.Context, repoURL, token string) (*RepositoryInfo, error) {
	owner, repoName, err := f.parseGitHubURL(repoURL)
	if err != nil {
		return nil, fmt.Errorf("not a github url")
	}

	// Include the auth scope in the cache key so private metadata fetched with
	// one token is never served to a caller with a different or missing token.
	tokenScope := "anon"
	if token != "" {
		h := fnv.New64a()
		_, _ = h.Write([]byte(token))
		tokenScope = fmt.Sprintf("%x", h.Sum64())
	}
	cacheKey := "repo_info:" + tokenScope + ":" + owner + "/" + repoName
	if cached, ok := f.getCache(cacheKey); ok {
		return cached.(*RepositoryInfo), nil
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repoName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}

	// We need the mercy-preview header to get topics, though it's standard in v3 now.
	req.Header.Set("Accept", "application/vnd.github.mercy-preview+json")
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status: %d", resp.StatusCode)
	}

	var data struct {
		Description string   `json:"description"`
		Topics      []string `json:"topics"`
		Private     bool     `json:"private"`
		Visibility  string   `json:"visibility"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode repo info: %w", err)
	}

	// Normalize visibility. GitHub returns a "visibility" string ("public"/"private"/"internal")
	// and a boolean "private"; fall back to the boolean when the string is empty.
	visibility := data.Visibility
	switch visibility {
	case "":
		if data.Private {
			visibility = "private"
		} else {
			visibility = "public"
		}
	case "internal":
		// Treat enterprise "internal" repos as private for display purposes.
		visibility = "private"
	}

	repoInfo := &RepositoryInfo{
		Description: data.Description,
		Topics:      data.Topics,
		Visibility:  visibility,
	}

	f.setCache(cacheKey, repoInfo, 1*time.Hour)
	return repoInfo, nil
}

// fetchPaginatedJSON fetches all pages from a GitHub API list endpoint and
// writes the combined results to filePath as a JSON array.
// If filterPRs is true, items with a "pull_request" key are excluded (used to
// de-duplicate when mirroring both issues and PRs).
func (f *GitHubMetadataFetcher) fetchPaginatedJSON(ctx context.Context, owner, repoName, endpoint, filePath, token string, filterPRs bool) error {
	var allItems []json.RawMessage

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/%s", owner, repoName, endpoint)

	for apiURL != "" {
		// Add a micro-sleep to avoid hitting secondary rate limits (abuse detection)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}

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

		// Handle Rate Limiting (403 Forbidden or 429 Too Many Requests)
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			resetHeader := resp.Header.Get("X-RateLimit-Reset")
			remainingHeader := resp.Header.Get("X-RateLimit-Remaining")

			if resetHeader != "" && remainingHeader == "0" {
				if resetUnix, parseErr := strconv.ParseInt(resetHeader, 10, 64); parseErr == nil {
					resetTime := time.Unix(resetUnix, 0)
					sleepDuration := time.Until(resetTime) + 2*time.Second
					if sleepDuration > 0 && sleepDuration < 5*time.Minute {
						f.logger.Warn("GitHub rate limit exceeded. Backing off and sleeping...",
							zap.Duration("duration", sleepDuration),
							zap.Time("reset_time", resetTime))
						resp.Body.Close()

						select {
						case <-ctx.Done():
							return ctx.Err()
						case <-time.After(sleepDuration):
							continue // Retry the same request
						}
					} else {
						resp.Body.Close()
						return fmt.Errorf("GitHub rate limit exceeded. Reset at %s, aborting fetch", resetTime)
					}
				}
			}
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("GitHub API returned status: %d", resp.StatusCode)
		}

		// Check for proactive rate limit sleep if remaining is extremely low
		remainingHeader := resp.Header.Get("X-RateLimit-Remaining")
		if remainingHeader == "1" || remainingHeader == "2" {
			resetHeader := resp.Header.Get("X-RateLimit-Reset")
			if resetHeader != "" {
				if resetUnix, parseErr := strconv.ParseInt(resetHeader, 10, 64); parseErr == nil {
					resetTime := time.Unix(resetUnix, 0)
					sleepDuration := time.Until(resetTime) + 2*time.Second
					if sleepDuration > 0 && sleepDuration < 5*time.Minute {
						f.logger.Info("GitHub rate limit near depletion. Proactively sleeping until reset...",
							zap.Duration("duration", sleepDuration))
						select {
						case <-ctx.Done():
							resp.Body.Close()
							return ctx.Err()
						case <-time.After(sleepDuration):
						}
					}
				}
			}
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

	// Archive media and rewrite remote URLs to local path
	data = f.archiveMedia(ctx, filepath.Dir(filePath), token, data)

	return os.WriteFile(filePath, data, 0o600)
}

// archiveMedia scans JSON bytes for external GitHub media URLs, downloads them, and rewrites URLs locally.
func (f *GitHubMetadataFetcher) archiveMedia(ctx context.Context, backupDir string, token string, jsonBytes []byte) []byte {
	mediaDir := filepath.Join(backupDir, "media")

	// Match user attachments, images, and raw user content domains from GitHub
	// CodeQL [go/regex/missing-regexp-anchor] - This regex is used for extracting media URLs from JSON payloads for archiving, not for validation or access control. Anchors are not appropriate here.
	re := regexp.MustCompile(`https?://(?:github\.com/assets/|github\.com/user-attachments/assets/|[\w.-]+\.githubusercontent\.com/[a-zA-Z0-9-_./~%#?&=]+)`)
	matches := re.FindAll(jsonBytes, -1)
	if len(matches) == 0 {
		return jsonBytes
	}

	// Deduplicate matches
	uniqueURLs := make(map[string]bool)
	for _, match := range matches {
		uniqueURLs[string(match)] = true
	}

	// Create media directory
	if err := os.MkdirAll(mediaDir, 0o750); err != nil {
		f.logger.Error("Failed to create media directory", zap.Error(err))
		return jsonBytes
	}

	jsonStr := string(jsonBytes)

	for urlStr := range uniqueURLs {
		// Check context cancellation
		if ctx.Err() != nil {
			break
		}

		// Clean URL from escaped quotes if matched inside JSON string raw matches
		cleanURL := strings.ReplaceAll(urlStr, `\"`, "")
		cleanURL = strings.Trim(cleanURL, `"'`)

		// Determine filename
		hash := sha256.Sum256([]byte(cleanURL))
		hexHash := fmt.Sprintf("%x", hash[:8]) // Keep it short and clean

		ext := filepath.Ext(cleanURL)
		if ext == "" {
			ext = ".png" // Default fallback
		} else {
			// Strip query parameters
			if idx := strings.Index(ext, "?"); idx >= 0 {
				ext = ext[:idx]
			}
			// Strip any non-alphanumeric chars at the end of extension
			extClean := ""
			for _, char := range ext {
				if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' {
					extClean += string(char)
				}
			}
			ext = extClean
			if len(ext) > 5 {
				ext = ".png"
			}
		}
		filename := fmt.Sprintf("media-%s%s", hexHash, ext)
		destPath := filepath.Join(mediaDir, filename)

		// Download if it doesn't exist
		if _, err := os.Stat(destPath); os.IsNotExist(err) {
			f.logger.Info("Archiving external media asset", zap.String("url", cleanURL))

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, cleanURL, nil)
			if err == nil {
				if token != "" {
					req.Header.Set("Authorization", "token "+token)
				}
				resp, err := f.client.Do(req)
				if err == nil {
					if resp.StatusCode == http.StatusOK {
						switch {
						case resp.ContentLength > maxMediaBytes:
							f.logger.Warn("skipping oversized media asset", zap.String("url", cleanURL), zap.Int64("content_length", resp.ContentLength))
						default:
							outFile, createErr := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
							if createErr == nil {
								// Cap the bytes written so a huge or unknown-length
								// asset cannot grow disk usage without bound.
								written, copyErr := io.Copy(outFile, io.LimitReader(resp.Body, maxMediaBytes+1))
								closeErr := outFile.Close()
								if copyErr != nil || written > maxMediaBytes || closeErr != nil {
									// Partial/oversized download — discard it.
									_ = os.Remove(destPath)
									f.logger.Warn("media asset exceeded size cap or failed, discarded", zap.String("url", cleanURL))
								}
							}
						}
					}
					resp.Body.Close()
				}
			}
		}

		// If download succeeded (or already existed), replace URL in JSON.
		// Use a path relative to the metadata JSON file (which lives in the
		// github_backup dir alongside the media/ subdirectory) so the rewritten
		// URL resolves to the archived asset regardless of how it is served.
		if _, err := os.Stat(destPath); err == nil {
			localURL := "media/" + filename
			jsonStr = strings.ReplaceAll(jsonStr, urlStr, localURL)
		}
	}

	return []byte(jsonStr)
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

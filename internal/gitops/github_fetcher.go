package gitops

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// maxMediaBytes caps the size of any single archived media asset to keep
// disk usage bounded when mirroring issue/PR attachments.
const maxMediaBytes = 100 << 20 // 100 MB

// Per-sync media budgets. A single metadata sync (issues + PRs + releases)
// stops starting new media downloads once either is exhausted, so attacker-
// controlled issue bodies cannot make one sync fetch an unbounded number of
// assets or bytes. Bytes read from failed or discarded downloads count too.
const (
	maxMediaDownloadsPerSync       = 500
	maxMediaBytesPerSync     int64 = 1 << 30 // 1 GiB
)

// backupDirName is the directory, inside the repository checkout, that holds
// the archived GitHub metadata JSON and its media/ subdirectory.
const backupDirName = "github_backup"

type cacheEntry struct {
	value      any
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

	// The backup lives inside the checkout, whose contents the upstream
	// controls: a committed symlink at github_backup/ (or at any file name
	// written below) must not redirect these writes elsewhere on disk. All
	// filesystem access therefore goes through an os.Root anchored at the
	// repository, which refuses to resolve any path that escapes it, and the
	// backup directory itself must be a real directory, not a link.
	backupRoot, err := openBackupRoot(storagePath)
	if err != nil {
		return fmt.Errorf("failed to open backup directory: %w", err)
	}
	defer backupRoot.Close()

	budget := &mediaBudget{downloadsLeft: maxMediaDownloadsPerSync, bytesLeft: maxMediaBytesPerSync}

	var errs []error
	if issues {
		// When both issues and PRs are mirrored, filter out PRs from /issues
		// (GitHub's /issues endpoint returns PRs too via the pull_request key).
		filterPRs := prs
		if err := f.fetchPaginatedJSON(ctx, owner, repoName, "issues?state=all&per_page=100", backupRoot, "issues.json", token, filterPRs, budget); err != nil {
			f.logger.Error("Failed to fetch issues", zap.Error(err))
			errs = append(errs, fmt.Errorf("failed to fetch issues: %w", err))
		}
	}
	if prs {
		if err := f.fetchPaginatedJSON(ctx, owner, repoName, "pulls?state=all&per_page=100", backupRoot, "pull_requests.json", token, false, budget); err != nil {
			f.logger.Error("Failed to fetch pull requests", zap.Error(err))
			errs = append(errs, fmt.Errorf("failed to fetch pull requests: %w", err))
		}
	}
	if releases {
		if err := f.fetchPaginatedJSON(ctx, owner, repoName, "releases?per_page=100", backupRoot, "releases.json", token, false, budget); err != nil {
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

func (f *GitHubMetadataFetcher) getCache(key string) (any, bool) {
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

func (f *GitHubMetadataFetcher) setCache(key string, val any, ttl time.Duration) {
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
	recordGitHubRateLimit(resp)

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
// writes the combined results to fileName (inside backupRoot) as a JSON array.
// If filterPRs is true, items with a "pull_request" key are excluded (used to
// de-duplicate when mirroring both issues and PRs).
func (f *GitHubMetadataFetcher) fetchPaginatedJSON(ctx context.Context, owner, repoName, endpoint string, backupRoot *os.Root, fileName, token string, filterPRs bool, budget *mediaBudget) error {
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
		recordGitHubRateLimit(resp)

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
						_ = resp.Body.Close()

						select {
						case <-ctx.Done():
							return ctx.Err()
						case <-time.After(sleepDuration):
							continue // Retry the same request
						}
					} else {
						_ = resp.Body.Close()
						return fmt.Errorf("GitHub rate limit exceeded. Reset at %s, aborting fetch", resetTime)
					}
				}
			}
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
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
							_ = resp.Body.Close()
							return ctx.Err()
						case <-time.After(sleepDuration):
						}
					}
				}
			}
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
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
	data = f.archiveMedia(ctx, backupRoot, token, data, budget)

	return writeFileReplacing(backupRoot, fileName, data)
}

// mediaBudget tracks how many media downloads and bytes a single metadata sync
// may still spend.
type mediaBudget struct {
	downloadsLeft int
	bytesLeft     int64
}

// openBackupRoot returns an os.Root for <storagePath>/github_backup, creating
// the directory if needed. It refuses a backup path that is a symlink or not a
// directory.
func openBackupRoot(storagePath string) (*os.Root, error) {
	repoRoot, err := os.OpenRoot(storagePath)
	if err != nil {
		return nil, err
	}
	defer repoRoot.Close()
	return openRealSubdir(repoRoot, backupDirName)
}

// openRealSubdir opens (creating if absent) the directory name directly under
// parent as its own os.Root, refusing it if it exists as a symlink or as
// anything other than a directory. os.Root already keeps every access inside
// parent; the explicit Lstat additionally stops an in-tree symlink from
// redirecting writes into some other part of the checkout.
func openRealSubdir(parent *os.Root, name string) (*os.Root, error) {
	if err := parent.Mkdir(name, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	fi, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing to use %s: it is a symlink", name)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("refusing to use %s: not a directory", name)
	}
	return parent.OpenRoot(name)
}

// writeFileReplacing writes data to name inside root without ever following a
// pre-existing file there: the bytes go to a freshly created (O_EXCL) temp file
// that is then renamed over name, so a symlink planted at name is replaced
// rather than written through.
func writeFileReplacing(root *os.Root, name string, data []byte) error {
	tmp, tmpName, err := createTempIn(root, name)
	if err != nil {
		return err
	}
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		_ = root.Remove(tmpName)
		return errors.Join(writeErr, closeErr)
	}
	return renameReplacing(root, tmpName, name)
}

// createTempIn creates a new, exclusively-owned temp file next to name inside
// root. O_EXCL means it fails rather than opening anything that already exists
// (including a symlink) at the chosen name.
func createTempIn(root *os.Root, name string) (*os.File, string, error) {
	var lastErr error
	for range 8 {
		var rnd [8]byte
		if _, err := rand.Read(rnd[:]); err != nil {
			return nil, "", err
		}
		tmpName := fmt.Sprintf(".%s.tmp-%x", name, rnd)
		file, err := root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return file, tmpName, nil
		}
		lastErr = err
		if !errors.Is(err, os.ErrExist) {
			break
		}
	}
	return nil, "", lastErr
}

// renameReplacing moves tmpName over name inside root. A symlink already at
// name is removed first (Remove deletes the link itself, never its target) so
// the rename can never be redirected through it on any platform.
func renameReplacing(root *os.Root, tmpName, name string) error {
	if fi, err := root.Lstat(name); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if err := root.Remove(name); err != nil {
			_ = root.Remove(tmpName)
			return err
		}
	}
	if err := root.Rename(tmpName, name); err != nil {
		_ = root.Remove(tmpName)
		return err
	}
	return nil
}

// candidateMediaURLRe finds URL-shaped tokens in a JSON payload. It deliberately
// does NOT try to validate the host in the pattern itself — host allow-listing is
// done programmatically in isArchivableGitHubMediaURL. A host-matching regex here
// would be brittle and unanchored (the go/regex/missing-regexp-anchor hazard),
// and host validation matters for security below, so it must not live in a regex.
var candidateMediaURLRe = regexp.MustCompile(`https?://[^\s"'<>\\)]+`)

// isArchivableGitHubMediaURL reports whether raw points at first-party GitHub
// media that is safe to fetch with the caller's token. This is a security
// control, not just tidiness: archiveMedia sends the GitHub token in the
// Authorization header, so a URL on an attacker-influenced host (media links are
// taken from issue/PR JSON that users control) could exfiltrate that token. Only
// exact github.com asset paths and the *.githubusercontent.com CDN are allowed.
func isArchivableGitHubMediaURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	// Require HTTPS: the Authorization token is attached to this request, so it
	// must never travel over cleartext HTTP. First-party GitHub media is always
	// served over HTTPS anyway.
	if u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "github.com" {
		return strings.HasPrefix(u.Path, "/assets/") || strings.HasPrefix(u.Path, "/user-attachments/assets/")
	}
	return host == "githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com")
}

// normalizeMediaURL returns the URL actually fetched for a media reference: the
// same URL with any fragment removed. Fragments are never sent to the server,
// so "x.png#a" and "x.png#b" are the same asset and must share one download
// and one archived file.
func normalizeMediaURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	u.Fragment = ""
	u.RawFragment = ""
	return u.String(), true
}

// mediaFilename derives the archived file name for a (normalized) media URL:
// a short sha256 of the URL plus a scrubbed extension taken from the URL path.
func mediaFilename(fetchURL string) string {
	hash := sha256.Sum256([]byte(fetchURL))
	hexHash := fmt.Sprintf("%x", hash[:8]) // Keep it short and clean

	ext := ""
	if u, err := url.Parse(fetchURL); err == nil {
		ext = path.Ext(u.Path)
	}
	// Keep only alphanumerics and dots, and fall back to .png for anything
	// missing or implausibly long.
	extClean := ""
	for _, char := range ext {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' {
			extClean += string(char)
		}
	}
	if extClean == "" || extClean == "." || len(extClean) > 5 {
		extClean = ".png"
	}
	return fmt.Sprintf("media-%s%s", hexHash, extClean)
}

// archiveMedia scans JSON bytes for external GitHub media URLs, downloads them, and rewrites URLs locally.
func (f *GitHubMetadataFetcher) archiveMedia(ctx context.Context, backupRoot *os.Root, token string, jsonBytes []byte, budget *mediaBudget) []byte {
	matches := candidateMediaURLRe.FindAll(jsonBytes, -1)
	if len(matches) == 0 {
		return jsonBytes
	}

	// Deduplicate, keeping only first-party GitHub media hosts. The map is keyed by
	// the raw matched token (what we replace in the JSON) and holds the
	// normalized, fragment-free URL (what we fetch and hash). Off-allowlist hosts
	// are dropped here so the token below is never sent to a user-influenced host.
	uniqueURLs := make(map[string]string)
	for _, match := range matches {
		raw := string(match)
		cleanURL := strings.Trim(strings.ReplaceAll(raw, `\"`, ""), `"'`)
		if !isArchivableGitHubMediaURL(cleanURL) {
			continue
		}
		if fetchURL, ok := normalizeMediaURL(cleanURL); ok {
			uniqueURLs[raw] = fetchURL
		}
	}
	if len(uniqueURLs) == 0 {
		return jsonBytes
	}

	mediaRoot, err := openRealSubdir(backupRoot, "media")
	if err != nil {
		f.logger.Error("Failed to open media directory", zap.Error(err))
		return jsonBytes
	}
	defer mediaRoot.Close()

	jsonStr := string(jsonBytes)

	// Replace longer URLs before shorter ones. When one captured URL is a strict
	// prefix of another, a naive map-iteration order could rewrite the shorter URL
	// first and corrupt the longer one, so process keys sorted by descending length
	// (lexicographic tie-break for determinism).
	sortedURLs := make([]string, 0, len(uniqueURLs))
	for urlStr := range uniqueURLs {
		sortedURLs = append(sortedURLs, urlStr)
	}
	sort.Slice(sortedURLs, func(i, j int) bool {
		if len(sortedURLs[i]) != len(sortedURLs[j]) {
			return len(sortedURLs[i]) > len(sortedURLs[j])
		}
		return sortedURLs[i] < sortedURLs[j]
	})

	// Outcome per normalized URL, so every raw variant of one asset (differing
	// only in fragment) triggers at most one download attempt.
	archived := make(map[string]bool, len(uniqueURLs))
	for _, urlStr := range sortedURLs {
		if ctx.Err() != nil {
			break
		}
		fetchURL := uniqueURLs[urlStr]
		filename := mediaFilename(fetchURL)

		ok, seen := archived[fetchURL]
		if !seen {
			ok = f.ensureMediaAsset(ctx, mediaRoot, filename, fetchURL, token, budget)
			archived[fetchURL] = ok
		}

		// If download succeeded (or already existed), replace URL in JSON.
		// Use a path relative to the metadata JSON file (which lives in the
		// github_backup dir alongside the media/ subdirectory) so the rewritten
		// URL resolves to the archived asset regardless of how it is served.
		if ok {
			jsonStr = strings.ReplaceAll(jsonStr, urlStr, "media/"+filename)
		}
	}

	return []byte(jsonStr)
}

// ensureMediaAsset makes sure filename in mediaRoot holds the asset at
// fetchURL, downloading it if needed (within budget). It reports whether a
// regular file is in place afterwards.
func (f *GitHubMetadataFetcher) ensureMediaAsset(ctx context.Context, mediaRoot *os.Root, filename, fetchURL, token string, budget *mediaBudget) bool {
	if fi, err := mediaRoot.Lstat(filename); err == nil {
		if fi.Mode().IsRegular() {
			return true
		}
		// A symlink (e.g. planted by the upstream checkout) is never used or
		// written through; drop the link itself and re-download. Any other
		// non-regular entry is left alone and the remote URL kept.
		if fi.Mode()&os.ModeSymlink == 0 || mediaRoot.Remove(filename) != nil {
			return false
		}
	}

	if budget.downloadsLeft <= 0 || budget.bytesLeft <= 0 {
		f.logger.Debug("media archive budget exhausted for this sync, leaving remote URL", zap.String("url", fetchURL))
		return false
	}
	budget.downloadsLeft--

	f.logger.Info("Archiving external media asset", zap.String("url", fetchURL))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		return false
	}
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}

	limit := min(int64(maxMediaBytes), budget.bytesLeft)
	if resp.ContentLength > limit {
		f.logger.Warn("skipping oversized media asset", zap.String("url", fetchURL), zap.Int64("content_length", resp.ContentLength))
		return false
	}

	tmp, tmpName, err := createTempIn(mediaRoot, filename)
	if err != nil {
		return false
	}
	// Cap the bytes written so a huge or unknown-length asset cannot grow disk
	// usage without bound; everything read counts against the sync budget,
	// whether or not the file is kept.
	written, copyErr := io.Copy(tmp, io.LimitReader(resp.Body, limit+1))
	closeErr := tmp.Close()
	budget.bytesLeft -= written
	if copyErr != nil || closeErr != nil || written > limit {
		// Partial/oversized download — discard it.
		_ = mediaRoot.Remove(tmpName)
		f.logger.Warn("media asset exceeded size cap or failed, discarded", zap.String("url", fetchURL))
		return false
	}
	return renameReplacing(mediaRoot, tmpName, filename) == nil
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

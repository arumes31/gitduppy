package handlers

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gitduppy/gitduppy/internal/gitops"
	"github.com/gitduppy/gitduppy/internal/models"
	"github.com/gitduppy/gitduppy/internal/services"
	"github.com/gitduppy/gitduppy/pkg/response"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/uuid"
)

// BrowseHandler handles git repository browsing requests.
type BrowseHandler struct {
	repoService *services.RepositoryService
}

// NewBrowseHandler creates a new browse handler.
func NewBrowseHandler(repoService *services.RepositoryService) *BrowseHandler {
	return &BrowseHandler{repoService: repoService}
}

// TreeEntry represents a file or directory in the tree.
type TreeEntry struct {
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Type        string    `json:"type"` // "blob" or "tree"
	Size        int64     `json:"size,omitempty"`
	LastCommit  string    `json:"last_commit,omitempty"`
	LastMessage string    `json:"last_message,omitempty"`
	LastAuthor  string    `json:"last_author,omitempty"`
	LastDate    time.Time `json:"last_date,omitempty"`
}

// CommitEntry represents a commit in the history.
type CommitEntry struct {
	SHA         string    `json:"sha"`
	ShortSHA    string    `json:"short_sha"`
	Message     string    `json:"message"`
	Author      string    `json:"author"`
	AuthorEmail string    `json:"author_email"`
	Date        time.Time `json:"date"`
}

// RefEntry represents a branch or tag.
type RefEntry struct {
	Name string `json:"name"`
	Type string `json:"type"` // "branch" or "tag"
	SHA  string `json:"sha"`
}

// openRepo opens the go-git repo at the repository's storage path.
func (h *BrowseHandler) openRepo(c *gin.Context) (*gogit.Repository, *models.Repository, error) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return nil, nil, fmt.Errorf("invalid id")
	}
	repo, err := h.repoService.GetRepositoryByID(c, id)
	if err != nil {
		return nil, nil, fmt.Errorf("repository not found")
	}
	gitRepo, err := gogit.PlainOpen(repo.StoragePath)
	if err != nil {
		// Deliberately drop the underlying go-git error: it embeds the on-disk
		// storage path. The message stays user-actionable without leaking internals.
		return nil, nil, fmt.Errorf("repository not cloned yet or path invalid")
	}
	return gitRepo, repo, nil
}

// resolveRef resolves a ref string (branch/tag/commit SHA) to a commit hash.
func resolveRef(gitRepo *gogit.Repository, ref string) (*plumbing.Hash, error) {
	if ref == "" || ref == "HEAD" {
		if head, err := gitRepo.Head(); err == nil {
			h := head.Hash()
			return &h, nil
		}
		// HEAD is unavailable: bare mirrors are frequently cloned without a
		// worktree HEAD, and detached/empty HEAD states also fail here. Fall back
		// to the first branch (preferring the conventional default names) so
		// browsing still works instead of erroring. Only error when the repository
		// genuinely has no branches.
		if h, ok := firstBranchHash(gitRepo); ok {
			return &h, nil
		}
		return nil, fmt.Errorf("repository has no HEAD and no branches")
	}
	// Try branch
	if branchRef, err := gitRepo.Reference(plumbing.NewBranchReferenceName(ref), true); err == nil {
		h := branchRef.Hash()
		return &h, nil
	}
	// Try tag
	if tagRef, err := gitRepo.Reference(plumbing.NewTagReferenceName(ref), true); err == nil {
		h := tagRef.Hash()
		return &h, nil
	}
	// Try as commit SHA
	h := plumbing.NewHash(ref)
	if !h.IsZero() {
		return &h, nil
	}
	return nil, fmt.Errorf("ref %q not found", ref)
}

// firstBranchHash returns the hash of a fallback branch when HEAD is unresolvable.
// It prefers the conventional default branches ('main', then 'master') and
// otherwise returns the first branch iterated. ok is false only when the
// repository has no branches at all.
func firstBranchHash(gitRepo *gogit.Repository) (plumbing.Hash, bool) {
	iter, err := gitRepo.Branches()
	if err != nil {
		return plumbing.Hash{}, false
	}
	defer iter.Close()

	var first, mainRef, masterRef *plumbing.Reference
	_ = iter.ForEach(func(ref *plumbing.Reference) error {
		if first == nil {
			first = ref
		}
		switch ref.Name().Short() {
		case "main":
			mainRef = ref
		case "master":
			masterRef = ref
		}
		return nil
	})

	switch {
	case mainRef != nil:
		return mainRef.Hash(), true
	case masterRef != nil:
		return masterRef.Hash(), true
	case first != nil:
		return first.Hash(), true
	default:
		return plumbing.Hash{}, false
	}
}

// GetRefs handles GET /api/v1/repos/:id/refs
func (h *BrowseHandler) GetRefs(c *gin.Context) {
	gitRepo, _, err := h.openRepo(c)
	if err != nil {
		response.NotFound(c, err.Error())
		return
	}

	var refs []RefEntry

	// List branches
	if branches, berr := gitRepo.Branches(); berr == nil {
		_ = branches.ForEach(func(ref *plumbing.Reference) error {
			refs = append(refs, RefEntry{
				Name: ref.Name().Short(),
				Type: "branch",
				SHA:  ref.Hash().String(),
			})
			return nil
		})
	}

	// List tags
	if tags, terr := gitRepo.Tags(); terr == nil {
		_ = tags.ForEach(func(ref *plumbing.Reference) error {
			refs = append(refs, RefEntry{
				Name: ref.Name().Short(),
				Type: "tag",
				SHA:  ref.Hash().String(),
			})
			return nil
		})
	}

	response.Success(c, refs)
}

// GetTree handles GET /api/v1/repos/:id/tree?ref=main&path=src/
// Uses git CLI for speed — the pure go-git Stats() implementation is prohibitively slow.
func (h *BrowseHandler) GetTree(c *gin.Context) {
	gitRepo, repo, err := h.openRepo(c)
	if err != nil {
		response.NotFound(c, err.Error())
		return
	}

	ref := c.Query("ref")
	treePath := c.Query("path")

	hash, err := resolveRef(gitRepo, ref)
	if err != nil {
		response.BadRequest(c, "INVALID_REF", err.Error())
		return
	}

	commit, err := gitRepo.CommitObject(*hash)
	if err != nil {
		logServerError(c, err)
		response.InternalError(c, "Failed to read commit")
		return
	}

	tree, err := commit.Tree()
	if err != nil {
		logServerError(c, err)
		response.InternalError(c, "Failed to read tree")
		return
	}

	// Navigate into subdirectory if path specified
	if treePath != "" && treePath != "/" {
		treePath = strings.Trim(treePath, "/")
		subTree, terr := tree.Tree(treePath)
		if terr != nil {
			response.NotFound(c, "Path not found: "+treePath)
			return
		}
		tree = subTree
	} else {
		treePath = ""
	}

	var dirs, files []TreeEntry

	for _, entry := range tree.Entries {
		fullPath := entry.Name
		if treePath != "" {
			fullPath = treePath + "/" + entry.Name
		}
		te := TreeEntry{
			Name: entry.Name,
			Path: fullPath,
		}
		if entry.Mode.IsFile() {
			te.Type = "blob"
			if blob, berr := gitRepo.BlobObject(entry.Hash); berr == nil {
				te.Size = blob.Size
			}
			files = append(files, te)
		} else {
			te.Type = "tree"
			dirs = append(dirs, te)
		}
	}
	// Build a fresh slice rather than append(dirs, files...) so files never
	// aliases into dirs' backing array (appendAssign).
	entries := make([]TreeEntry, 0, len(dirs)+len(files))
	entries = append(entries, dirs...)
	entries = append(entries, files...)

	// Use git log --format to get last commit per file — fast CLI approach
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// For each entry, use git log -1 --format to get last commit quickly.
	// Pin the log to the resolved commit hash so each entry's last commit is
	// computed relative to the requested ref (not HEAD). The hash is a
	// validated object id, so it cannot be interpreted as a git option.
	refHash := hash.String()
	// Each entry's last commit needs its own `git log -1`. Run them through a
	// bounded worker pool so a directory with many entries does not spawn one git
	// subprocess after another serially (N × process+startup latency). Each
	// goroutine writes only to its own entries[i], so there is no shared-slice
	// race.
	const maxLogWorkers = 8
	sem := make(chan struct{}, maxLogWorkers)
	var wg sync.WaitGroup
	for i := range entries {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			// Use the ASCII unit separator (\x1f) between fields instead of "|":
			// a commit subject (%s) or author name (%an) can legitimately contain
			// "|", which would otherwise shift every subsequent field.
			out, gerr := gitops.RunGitCommand(ctx, repo.StoragePath,
				"log", "-1", "--format=%H%x1f%s%x1f%an%x1f%ae%x1f%aI", refHash, "--", entries[i].Path)
			if gerr != nil || strings.TrimSpace(out) == "" {
				return
			}
			parts := strings.SplitN(strings.TrimSpace(out), "\x1f", 5)
			if len(parts) < 5 {
				return
			}
			sha := parts[0]
			entries[i].LastCommit = sha[:min(7, len(sha))]
			entries[i].LastMessage = parts[1]
			entries[i].LastAuthor = parts[2]
			if t, perr := time.Parse(time.RFC3339, parts[4]); perr == nil {
				entries[i].LastDate = t
			}
		}(i)
	}
	wg.Wait()

	response.Success(c, gin.H{
		"path":    treePath,
		"ref":     ref,
		"entries": entries,
		"commit": gin.H{
			"sha":     commit.Hash.String()[:7],
			"message": strings.SplitN(commit.Message, "\n", 2)[0],
			"author":  commit.Author.Name,
			"date":    commit.Author.When,
		},
	})
}

// GetBlob handles GET /api/v1/repos/:id/blob?ref=main&path=README.md
func (h *BrowseHandler) GetBlob(c *gin.Context) {
	gitRepo, _, err := h.openRepo(c)
	if err != nil {
		response.NotFound(c, err.Error())
		return
	}

	ref := c.Query("ref")
	filePath := strings.Trim(c.Query("path"), "/")

	if filePath == "" {
		response.BadRequest(c, "MISSING_PATH", "path query param is required")
		return
	}

	hash, err := resolveRef(gitRepo, ref)
	if err != nil {
		response.BadRequest(c, "INVALID_REF", err.Error())
		return
	}

	commit, err := gitRepo.CommitObject(*hash)
	if err != nil {
		response.InternalError(c, "Failed to get commit")
		return
	}

	file, err := commit.File(filePath)
	if err != nil {
		response.NotFound(c, "File not found: "+filePath)
		return
	}

	// Guard against oversized blobs before reading/base64-encoding them into
	// memory, which could otherwise exhaust the process.
	const maxBlobSize = 10 << 20 // 10 MB
	if file.Size > maxBlobSize {
		response.BadRequest(c, "FILE_TOO_LARGE", "File is too large to display")
		return
	}

	content, err := file.Contents()
	if err != nil {
		response.InternalError(c, "Failed to read file")
		return
	}

	// Detect if binary
	checkLen := len(content)
	if checkLen > 8000 {
		checkLen = 8000
	}
	isBinary := strings.ContainsRune(content[:checkLen], 0)
	ext := strings.TrimPrefix(path.Ext(filePath), ".")

	resp := gin.H{
		"path":      filePath,
		"name":      path.Base(filePath),
		"extension": ext,
		"size":      file.Size,
		"is_binary": isBinary,
	}
	if isBinary {
		resp["content"] = base64.StdEncoding.EncodeToString([]byte(content))
	} else {
		resp["content"] = content
	}

	response.Success(c, resp)
}

// GetCommits handles GET /api/v1/repos/:id/commits?ref=main&limit=30
func (h *BrowseHandler) GetCommits(c *gin.Context) {
	gitRepo, repo, err := h.openRepo(c)
	if err != nil {
		response.NotFound(c, err.Error())
		return
	}

	ref := c.Query("ref")
	limit := 30
	if l := c.Query("limit"); l != "" {
		if n, perr := strconv.Atoi(l); perr == nil && n > 0 {
			limit = n
		}
	}
	if limit > 100 {
		limit = 100
	}

	// Resolve the request-controlled ref to a concrete commit hash before
	// passing it to git, so user input can never be interpreted as a git option.
	hash, rerr := resolveRef(gitRepo, ref)
	if rerr != nil {
		response.BadRequest(c, "INVALID_REF", rerr.Error())
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// Use git log CLI — fast and efficient
	out, gerr := gitops.RunGitCommand(ctx, repo.StoragePath,
		"log", hash.String(),
		fmt.Sprintf("--max-count=%d", limit),
		// Field-separate with \x1f so a "|" inside a subject/author cannot shift fields.
		"--format=%H%x1f%s%x1f%an%x1f%ae%x1f%aI",
	)
	if gerr != nil {
		logServerError(c, gerr)
		response.InternalError(c, "Failed to read commit log")
		return
	}

	var commits []CommitEntry
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 5)
		if len(parts) < 5 {
			continue
		}
		sha := parts[0]
		var t time.Time
		if parsed, perr := time.Parse(time.RFC3339, parts[4]); perr == nil {
			t = parsed
		}
		commits = append(commits, CommitEntry{
			SHA:         sha,
			ShortSHA:    sha[:min(7, len(sha))],
			Message:     parts[1],
			Author:      parts[2],
			AuthorEmail: parts[3],
			Date:        t,
		})
	}

	response.Success(c, commits)
}

// GetCommit handles GET /api/v1/repos/:id/commit/:sha
func (h *BrowseHandler) GetCommit(c *gin.Context) {
	_, repo, err := h.openRepo(c)
	if err != nil {
		response.NotFound(c, err.Error())
		return
	}

	sha := c.Param("sha")

	// Reject anything that could be parsed as a git option before it reaches
	// the command line.
	if strings.HasPrefix(sha, "-") {
		response.BadRequest(c, "INVALID_SHA", "Invalid commit identifier")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	// Use show --stat for file stats and the full message. This also serves as
	// the existence check: an error or empty output means the commit is unknown.
	showOut, gerr := gitops.RunGitCommand(ctx, repo.StoragePath,
		"show", "--stat", "--format=%H|%an|%ae|%aI%n%B", sha)
	if gerr != nil || strings.TrimSpace(showOut) == "" {
		if gerr != nil {
			logServerError(c, gerr)
		}
		response.NotFound(c, "Commit not found")
		return
	}

	// Get the actual diff (unified)
	diffOut, _ := gitops.RunGitCommand(ctx, repo.StoragePath,
		"show", "--unified=3", "--format=", sha)

	// Parse metadata from show output
	var commitSHA, author, email, date, fullMsg string
	showLines := strings.Split(showOut, "\n")
	if len(showLines) > 0 {
		metaParts := strings.SplitN(showLines[0], "|", 4)
		if len(metaParts) >= 4 {
			commitSHA = metaParts[0]
			author = metaParts[1]
			email = metaParts[2]
			date = metaParts[3]
		}
	}

	// Extract full message (lines between format line and diff stats)
	msgLines := []string{}
	inStats := false
	for _, l := range showLines[1:] {
		if strings.HasPrefix(l, "diff --git") || strings.HasPrefix(l, " ") && strings.Contains(l, "|") {
			inStats = true
		}
		if !inStats {
			msgLines = append(msgLines, l)
		}
	}
	fullMsg = strings.TrimSpace(strings.Join(msgLines, "\n"))

	// Parse file stats from `git show --stat`
	type FileStat struct {
		Name      string `json:"name"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
	}
	var fileStats []FileStat
	for _, l := range showLines {
		// Lines like: " file.go | 10 ++"
		l = strings.TrimSpace(l)
		if strings.Contains(l, "|") && !strings.HasPrefix(l, "diff") {
			parts := strings.SplitN(l, "|", 2)
			if len(parts) == 2 {
				fname := strings.TrimSpace(parts[0])
				changes := strings.TrimSpace(parts[1])
				if fname == "" || strings.HasPrefix(changes, "Bin") {
					continue
				}
				adds := strings.Count(changes, "+")
				dels := strings.Count(changes, "-")
				if adds > 0 || dels > 0 {
					fileStats = append(fileStats, FileStat{
						Name:      fname,
						Additions: adds,
						Deletions: dels,
					})
				}
			}
		}
	}

	var parsedDate time.Time
	if t, perr := time.Parse(time.RFC3339, strings.TrimSpace(date)); perr == nil {
		parsedDate = t
	}

	shortSHA := sha
	if len(sha) > 7 {
		shortSHA = sha[:7]
	}

	response.Success(c, gin.H{
		"sha":          commitSHA,
		"short_sha":    shortSHA,
		"message":      fullMsg,
		"author":       author,
		"author_email": email,
		"date":         parsedDate,
		"diff":         strings.Split(diffOut, "\n"),
		"file_stats":   fileStats,
	})
}

// DownloadRepo handles GET /api/v1/repos/:id/download?ref=main
func (h *BrowseHandler) DownloadRepo(c *gin.Context) {
	id, ok := parseUUIDParam(c, "id", "repository")
	if !ok {
		return
	}
	repo, err := h.repoService.GetRepositoryByID(c, id)
	if err != nil {
		response.NotFound(c, "Repository not found")
		return
	}

	ref := c.Query("ref")
	if ref == "" {
		ref = "HEAD"
	}
	// Reject refs that could be parsed as git options.
	if strings.HasPrefix(ref, "-") {
		response.BadRequest(c, "INVALID_REF", "Invalid ref")
		return
	}

	// Clean/sanitize the repo name for filename
	safeName := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, repo.Name)

	// Sanitize the ref the same way for the filename so it cannot inject quotes
	// or control characters into the Content-Disposition header. The original
	// (option-injection-checked) ref is still passed to git archive below.
	safeRef := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, ref)

	filename := fmt.Sprintf("%s-%s.zip", safeName, safeRef)

	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Transfer-Encoding", "chunked")

	// Execute git archive and pipe directly to response
	// #nosec G204 - ref is checked for option injection, and command is run without shell
	cmd := exec.CommandContext(c.Request.Context(), gitops.GetGitExecutable(), "archive", "--format=zip", "--prefix="+safeName+"/", ref)
	cmd.Dir = repo.StoragePath
	cmd.Stdout = c.Writer
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		_ = c.Error(err)
	}
}

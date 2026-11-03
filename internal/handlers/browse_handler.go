package handlers

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	if !h.repoService.IsValidStoragePath(repo) {
		return nil, nil, fmt.Errorf("repository not cloned yet or path invalid")
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

	// Use git CLI for speed and boundedness: the per-entry go-git BlobObject
	// lookups this replaces inflated every (possibly huge, delta-encoded)
	// blob just to learn its size.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	sizes := treeBlobSizes(ctx, repo.StoragePath, tree.Hash.String())

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
			te.Size = sizes[entry.Name]
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

	// Last-commit summaries are cached by commit hash (entries sharing a commit
	// share one bounded string) and capped in aggregate per response, so an
	// upstream author cannot inflate memory with huge subjects.
	summaries := newCommitSummaryCache(maxTreeSummaryBytes)

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
			// "|", which would otherwise shift every subsequent field. The
			// subject goes last so the output cap can only truncate it.
			out, gerr := gitops.GitOutputLimited(ctx, repo.StoragePath, maxLogLineBytes,
				"log", "-1", "--format=%H%x1f%an%x1f%ae%x1f%aI%x1f%s", refHash, "--", entries[i].Path)
			if gerr != nil && !errors.Is(gerr, gitops.ErrOutputTooLarge) {
				return
			}
			parts := strings.SplitN(strings.TrimSpace(out), "\x1f", 5)
			if len(parts) < 5 {
				return
			}
			sha := parts[0]
			summary := summaries.get(sha, parts[1], parts[4])
			entries[i].LastCommit = sha[:minInt(7, len(sha))]
			entries[i].LastMessage = summary.message
			entries[i].LastAuthor = summary.author
			if t, perr := time.Parse(time.RFC3339, parts[3]); perr == nil {
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
			"message": truncateRunes(strings.SplitN(commit.Message, "\n", 2)[0], maxSubjectRunes),
			"author":  truncateRunes(commit.Author.Name, maxSubjectRunes),
			"date":    commit.Author.When,
		},
	})
}

// GetBlob handles GET /api/v1/repos/:id/blob?ref=main&path=README.md
func (h *BrowseHandler) GetBlob(c *gin.Context) {
	gitRepo, repo, err := h.openRepo(c)
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

	// Locate the entry by walking trees only. go-git's commit.File would also
	// decode the blob, buffering its whole inflated delta stream before any
	// size check could run.
	tree, err := commit.Tree()
	if err != nil {
		response.InternalError(c, "Failed to read tree")
		return
	}
	entry, err := tree.FindEntry(filePath)
	if err != nil || !entry.Mode.IsFile() {
		response.NotFound(c, "File not found: "+filePath)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	// Guard against oversized blobs before reading/base64-encoding them into
	// memory, which could otherwise exhaust the process. "git cat-file -s"
	// reads the size from the object (or delta) header without inflating it.
	const maxBlobSize = 10 << 20 // 10 MB
	oid := entry.Hash.String()
	size, err := blobSize(ctx, repo.StoragePath, oid)
	if err != nil {
		logServerError(c, err)
		response.InternalError(c, "Failed to read file")
		return
	}
	if size > maxBlobSize {
		response.BadRequest(c, "FILE_TOO_LARGE", "File is too large to display")
		return
	}

	// The output cap backs up the size check (e.g. a racing repack).
	content, err := gitops.GitOutputLimited(ctx, repo.StoragePath, maxBlobSize, "cat-file", "blob", oid)
	if err != nil {
		logServerError(c, err)
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
		"size":      size,
		"is_binary": isBinary,
	}
	if isBinary {
		resp["content"] = base64.StdEncoding.EncodeToString([]byte(content))
	} else {
		resp["content"] = content
	}

	response.Success(c, resp)
}

// Output and size budgets for the browse endpoints. Commit subjects, author
// names, diffs and file contents are upstream-controlled, so every git
// invocation here is capped.
const (
	maxSubjectRunes     = 200
	maxLogLineBytes     = 16 << 10  // per-entry `git log -1` output in GetTree
	maxTreeSummaryBytes = 256 << 10 // all distinct summaries in one GetTree response
	maxTreeListBytes    = 16 << 20  // `git ls-tree -l` output for one directory
	maxCommitLogBytes   = 2 << 20   // GetCommits (at most 100 commits)
	maxCommitShowBytes  = 1 << 20   // GetCommit metadata, message and --stat
	maxCommitDiffBytes  = 8 << 20   // GetCommit unified diff

	// GlobalSearch (repository_handler.go) budgets.
	maxSearchOutputBytes = 1 << 20 // per-repository `git grep` output
	maxSearchLineRunes   = 500     // per reported match line
)

// isOutputTruncated reports whether err signals that a limited git command hit
// its output cap (the returned output is then a truncated prefix).
func isOutputTruncated(err error) bool {
	return errors.Is(err, gitops.ErrOutputTooLarge)
}

// truncateRunes shortens s to at most n runes, appending an ellipsis when cut.
// The result is a fresh copy, so it never pins a larger backing string.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return strings.Clone(s)
	}
	i, count := 0, 0
	for i = range s {
		if count == n {
			break
		}
		count++
	}
	return strings.Clone(s[:i]) + "…"
}

type commitSummary struct {
	message string
	author  string
}

// commitSummaryCache deduplicates per-commit summaries within one GetTree
// response and enforces an aggregate byte budget: once spent, further commits
// get an empty message (the hash, author and date are still reported).
type commitSummaryCache struct {
	mu     sync.Mutex
	byHash map[string]commitSummary
	budget int
}

func newCommitSummaryCache(budget int) *commitSummaryCache {
	return &commitSummaryCache{byHash: make(map[string]commitSummary), budget: budget}
}

func (c *commitSummaryCache) get(hash, author, subject string) commitSummary {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.byHash[hash]; ok {
		return s
	}
	s := commitSummary{author: truncateRunes(author, maxSubjectRunes)}
	c.budget -= len(s.author)
	if msg := truncateRunes(subject, maxSubjectRunes); len(msg) <= c.budget {
		s.message = msg
		c.budget -= len(msg)
	}
	c.byHash[hash] = s
	return s
}

// treeBlobSizes returns the size of every blob directly in the tree object
// treeHash, keyed by entry name, via `git ls-tree -l -z` (sizes come from the
// object headers; nothing is inflated). On error it returns what it has, so
// sizes are simply omitted.
func treeBlobSizes(ctx context.Context, dir, treeHash string) map[string]int64 {
	sizes := make(map[string]int64)
	out, err := gitops.GitOutputLimited(ctx, dir, maxTreeListBytes, "ls-tree", "-l", "-z", treeHash)
	if err != nil && !errors.Is(err, gitops.ErrOutputTooLarge) {
		return sizes
	}
	for _, rec := range strings.Split(out, "\x00") {
		// "<mode> SP <type> SP <object> SP+ <size> TAB <name>"
		meta, name, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) != 4 || fields[1] != "blob" {
			continue
		}
		if n, perr := strconv.ParseInt(fields[3], 10, 64); perr == nil {
			sizes[name] = n
		}
	}
	return sizes
}

// blobSize returns an object's size via `git cat-file -s`, which reads it from
// the object or delta header rather than inflating the content.
func blobSize(ctx context.Context, dir, oid string) (int64, error) {
	out, err := gitops.GitOutputLimited(ctx, dir, 64, "cat-file", "-s", oid)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(out), 10, 64)
}

// hexObjectID matches a full or abbreviated (at least 4 digits) object id.
var hexObjectID = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)

// resolveCommitParam resolves a request-supplied commit identifier to a full
// commit hash. Only hex object ids and existing branch/tag names are accepted;
// anything else (options, ranges, "rev^{...}" or ":path" expressions) is
// rejected before reaching git.
func resolveCommitParam(ctx context.Context, gitRepo *gogit.Repository, dir, param string) (string, bool) {
	candidate := ""
	switch {
	case hexObjectID.MatchString(param):
		candidate = strings.ToLower(param)
	default:
		if ref, err := gitRepo.Reference(plumbing.NewBranchReferenceName(param), true); err == nil {
			candidate = ref.Hash().String()
		} else if ref, err := gitRepo.Reference(plumbing.NewTagReferenceName(param), true); err == nil {
			candidate = ref.Hash().String()
		}
	}
	if candidate == "" {
		return "", false
	}
	// Peel tags and expand abbreviations; --verify fails on ambiguity or a
	// non-commit object. candidate is pure hex, so it cannot be an option.
	out, err := gitops.GitOutputLimited(ctx, dir, 256, "rev-parse", "--verify", "--quiet", candidate+"^{commit}")
	if err != nil {
		return "", false
	}
	full := strings.TrimSpace(out)
	if !plumbing.IsHash(full) && (len(full) != 64 || !hexObjectID.MatchString(full)) {
		return "", false
	}
	return full, true
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
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
	// Output is capped: subjects are attacker-controlled upstream content. A
	// line cut by the cap fails the field-count check below and is skipped.
	out, gerr := gitops.GitOutputLimited(ctx, repo.StoragePath, maxCommitLogBytes,
		"log", hash.String(),
		fmt.Sprintf("--max-count=%d", limit),
		// Field-separate with \x1f so a "|" inside a subject/author cannot shift fields.
		"--format=%H%x1f%s%x1f%an%x1f%ae%x1f%aI",
	)
	if gerr != nil && !errors.Is(gerr, gitops.ErrOutputTooLarge) {
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
			ShortSHA:    sha[:minInt(7, len(sha))],
			Message:     truncateRunes(parts[1], maxSubjectRunes),
			Author:      truncateRunes(parts[2], maxSubjectRunes),
			AuthorEmail: truncateRunes(parts[3], maxSubjectRunes),
			Date:        t,
		})
	}

	response.Success(c, commits)
}

// GetCommit handles GET /api/v1/repos/:id/commit/:sha
func (h *BrowseHandler) GetCommit(c *gin.Context) {
	gitRepo, repo, err := h.openRepo(c)
	if err != nil {
		response.NotFound(c, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	// Resolve the parameter (a full or abbreviated hex id, or a branch/tag
	// name) to a full commit hash, so only a validated object id ever reaches
	// git show: no option, revision-range or other revision expression.
	sha, ok := resolveCommitParam(ctx, gitRepo, repo.StoragePath, c.Param("sha"))
	if !ok {
		response.NotFound(c, "Commit not found")
		return
	}

	// Use show --stat for file stats and the full message. This also serves as
	// the existence check: an error or empty output means the commit is unknown.
	// Field-separate the metadata with \x1f (as in GetTree/GetCommits) so an
	// author name containing "|" cannot shift the email/date fields, and terminate
	// %B with an \x1e sentinel so the message body can be split from the --stat
	// section without heuristics (a message may itself contain indented "|" lines,
	// e.g. tables or path lists).
	// Both outputs are capped; a truncated stat section only loses trailing
	// file stats, and a truncated diff is flagged in the response.
	showOut, gerr := gitops.GitOutputLimited(ctx, repo.StoragePath, maxCommitShowBytes,
		"show", "--stat", "--format=%H%x1f%an%x1f%ae%x1f%aI%n%B%x1e", sha, "--")
	if (gerr != nil && !errors.Is(gerr, gitops.ErrOutputTooLarge)) || strings.TrimSpace(showOut) == "" {
		if gerr != nil {
			logServerError(c, gerr)
		}
		response.NotFound(c, "Commit not found")
		return
	}

	// Get the actual diff (unified)
	diffOut, derr := gitops.GitOutputLimited(ctx, repo.StoragePath, maxCommitDiffBytes,
		"show", "--unified=3", "--format=", sha, "--")
	diffTruncated := errors.Is(derr, gitops.ErrOutputTooLarge)

	// Split at the \x1e sentinel: everything before it is the metadata line plus
	// the verbatim %B message body; everything after is the --stat section.
	metaAndMsg, statsPart, _ := strings.Cut(showOut, "\x1e")

	// Parse metadata from the first line, the message from the rest.
	var commitSHA, author, email, date string
	headerLine, msgPart, _ := strings.Cut(metaAndMsg, "\n")
	metaParts := strings.SplitN(headerLine, "\x1f", 4)
	if len(metaParts) >= 4 {
		commitSHA = metaParts[0]
		author = metaParts[1]
		email = metaParts[2]
		date = metaParts[3]
	}
	fullMsg := strings.TrimSpace(msgPart)

	// Parse file stats from `git show --stat`
	type FileStat struct {
		Name      string `json:"name"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
	}
	var fileStats []FileStat
	for _, l := range strings.Split(statsPart, "\n") {
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

	response.Success(c, gin.H{
		"sha":            commitSHA,
		"short_sha":      sha[:7],
		"message":        fullMsg,
		"author":         author,
		"author_email":   email,
		"date":           parsedDate,
		"diff":           strings.Split(diffOut, "\n"),
		"diff_truncated": diffTruncated,
		"file_stats":     fileStats,
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
	if !h.repoService.IsValidStoragePath(repo) {
		response.NotFound(c, "Repository not cloned yet or path invalid")
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

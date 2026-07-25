package gitops

import (
	"sync"
	"time"

	"github.com/gitduppy/gitduppy/internal/models"
	"gorm.io/gorm"
)

// dedupeSavingsTTL is how long a computed dedupe-savings snapshot is reused
// before the pool directories are walked again. The figure is inherently a
// slow-moving estimate (it changes only as repositories are added/removed or
// pools grow), so a coarse TTL avoids repeated disk walks on dashboard polls.
const dedupeSavingsTTL = 10 * time.Minute

// DedupeSavings summarizes how much disk space the shared-object-pool dedupe
// clone architecture is saving: PoolCount pools hold SharedBytes of git
// objects that would otherwise have been duplicated into every repository
// sharing them.
type DedupeSavings struct {
	PoolCount           int       `json:"pool_count"`
	SharedBytes         int64     `json:"shared_bytes"`
	EstimatedSavedBytes int64     `json:"estimated_saved_bytes"`
	ComputedAt          time.Time `json:"computed_at"`
}

// dedupeSavingsCache holds the last computed DedupeSavings for GitOperations,
// refreshed lazily in the background (see DedupeSavings method) so dashboard
// requests never block on a filesystem walk.
type dedupeSavingsCache struct {
	mu         sync.Mutex
	value      *DedupeSavings
	at         time.Time
	refreshing bool
}

// DedupeSavings returns the last computed dedupe-savings snapshot without
// blocking the caller, kicking off a background refresh when the cached value
// is missing or stale. It returns nil until the first refresh completes.
func (g *GitOperations) DedupeSavings(db *gorm.DB) *DedupeSavings {
	g.dedupeCache.mu.Lock()
	cached := g.dedupeCache.value
	stale := g.dedupeCache.at.IsZero() || time.Since(g.dedupeCache.at) >= dedupeSavingsTTL
	if stale && !g.dedupeCache.refreshing {
		g.dedupeCache.refreshing = true
		go g.refreshDedupeSavings(db)
	}
	g.dedupeCache.mu.Unlock()
	return cached
}

// refreshDedupeSavings groups active repositories by the shared pool their
// URL hashes to, measures each pool actually referenced by at least one repo
// on disk, and estimates the space saved as each pool's size times the number
// of additional repositories sharing it (the copies that dedupe avoided).
// This undercounts slightly relative to reality (it assumes every repo in a
// pool would otherwise have needed a full copy of the pool's current size,
// ignoring incremental growth differences), but is cheap and directionally
// honest.
func (g *GitOperations) refreshDedupeSavings(db *gorm.DB) {
	defer func() {
		g.dedupeCache.mu.Lock()
		g.dedupeCache.refreshing = false
		g.dedupeCache.mu.Unlock()
	}()

	var urls []string
	if err := db.Model(&models.Repository{}).Where("is_active = ?", true).Pluck("url", &urls).Error; err != nil {
		return
	}

	savings := g.computeDedupeSavings(urls)

	g.dedupeCache.mu.Lock()
	g.dedupeCache.value = savings
	g.dedupeCache.at = time.Now()
	g.dedupeCache.mu.Unlock()
}

// computeDedupeSavings is the pure grouping/measuring/estimating step of
// refreshDedupeSavings, split out so it can be unit tested against a
// synthetic pool directory layout without a database.
func (g *GitOperations) computeDedupeSavings(urls []string) *DedupeSavings {
	poolRepoCounts := make(map[string]int, len(urls))
	for _, url := range urls {
		poolRepoCounts[g.GetPoolPath(url)]++
	}

	savings := &DedupeSavings{ComputedAt: time.Now()}
	for poolPath, repoCount := range poolRepoCounts {
		if repoCount < 2 {
			// A pool with a single repository is not saving anything; it may
			// also simply not exist on disk if dedupe was never enabled.
			continue
		}
		size := DirSize(poolPath)
		if size == 0 {
			continue
		}
		savings.PoolCount++
		savings.SharedBytes += size
		savings.EstimatedSavedBytes += size * int64(repoCount-1)
	}
	return savings
}

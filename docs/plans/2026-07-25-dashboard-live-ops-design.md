# Dashboard live-operations upgrade

Date: 2026-07-25

## Problem

The dashboard's "Interactive Sync Timeline" always shows `0s` for job
duration: it reads `job.duration_ms`, a field the backend never sends
(`CloneJob` only has `started_at`/`completed_at`). The "Recent Jobs" table
next to it computes duration correctly from those two timestamps client-side
— the timeline should do the same.

Separately, the dashboard is optimized for point-in-time stats, not live
operational awareness: no visibility into what's actively running, what's
queued, what's about to be scheduled, or why the last few clones failed.

## Goals

Primary: live operational awareness — see what's happening right now.
Secondary: a handful of cheap, high-value read-only widgets that round out
the picture without turning the dashboard into an analytics page.

## Scope

1. Fix the timeline duration bug.
2. Live log firehose: one scrolling panel merging log lines from every
   currently-running job, tagged by repo.
3. Five new `GET /api/v1/dashboard/overview` fields:
   - `queue`: `{depth, running, max_concurrent}`
   - `next_syncs`: soonest N repos due for a scheduled mirror
   - `recent_failures`: last N failed jobs, with a retry action
   - `github_rate_limit`: `{remaining, limit, reset_at} | null`
   - `dedupe_savings`: `{pool_count, shared_bytes, estimated_saved_bytes} | null`

## Backend design

### Cheap widgets (queue, next syncs, recent failures)

All ride on data that already exists — no new state tracking.

- `DashboardService.GetQueueStatus(worker *gitops.CloneWorker)`: combines
  `worker.QueueDepth()`, `COUNT(*) WHERE status='running'`, and
  `worker.Config().MaxConcurrent`.
- `DashboardService.GetNextSyncs(ctx, limit)`: `last_clone_at +
  clone_interval_minutes` per active repo with `clone_interval_minutes > 0`
  (manual-only repos excluded, same filter the scheduler itself applies),
  ordered ascending.
- `DashboardService.GetRecentFailures(ctx, limit)`: same preload shape as
  `GetTimelineData` (omit `EncryptedCredentials`), filtered
  `status='failed'`, ordered by `completed_at DESC`. The frontend's "Retry"
  button reuses the existing `POST /repositories/:id/clone` endpoint — no
  new retry endpoint needed.

`DashboardHandler` gains a `*gitops.CloneWorker` reference (wired in
`main.go` alongside the existing `*DashboardService`) so `GetQueueStatus`
has something to call `QueueDepth()` on.

### Live log firehose

`LogHub` (`internal/gitops/worker.go`) gets a second, parallel subscriber
list for global listeners; the existing per-repo path is untouched:

```go
type FirehoseEntry struct {
    RepositoryID   string `json:"repository_id"`
    RepositoryName string `json:"repository_name"`
    Line           string `json:"line"`
    TS             string `json:"ts"`
}

type LogHub struct {
    mu          sync.Mutex
    subscribers map[string][]chan string  // unchanged
    globalSubs  []chan FirehoseEntry       // new
}

func (h *LogHub) SubscribeAll() chan FirehoseEntry
func (h *LogHub) UnsubscribeAll(ch chan FirehoseEntry)
```

`Broadcast` gains a `repoName` parameter (its one call site,
`CloneProgress.Write`, gets a `repositoryName` field set at construction
time from the already-loaded `repo.Name` — no extra query) and fans each
message out to `globalSubs` as a `FirehoseEntry` in addition to the
existing per-repo fan-out. Global sends use the same non-blocking `select`
as per-repo sends: a slow dashboard viewer drops lines rather than backing
up cloning workers.

New handler `StreamDashboardLogs` at `GET /api/v1/dashboard/logs/stream`
mirrors `StreamRepositoryLogs` (same origin-check upgrader, ping-keepalive
goroutine, peer-disconnect reader) but subscribes via `SubscribeAll()`.

### GitHub rate-limit tracker

New `internal/gitops/ratelimit_tracker.go`: a package-level mutex-guarded
`rateLimitState{remaining, limit int; resetAt, updatedAt time.Time}`,
updated by `recordGitHubRateLimit(resp)` from the two places
`github_fetcher.go` already reads `X-RateLimit-*` headers (piggybacking on
reads that happen anyway). `GetGitHubRateLimit()` returns
`observed=false` until the first call; the dashboard shows "no GitHub
activity yet" rather than a misleading `0/0`.

### Dedupe savings

The only widget requiring a filesystem walk, so it's computed on a 10-minute
timer (similar cadence to `cleanup_worker.go`'s periodic sweep), cached in
memory with its own `computed_at`, and read instantly by `GetOverview`:

1. Group active dedupe-enabled repos by pool path (`getPoolPath`, exported).
2. `DirSize(poolPath)` per pool with ≥1 repo (existing helper).
3. `estimated_saved_bytes = Σ poolSize × (reposInPool - 1)`.

This undercounts slightly (ignores per-repo growth differences within a
pool) but is directionally honest and cheap. `dedupe_savings` is `null`
until the first computation completes.

## Frontend design

```
[Total Repos] [Success] [Failed] [Storage]     existing stat cards
[Queue Depth] [Next Sync] [Rate Limit]         new stat cards
─────────────────────────────────────────
Interactive Sync Timeline                      existing, duration fixed
─────────────────────────────────────────
Live Activity Firehose                         new, full-width scrolling panel
─────────────────────────────────────────
[Recent Jobs table]      [Recent Failures]     existing + new, 2-col grid
─────────────────────────────────────────
Dedupe Savings                                 new, low-key card
```

- New stat cards reuse the existing `.stat-card` markup.
- Firehose: fixed-height (~300px) auto-scrolling panel, lines prefixed
  `[repo-name]`, colored by a client-side repo→color hash. Pauses
  auto-scroll on hover/manual scroll-up (terminal-like).
- Recent Failures: same row shape as Recent Jobs, plus a Retry button.
- Dedupe Savings: intentionally the least prominent — the most heuristic
  number and least "live" of the set.
- Firehose WebSocket opens on dashboard load, closes on navigation away,
  mirroring the existing repo-detail-page pattern.

## Error handling

- Every new `overview` field degrades independently (errgroup pattern
  already in `GetOverview`): one failing sub-query doesn't blank the whole
  dashboard.
- `github_rate_limit` / `dedupe_savings` are `null`-able "not yet observed"
  states, not zeros, so the frontend can render "no data yet" instead of a
  misleading `0`.
- Firehose WS: same backpressure (non-blocking channel sends) and
  disconnect-handling as the existing per-repo stream.

## Testing

- Backend: unit tests for `GetQueueStatus`, `GetNextSyncs`,
  `GetRecentFailures`, `recordGitHubRateLimit`/`GetGitHubRateLimit`, and the
  dedupe-savings aggregation (table-driven, using a temp directory tree for
  the pool/repo layout).
- `LogHub`: unit test that `Broadcast` reaches both a per-repo subscriber
  and a global subscriber, and that unsubscribing one doesn't affect the
  other.
- Full suite: `go build`, `go vet`, `GOOS=linux go build`, `go test ./...`,
  `golangci-lint`, `gosec` — all must stay clean.
- Manual: run the stack, watch the dashboard while a real clone is in
  flight, confirm the firehose shows live lines, the timeline shows real
  durations, and all new widgets render (including their empty/loading/error
  states).

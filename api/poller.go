package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	fetchTimeout = 10 * time.Second
	syncTimeout  = 30 * time.Second
	maxFeedBytes = 5 << 20

	// Every trigger shares one run, so they all queue behind a single key.
	syncKey = "sync"
)

// outcome records how a source was served, which is what makes the dedup
// visible in the poll summary.
type outcome string

const (
	servedNetwork outcome = "network"
	servedShared  outcome = "shared"
	servedCache   outcome = "cache"
)

// Cache is a TTL map guarded by one lock.
//
// There is no background sweep. The keys are the configured feed URLs, a small
// fixed set, and an expired entry is overwritten on the next fetch.
type Cache[K comparable, V any] struct {
	mu      sync.RWMutex
	ttl     time.Duration
	now     func() time.Time
	entries map[K]cacheEntry[V]
}

type cacheEntry[V any] struct {
	value     V
	expiresAt time.Time
}

func NewCache[K comparable, V any](ttl time.Duration) *Cache[K, V] {
	return &Cache[K, V]{ttl: ttl, now: time.Now, entries: make(map[K]cacheEntry[V])}
}

func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[key]
	if !ok || !c.now().Before(entry.expiresAt) {
		var zero V
		return zero, false
	}
	return entry.value, true
}

func (c *Cache[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry[V]{value: value, expiresAt: c.now().Add(c.ttl)}
}

// PolledDeadline is a feed entry stamped with the case and source it came from.
type PolledDeadline struct {
	CaseID     int
	Title      string
	DueDate    time.Time
	Source     string
	ExternalID string
}

// SourceOutcome is how one source fared: served from somewhere, or failed.
type SourceOutcome struct {
	Source string
	Served outcome
	Err    error
}

type PollResult struct {
	Deadlines []PolledDeadline
	Outcomes  []SourceOutcome
}

// SourceError is one failure in a run summary, safe to show in the UI.
type SourceError struct {
	Source string `json:"source"`
	Error  string `json:"error"`
}

// SyncSummary is one poll run, as logged and as returned by POST /api/poll.
type SyncSummary struct {
	StartedAt  time.Time     `json:"started_at"`
	DurationMS int64         `json:"duration_ms"`
	Sources    int           `json:"sources"`
	Fetched    int           `json:"fetched"`
	Shared     int           `json:"shared"`
	CacheHits  int           `json:"cache_hits"`
	Items      int           `json:"items"`
	Inserted   int           `json:"inserted"`
	Updated    int           `json:"updated"`
	Unchanged  int           `json:"unchanged"`
	Errors     []SourceError `json:"errors"`
}

type deadlineWriter interface {
	ExistingCaseIDs(ctx context.Context, ids []int) (map[int]bool, error)
	UpsertDeadlines(ctx context.Context, rows []PolledDeadline) (UpsertCounts, error)
}

// Poller fetches every configured source concurrently and turns feed entries
// into deadline rows.
//
// At most len(slots) HTTP requests run at once. Sources that share a URL are
// fetched once: singleflight merges requests that overlap in time, and the TTL
// cache answers the ones that arrive afterwards. A failing source only affects
// its own outcome, and its error is never cached, so the next poll retries it.
type Poller struct {
	client  *http.Client
	store   deadlineWriter
	sources []Source
	cache   *Cache[string, []FeedItem]
	slots   chan struct{}
	loc     *time.Location

	flights  singleflight.Group
	syncRuns singleflight.Group

	// shutdown is the process's lifetime. A run outlives the request that
	// triggered it, but not the program.
	shutdown context.Context

	// testHookJoined runs once a caller has attached to the flight for a key.
	// Only the tests set it, to line several callers up on one flight.
	testHookJoined func(key string)
}

func NewPoller(shutdown context.Context, store deadlineWriter, sources []Source, loc *time.Location, concurrency int, cacheTTL time.Duration) *Poller {
	return &Poller{
		client:   &http.Client{},
		store:    store,
		sources:  sources,
		cache:    NewCache[string, []FeedItem](cacheTTL),
		slots:    make(chan struct{}, concurrency),
		loc:      loc,
		shutdown: shutdown,
	}
}

type sourceResult struct {
	outcome   SourceOutcome
	deadlines []PolledDeadline
}

// PollAll fetches every source concurrently and never touches the database, so
// it can be tested with nothing but an httptest server.
func (p *Poller) PollAll(ctx context.Context, sources []Source) PollResult {
	results := make(chan sourceResult, len(sources))
	var wg sync.WaitGroup
	for _, src := range sources {
		wg.Go(func() { results <- p.poll(ctx, src) })
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	var result PollResult
	for r := range results {
		result.Outcomes = append(result.Outcomes, r.outcome)
		result.Deadlines = append(result.Deadlines, r.deadlines...)
	}
	slices.SortFunc(result.Outcomes, func(a, b SourceOutcome) int {
		return strings.Compare(a.Source, b.Source)
	})
	slices.SortFunc(result.Deadlines, func(a, b PolledDeadline) int {
		return strings.Compare(a.ExternalID, b.ExternalID)
	})
	return result
}

func (p *Poller) poll(ctx context.Context, src Source) sourceResult {
	items, served, err := p.items(ctx, src)
	if err != nil {
		return sourceResult{outcome: SourceOutcome{Source: src.ID, Err: err}}
	}

	// Each source copies the shared items into its own rows, which is what
	// lets two sources share one fetch and still land in different cases.
	deadlines := make([]PolledDeadline, 0, len(items))
	for _, item := range items {
		if !src.keep(item) {
			continue
		}
		deadlines = append(deadlines, PolledDeadline{
			CaseID:     src.CaseID,
			Title:      item.Title,
			DueDate:    item.Due,
			Source:     src.Kind,
			ExternalID: src.ID + ":" + item.UID,
		})
	}
	return sourceResult{
		outcome:   SourceOutcome{Source: src.ID, Served: served},
		deadlines: deadlines,
	}
}

// joined marks this caller as attached to the flight for key. DoChan registers
// the caller before it returns, so by the time this runs a follower is provably
// sharing the leader's call rather than about to start its own.
func (p *Poller) joined(key string) {
	if p.testHookJoined != nil {
		p.testHookJoined(key)
	}
}

func (p *Poller) items(ctx context.Context, src Source) ([]FeedItem, outcome, error) {
	if items, ok := p.cache.Get(src.URL); ok {
		return items, servedCache, nil
	}

	// served stays empty until fn runs, and singleflight runs only the
	// leader's copy of it. Setting it after the request means a leader whose
	// second cache check hits reports cache rather than a fetch that never
	// happened.
	var served outcome
	ch := p.flights.DoChan(src.URL, func() (any, error) {
		// Another source may have filled the cache between our miss and the
		// start of this flight.
		if items, ok := p.cache.Get(src.URL); ok {
			served = servedCache
			return items, nil
		}

		// Taking the slot inside the flight means it caps real requests. Cache
		// hits and followers never queue behind a slow feed.
		select {
		case p.slots <- struct{}{}:
			defer func() { <-p.slots }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}

		items, err := p.fetch(ctx, src)
		if err != nil {
			return nil, err
		}
		served = servedNetwork
		p.cache.Set(src.URL, items)
		return items, nil
	})

	p.joined(src.URL)

	// A plain receive, so no flight goroutine outlives PollAll. fetch already
	// honours ctx, and fn's write to served happens before the result is sent.
	res := <-ch
	if res.Err != nil {
		return nil, "", res.Err
	}
	if served == "" {
		served = servedShared
	}
	return res.Val.([]FeedItem), served, nil
}

func (p *Poller) fetch(ctx context.Context, src Source) ([]FeedItem, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", src.ID, err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", src.ID, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("fetch %s: status %d", src.ID, resp.StatusCode)
	}

	body := io.LimitReader(resp.Body, maxFeedBytes)
	if src.Kind == kindDocket {
		return parseDocket(body, src.ID)
	}
	return parseCalendar(body, src.ID, p.loc)
}

// Sync polls every source and writes the result in one transaction.
//
// The startup poll, the ticker and POST /api/poll all land here, and
// overlapping triggers share a single run.
func (p *Poller) Sync(ctx context.Context) (SyncSummary, error) {
	ch := p.syncRuns.DoChan(syncKey, func() (any, error) {
		// Several callers can share this run. One of them hanging up
		// shouldn't cancel it for the rest.
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), syncTimeout)
		defer cancel()

		// Shutdown still has to reach it. A run cancelled mid-transaction
		// rolls back, and the next start polls again.
		stop := context.AfterFunc(p.shutdown, cancel)
		defer stop()

		return p.sync(runCtx)
	})
	p.joined(syncKey)

	res := <-ch
	if res.Err != nil {
		return SyncSummary{}, res.Err
	}
	return res.Val.(SyncSummary), nil
}

func (p *Poller) sync(ctx context.Context) (SyncSummary, error) {
	started := time.Now()
	summary := SyncSummary{
		StartedAt: started.UTC(),
		Sources:   len(p.sources),
		Errors:    []SourceError{},
	}

	result := p.PollAll(ctx, p.sources)
	for _, o := range result.Outcomes {
		switch {
		case o.Err != nil:
			summary.Errors = append(summary.Errors, SourceError{Source: o.Source, Error: o.Err.Error()})
		case o.Served == servedNetwork:
			summary.Fetched++
		case o.Served == servedShared:
			summary.Shared++
		case o.Served == servedCache:
			summary.CacheHits++
		}
	}
	summary.Items = len(result.Deadlines)

	rows, dropped, err := p.dropMissingCases(ctx, result.Deadlines)
	if err != nil {
		return SyncSummary{}, err
	}
	summary.Errors = append(summary.Errors, dropped...)
	slices.SortFunc(summary.Errors, func(a, b SourceError) int {
		return strings.Compare(a.Source, b.Source)
	})

	counts, err := p.store.UpsertDeadlines(ctx, rows)
	if err != nil {
		return SyncSummary{}, err
	}
	summary.Inserted = counts.Inserted
	summary.Updated = counts.Updated
	summary.Unchanged = counts.Unchanged
	summary.DurationMS = time.Since(started).Milliseconds()

	slog.Info("poll finished",
		"sources", summary.Sources,
		"fetched", summary.Fetched,
		"shared", summary.Shared,
		"cache_hits", summary.CacheHits,
		"items", summary.Items,
		"inserted", summary.Inserted,
		"updated", summary.Updated,
		"unchanged", summary.Unchanged,
		"errors", len(summary.Errors),
		"took", time.Since(started))
	return summary, nil
}

// dropMissingCases keeps foreign-key violations out of the batch. One row
// naming a deleted case would abort the transaction and lose every good row
// with it.
func (p *Poller) dropMissingCases(ctx context.Context, rows []PolledDeadline) ([]PolledDeadline, []SourceError, error) {
	if len(rows) == 0 {
		return rows, nil, nil
	}

	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.CaseID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)

	existing, err := p.store.ExistingCaseIDs(ctx, ids)
	if err != nil {
		return nil, nil, err
	}

	kept := make([]PolledDeadline, 0, len(rows))
	var dropped []SourceError
	reported := make(map[string]bool)
	for _, row := range rows {
		if existing[row.CaseID] {
			kept = append(kept, row)
			continue
		}
		sourceID, _, _ := strings.Cut(row.ExternalID, ":")
		if !reported[sourceID] {
			reported[sourceID] = true
			dropped = append(dropped, SourceError{
				Source: sourceID,
				Error:  fmt.Sprintf("case %d no longer exists", row.CaseID),
			})
		}
	}
	return kept, dropped, nil
}

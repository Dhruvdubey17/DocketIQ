package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func newTestPoller(t *testing.T, store deadlineWriter, concurrency int, ttl time.Duration) *Poller {
	t.Helper()
	p := NewPoller(context.Background(), store, nil, nyc, concurrency, ttl)
	// Keep-alive connections outlive the test and look like leaks.
	p.client = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	t.Cleanup(p.client.CloseIdleConnections)
	return p
}

func docketBody(uid, title string, due time.Time) string {
	return fmt.Sprintf(`{"court":"D. Mass.","docket_number":"1:26-cv-10482","extra":"ignored",
		"entries":[{"uid":%q,"title":%q,"due":%q}]}`, uid, title, due.Format(time.RFC3339))
}

func ardenCalendarBody(due time.Time) string {
	at := due.UTC().Format("20060102T150405") + "Z"
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:ard-1\r\nSUMMARY:Status conference ARD-CV-26-0412 Mendez\r\nDTSTART:" + at + "\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:ard-2\r\nSUMMARY:Motion hearing ARD-CV-26-0977 Oakridge\r\nDTSTART:" + at + "\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:ard-3\r\nSUMMARY:Trial call ARD-CV-26-0311 Sandoval\r\nDTSTART:" + at + "\r\nEND:VEVENT\r\n" +
		"END:VCALENDAR\r\n"
}

func TestPollAllStaysUnderTheConcurrencyLimit(t *testing.T) {
	const limit = 3

	var inFlight, peak atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := inFlight.Add(1)
		for {
			seen := peak.Load()
			if current <= seen || peak.CompareAndSwap(seen, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
		_, _ = fmt.Fprint(w, docketBody("dkt-1", "filing", time.Now()))
	}))
	t.Cleanup(srv.Close)

	sources := make([]Source, 12)
	for i := range sources {
		sources[i] = Source{
			ID:     fmt.Sprintf("feed-%02d", i),
			Kind:   kindDocket,
			URL:    fmt.Sprintf("%s/?feed=%d", srv.URL, i),
			CaseID: 1,
		}
	}

	p := newTestPoller(t, nil, limit, time.Minute)
	result := p.PollAll(context.Background(), sources)

	if got := peak.Load(); got > limit {
		t.Errorf("peak concurrency = %d, want at most %d", got, limit)
	}
	if len(result.Deadlines) != len(sources) {
		t.Errorf("got %d deadlines, want %d", len(result.Deadlines), len(sources))
	}
}

func TestPollAllSharesOneFetchBetweenSources(t *testing.T) {
	due := time.Date(2026, 10, 8, 14, 0, 0, 0, time.UTC)

	var requests atomic.Int64
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		// Held until both callers have attached to the flight, so the second
		// one is a follower every run rather than a cache hit.
		<-release
		_, _ = fmt.Fprint(w, ardenCalendarBody(due))
	}))
	t.Cleanup(srv.Close)

	sources := []Source{
		{ID: "mendez-court-calendar", Kind: kindCalendar, URL: srv.URL, CaseID: 3, Match: "ARD-CV-26-0412"},
		{ID: "oakridge-court-calendar", Kind: kindCalendar, URL: srv.URL, CaseID: 4, Match: "ARD-CV-26-0977"},
	}

	p := newTestPoller(t, nil, 5, time.Minute)
	var joined atomic.Int64
	p.testHookJoined = func(string) {
		if joined.Add(1) == int64(len(sources)) {
			close(release)
		}
	}

	result := p.PollAll(context.Background(), sources)

	if got := requests.Load(); got != 1 {
		t.Errorf("made %d http requests, want 1", got)
	}
	served := map[outcome]int{}
	for _, o := range result.Outcomes {
		if o.Err != nil {
			t.Fatalf("source %s failed: %v", o.Source, o.Err)
		}
		served[o.Served]++
	}
	if served[servedNetwork] != 1 || served[servedShared] != 1 {
		t.Errorf("outcomes = %v, want one network and one shared", served)
	}

	if len(result.Deadlines) != 2 {
		t.Fatalf("got %d deadlines, want 2 after match filtering: %+v", len(result.Deadlines), result.Deadlines)
	}
	want := map[string]int{
		"mendez-court-calendar:ard-1":   3,
		"oakridge-court-calendar:ard-2": 4,
	}
	for _, d := range result.Deadlines {
		caseID, ok := want[d.ExternalID]
		if !ok {
			t.Errorf("unexpected row %s", d.ExternalID)
			continue
		}
		if d.CaseID != caseID {
			t.Errorf("%s landed on case %d, want %d", d.ExternalID, d.CaseID, caseID)
		}
		if d.Source != kindCalendar {
			t.Errorf("%s has source %q, want %q", d.ExternalID, d.Source, kindCalendar)
		}
	}
}

func TestPollAllServesTheSecondRunFromCache(t *testing.T) {
	const ttl = 5 * time.Minute

	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprint(w, docketBody("dkt-1", "filing", time.Now()))
	}))
	t.Cleanup(srv.Close)

	sources := []Source{
		{ID: "harlow-docket", Kind: kindDocket, URL: srv.URL + "/harlow", CaseID: 1},
		{ID: "kestrel-docket", Kind: kindDocket, URL: srv.URL + "/kestrel", CaseID: 2},
	}

	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	p := newTestPoller(t, nil, 5, ttl)
	p.cache.now = func() time.Time { return time.Unix(0, clock.Load()) }

	first := p.PollAll(context.Background(), sources)
	if got := requests.Load(); got != 2 {
		t.Fatalf("first run made %d requests, want 2", got)
	}
	for _, o := range first.Outcomes {
		if o.Served != servedNetwork {
			t.Errorf("%s served from %s on the first run", o.Source, o.Served)
		}
	}

	second := p.PollAll(context.Background(), sources)
	if got := requests.Load(); got != 2 {
		t.Errorf("second run made %d more requests, want none", got-2)
	}
	for _, o := range second.Outcomes {
		if o.Served != servedCache {
			t.Errorf("%s served from %s within the ttl, want cache", o.Source, o.Served)
		}
	}

	clock.Store(clock.Load() + int64(2*ttl))
	third := p.PollAll(context.Background(), sources)
	if got := requests.Load(); got != 4 {
		t.Errorf("made %d requests in total, want 4 once the ttl lapsed", got)
	}
	for _, o := range third.Outcomes {
		if o.Served != servedNetwork {
			t.Errorf("%s served from %s after the ttl lapsed", o.Source, o.Served)
		}
	}
}

func TestPollAllIsolatesFailuresAndNeverCachesThem(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/broken":
			w.WriteHeader(http.StatusInternalServerError)
		case "/garbage":
			_, _ = fmt.Fprint(w, "{not json at all")
		default:
			_, _ = fmt.Fprint(w, docketBody("dkt-1", "filing", time.Now()))
		}
	}))
	t.Cleanup(srv.Close)

	sources := []Source{
		{ID: "broken-docket", Kind: kindDocket, URL: srv.URL + "/broken", CaseID: 1},
		{ID: "garbage-docket", Kind: kindDocket, URL: srv.URL + "/garbage", CaseID: 2},
		{ID: "healthy-docket", Kind: kindDocket, URL: srv.URL + "/healthy", CaseID: 3},
	}

	p := newTestPoller(t, nil, 5, time.Minute)
	result := p.PollAll(context.Background(), sources)

	failed := map[string]bool{}
	for _, o := range result.Outcomes {
		if o.Err != nil {
			failed[o.Source] = true
		}
	}
	if !failed["broken-docket"] || !failed["garbage-docket"] {
		t.Errorf("expected both bad sources to fail, got %v", failed)
	}
	if failed["healthy-docket"] {
		t.Error("a failing source took the healthy one down with it")
	}
	if len(result.Deadlines) != 1 {
		t.Errorf("got %d deadlines, want only the healthy source's", len(result.Deadlines))
	}

	p.PollAll(context.Background(), sources)
	if got := requests.Load(); got != 5 {
		t.Errorf("made %d requests over two runs, want 5: the failures must not be cached", got)
	}
}

func TestPollAllReturnsPromptlyWhenCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	sources := make([]Source, 6)
	for i := range sources {
		sources[i] = Source{
			ID:     fmt.Sprintf("feed-%d", i),
			Kind:   kindDocket,
			URL:    fmt.Sprintf("%s/?feed=%d", srv.URL, i),
			CaseID: 1,
		}
	}

	p := newTestPoller(t, nil, 2, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan PollResult, 1)
	go func() { done <- p.PollAll(ctx, sources) }()

	select {
	case result := <-done:
		for _, o := range result.Outcomes {
			if o.Err == nil {
				t.Errorf("%s succeeded against a cancelled context", o.Source)
			}
		}
		if len(result.Deadlines) != 0 {
			t.Errorf("got %d deadlines from a cancelled poll", len(result.Deadlines))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PollAll did not return after its context was cancelled")
	}
}

// countingWriter records how many times a run reached the database.
type countingWriter struct {
	mu       sync.Mutex
	polls    int
	lastRows []PolledDeadline
}

func (w *countingWriter) ExistingCaseIDs(_ context.Context, ids []int) (map[int]bool, error) {
	existing := make(map[int]bool, len(ids))
	for _, id := range ids {
		existing[id] = true
	}
	return existing, nil
}

func (w *countingWriter) UpsertDeadlines(_ context.Context, rows []PolledDeadline) (UpsertCounts, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.polls++
	w.lastRows = rows
	return UpsertCounts{Inserted: len(rows)}, nil
}

func TestSyncMergesOverlappingRuns(t *testing.T) {
	const callers = 5

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Holds the run that won the race until every caller has attached to
		// it, so the rest are followers rather than a second run.
		<-release
		_, _ = fmt.Fprint(w, docketBody("dkt-1", "filing", time.Now()))
	}))
	t.Cleanup(srv.Close)

	writer := &countingWriter{}
	p := newTestPoller(t, writer, 5, time.Minute)
	p.sources = []Source{{ID: "harlow-docket", Kind: kindDocket, URL: srv.URL, CaseID: 1}}

	var joined atomic.Int64
	p.testHookJoined = func(key string) {
		if key == syncKey && joined.Add(1) == callers {
			close(release)
		}
	}

	summaries := make([]SyncSummary, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			summary, err := p.Sync(context.Background())
			if err != nil {
				t.Errorf("caller %d: %v", i, err)
				return
			}
			summaries[i] = summary
		})
	}
	wg.Wait()

	if writer.polls != 1 {
		t.Errorf("wrote %d times, want 1: overlapping calls must share a run", writer.polls)
	}
	for i, summary := range summaries {
		if summary.Items != 1 || summary.Inserted != 1 {
			t.Errorf("caller %d got %+v, want the shared run's summary", i, summary)
		}
	}
}

func TestSyncDropsRowsWhoseCaseIsGone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, docketBody("dkt-1", "filing", time.Now()))
	}))
	t.Cleanup(srv.Close)

	writer := &missingCaseWriter{present: 1}
	p := newTestPoller(t, writer, 5, time.Minute)
	p.sources = []Source{
		{ID: "harlow-docket", Kind: kindDocket, URL: srv.URL + "/harlow", CaseID: 1},
		{ID: "deleted-docket", Kind: kindDocket, URL: srv.URL + "/deleted", CaseID: 99},
	}

	summary, err := p.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if summary.Items != 2 {
		t.Errorf("items = %d, want both rows counted before the drop", summary.Items)
	}
	if summary.Inserted != 1 {
		t.Errorf("inserted = %d, want only the row whose case exists", summary.Inserted)
	}
	if len(summary.Errors) != 1 || summary.Errors[0].Source != "deleted-docket" {
		t.Fatalf("errors = %+v, want one against deleted-docket", summary.Errors)
	}
	for _, row := range writer.written {
		if row.CaseID == 99 {
			t.Error("a row naming a deleted case reached the batch")
		}
	}
}

type missingCaseWriter struct {
	present int
	written []PolledDeadline
}

func (w *missingCaseWriter) ExistingCaseIDs(_ context.Context, ids []int) (map[int]bool, error) {
	existing := make(map[int]bool, len(ids))
	for _, id := range ids {
		existing[id] = id == w.present
	}
	return existing, nil
}

func (w *missingCaseWriter) UpsertDeadlines(_ context.Context, rows []PolledDeadline) (UpsertCounts, error) {
	w.written = rows
	return UpsertCounts{Inserted: len(rows)}, nil
}

package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	ics "github.com/arran4/golang-ical"
	"github.com/go-chi/chi/v5"
)

// The demo feeds stand in for a court's docket and calendar exports. Content
// is generated per request and anchored to today, so the dashboard never goes
// stale, and it is stable within a day, so a second poll reports unchanged.
//
// Each feed answers slowly on purpose. Without the delay the whole point of
// polling in parallel is invisible in the run summary.

type mockEntry struct {
	uid    string
	title  string
	day    int
	hour   int
	minute int
	allDay bool
	tzid   string
}

type mockFeed struct {
	court        string
	docketNumber string
	latency      time.Duration
	entries      []mockEntry
}

var mockDockets = map[string]mockFeed{
	"harlow.json": {
		court:        "D. Mass.",
		docketNumber: "1:26-cv-10482",
		latency:      300 * time.Millisecond,
		entries: []mockEntry{
			{uid: "dkt-22", title: "Rule 26(f) report filed", day: -3, hour: 17},
			{uid: "dkt-28", title: "Expert report exchange", day: 5, hour: 17},
			{uid: "dkt-31", title: "Opposition to motion to dismiss due", day: 13, hour: 17},
			{uid: "dkt-34", title: "Reply brief due", day: 20, hour: 17},
		},
	},
	"kestrel.json": {
		court:        "Bankr. D. Del.",
		docketNumber: "26-10774",
		latency:      380 * time.Millisecond,
		entries: []mockEntry{
			{uid: "bk-11", title: "Disclosure statement hearing", day: 2, hour: 10},
			{uid: "bk-14", title: "Claims bar date", day: 9, hour: 17},
			{uid: "bk-17", title: "Plan objection deadline", day: 16, hour: 17},
		},
	},
	"mendez.json": {
		court:        "Arden County Superior Court",
		docketNumber: "ARD-CV-26-0412",
		latency:      260 * time.Millisecond,
		entries: []mockEntry{
			{uid: "civ-07", title: "Answer to amended complaint due", day: 4, hour: 17},
			{uid: "civ-09", title: "Discovery cutoff", day: 18, hour: 17},
		},
	},
	"oakridge.json": {
		court:        "Arden County Superior Court",
		docketNumber: "ARD-CV-26-0977",
		latency:      420 * time.Millisecond,
		entries: []mockEntry{
			{uid: "civ-21", title: "Response to motion to compel", day: 4, hour: 14},
			{uid: "civ-25", title: "Building inspection report due", day: 11, hour: 17},
		},
	},
}

var mockCalendars = map[string]mockFeed{
	"harlow-mediation.ics": {
		latency: 340 * time.Millisecond,
		entries: []mockEntry{
			{uid: "med-1", title: "Pre-mediation call", day: 2, hour: 15},
			{uid: "med-2", title: "Mediation session with Brightline", day: 5, hour: 9, minute: 30},
		},
	},
	"kestrel-scheduling.ics": {
		latency: 450 * time.Millisecond,
		entries: []mockEntry{
			{uid: "sch-1", title: "Scheduling conference", day: 9, hour: 11},
			{uid: "sch-2", title: "341 meeting of creditors", day: 14, allDay: true},
		},
	},
	// Two cases watch this one calendar, and a third case's hearing is here to
	// give the match filter something to exclude.
	"arden-superior.ics": {
		latency: 290 * time.Millisecond,
		entries: []mockEntry{
			{uid: "ard-1", title: "Status conference ARD-CV-26-0412 Mendez", day: 4, hour: 9, tzid: "America/New_York"},
			{uid: "ard-2", title: "Motion hearing ARD-CV-26-0977 Oakridge Tenants", day: 11, hour: 10},
			{uid: "ard-3", title: "Trial call ARD-CV-26-0311 Sandoval", day: 7, hour: 9},
		},
	},
}

func mockFeedRoutes(loc *time.Location, now func() time.Time) chi.Router {
	r := chi.NewRouter()
	r.Get("/docket/{file}", serveMock(mockDockets, loc, now, writeDocket))
	r.Get("/calendar/{file}", serveMock(mockCalendars, loc, now, writeCalendar))
	return r
}

func serveMock(feeds map[string]mockFeed, loc *time.Location, now func() time.Time,
	write func(http.ResponseWriter, mockFeed, time.Time, *time.Location) error,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		feed, ok := feeds[chi.URLParam(r, "file")]
		if !ok {
			writeError(w, http.StatusNotFound, "no such feed")
			return
		}

		// Waiting on the request context instead of sleeping means shutdown
		// isn't held up by a feed nobody is reading any more.
		select {
		case <-time.After(feed.latency):
		case <-r.Context().Done():
			return
		}

		if err := write(w, feed, startOfDay(now(), loc), loc); err != nil {
			slog.Error("write mock feed", "err", err)
		}
	}
}

func startOfDay(now time.Time, loc *time.Location) time.Time {
	y, m, d := now.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

func (e mockEntry) due(today time.Time, loc *time.Location) time.Time {
	day := today.AddDate(0, 0, e.day)
	return time.Date(day.Year(), day.Month(), day.Day(), e.hour, e.minute, 0, 0, loc)
}

func writeDocket(w http.ResponseWriter, feed mockFeed, today time.Time, loc *time.Location) error {
	out := struct {
		Court        string        `json:"court"`
		DocketNumber string        `json:"docket_number"`
		Entries      []docketEntry `json:"entries"`
	}{Court: feed.court, DocketNumber: feed.docketNumber}

	for _, entry := range feed.entries {
		out.Entries = append(out.Entries, docketEntry{
			UID:   entry.uid,
			Title: entry.title,
			Due:   entry.due(today, loc).Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(out)
}

func writeCalendar(w http.ResponseWriter, feed mockFeed, today time.Time, loc *time.Location) error {
	const (
		dateLayout  = "20060102"
		localLayout = "20060102T150405"
	)

	cal := ics.NewCalendar()
	cal.SetProductId("-//DocketIQ//mock feed//EN")

	for _, entry := range feed.entries {
		event := cal.AddEvent(entry.uid)
		event.SetProperty(ics.ComponentPropertySummary, entry.title)
		due := entry.due(today, loc)

		switch {
		case entry.allDay:
			event.SetProperty(ics.ComponentPropertyDtStart, due.Format(dateLayout),
				&ics.KeyValues{Key: "VALUE", Value: []string{"DATE"}})
		case entry.tzid != "":
			event.SetProperty(ics.ComponentPropertyDtStart, due.Format(localLayout),
				&ics.KeyValues{Key: "TZID", Value: []string{entry.tzid}})
		default:
			event.SetProperty(ics.ComponentPropertyDtStart, due.UTC().Format(localLayout)+"Z")
		}
	}

	w.Header().Set("Content-Type", "text/calendar")
	return cal.SerializeTo(w)
}

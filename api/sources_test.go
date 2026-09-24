package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseDocket(t *testing.T) {
	body := `{
		"court": "D. Mass.",
		"docket_number": "1:26-cv-10482",
		"clerk": "an unknown field the parser ignores",
		"entries": [
			{"uid": "dkt-31", "title": "Opposition to motion to dismiss due", "due": "2026-10-06T17:00:00-04:00"},
			{"uid": "", "title": "no uid", "due": "2026-10-06T17:00:00-04:00"},
			{"uid": "dkt-32", "title": "", "due": "2026-10-06T17:00:00-04:00"},
			{"uid": "dkt-33", "title": "no due date"},
			{"uid": "dkt-34", "title": "unparseable due", "due": "next Tuesday"}
		]
	}`

	items, err := parseDocket(strings.NewReader(body), "harlow-docket")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want only the complete one: %+v", len(items), items)
	}
	if items[0].UID != "dkt-31" {
		t.Errorf("uid = %q", items[0].UID)
	}
	if want := time.Date(2026, 10, 6, 21, 0, 0, 0, time.UTC); !items[0].Due.Equal(want) {
		t.Errorf("due = %s, want %s", items[0].Due, want)
	}
}

func TestParseDocketRejectsGarbage(t *testing.T) {
	if _, err := parseDocket(strings.NewReader("{not json"), "harlow-docket"); err == nil {
		t.Error("expected an error for a malformed body")
	}
}

func calendarWith(events ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n" +
		strings.Join(events, "") + "END:VCALENDAR\r\n"
}

func event(lines ...string) string {
	return "BEGIN:VEVENT\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VEVENT\r\n"
}

func TestParseCalendar(t *testing.T) {
	body := calendarWith(
		event("UID:utc-1", "SUMMARY:Hearing in UTC", "DTSTART:20261006T210000Z"),
		event("UID:tzid-1", "SUMMARY:Hearing with a zone", "DTSTART;TZID=America/New_York:20261006T170000"),
		event("UID:float-1", "SUMMARY:Floating hearing", "DTSTART:20261006T170000"),
		event("UID:allday-1", "SUMMARY:341 meeting of creditors", "DTSTART;VALUE=DATE:20261006"),
		event("SUMMARY:No uid at all", "DTSTART:20261006T210000Z"),
		event("UID:nostart-1", "SUMMARY:No start at all"),
	)

	items, err := parseCalendar(strings.NewReader(body), "arden-superior", nyc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("got %d items, want 4: %+v", len(items), items)
	}

	byUID := map[string]FeedItem{}
	for _, item := range items {
		byUID[item.UID] = item
	}

	// The first three are the same instant written three ways.
	sameInstant := time.Date(2026, 10, 6, 21, 0, 0, 0, time.UTC)
	for _, uid := range []string{"utc-1", "tzid-1", "float-1"} {
		if got := byUID[uid].Due; !got.Equal(sameInstant) {
			t.Errorf("%s due = %s, want %s", uid, got, sameInstant)
		}
	}

	allDay := byUID["allday-1"].Due.In(nyc)
	if allDay.Hour() != allDayHour || allDay.Minute() != allDayMinute {
		t.Errorf("all-day event due at %s, want %02d:%02d local", allDay, allDayHour, allDayMinute)
	}
	if y, m, d := allDay.Date(); y != 2026 || m != time.October || d != 6 {
		t.Errorf("all-day event landed on %s", allDay)
	}
}

func TestParseCalendarIgnoresRecurrence(t *testing.T) {
	body := calendarWith(event(
		"UID:weekly-1",
		"SUMMARY:Weekly status call",
		"DTSTART:20261006T210000Z",
		"RRULE:FREQ=WEEKLY;COUNT=10",
	))

	items, err := parseCalendar(strings.NewReader(body), "arden-superior", nyc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("got %d items, want only the first occurrence", len(items))
	}
}

func TestSourceMatchFiltersByTitle(t *testing.T) {
	src := Source{Match: "ARD-CV-26-0412"}
	tests := map[string]bool{
		"Status conference ARD-CV-26-0412 Mendez": true,
		"status conference ard-cv-26-0412 mendez": true,
		"Motion hearing ARD-CV-26-0977 Oakridge":  false,
		"Trial call ARD-CV-26-0311 Sandoval":      false,
	}
	for title, want := range tests {
		if got := src.keep(FeedItem{Title: title}); got != want {
			t.Errorf("keep(%q) = %v, want %v", title, got, want)
		}
	}
	if !(Source{}).keep(FeedItem{Title: "anything"}) {
		t.Error("a source without a match should keep everything")
	}
}

func writeSources(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sources.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestLoadSourcesReportsEveryProblemAtOnce(t *testing.T) {
	path := writeSources(t, `[
		{"id": "harlow-docket",  "kind": "docket",   "url": "http://feeds.test/a.json", "case_id": 1},
		{"id": "harlow-docket",  "kind": "docket",   "url": "http://feeds.test/b.json", "case_id": 2},
		{"id": "bad-kind",       "kind": "email",    "url": "http://feeds.test/c.json", "case_id": 3},
		{"id": "bad-url",        "kind": "calendar", "url": "ftp://feeds.test/d.ics",   "case_id": 4},
		{"id": "Bad_ID",         "kind": "calendar", "url": "http://feeds.test/e.ics",  "case_id": 5},
		{"id": "bad-case",       "kind": "docket",   "url": "http://feeds.test/f.json", "case_id": 0}
	]`)

	_, err := loadSources(path)
	if err == nil {
		t.Fatal("expected the load to fail")
	}

	for _, want := range []string{
		`"harlow-docket": id is already used`,
		`"bad-kind": kind "email" is not docket or calendar`,
		`"bad-url": url "ftp://feeds.test/d.ics" is not an http or https address`,
		`"Bad_ID": id may only hold lowercase letters, digits and hyphens`,
		`"bad-case": case_id 0 is not a positive integer`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q:\n%v", want, err)
		}
	}
}

func TestLoadSourcesAcceptsTheShippedFile(t *testing.T) {
	sources, err := loadSources("sources.json")
	if err != nil {
		t.Fatalf("the demo sources should load: %v", err)
	}
	if len(sources) != 8 {
		t.Errorf("got %d sources, want 8", len(sources))
	}

	urls := map[string]bool{}
	for _, src := range sources {
		urls[src.URL] = true
	}
	if len(urls) != 7 {
		t.Errorf("got %d distinct urls, want 7 so one is shared", len(urls))
	}
}

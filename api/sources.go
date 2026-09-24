package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	ics "github.com/arran4/golang-ical"
)

const (
	kindDocket   = "docket"
	kindCalendar = "calendar"

	// All-day events carry no time. End of day is the safe reading for a
	// filing deadline, since anything earlier would invent urgency.
	allDayHour   = 23
	allDayMinute = 59
)

var sourceIDPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// Source is one feed to poll, bound to the case its entries belong to.
type Source struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	URL    string `json:"url"`
	CaseID int    `json:"case_id"`
	Match  string `json:"match,omitempty"`
}

// keep reports whether an item belongs to this source. Two cases in the same
// court share one hearing calendar, and Match is how each keeps only its own
// entries.
func (s Source) keep(item FeedItem) bool {
	if s.Match == "" {
		return true
	}
	return strings.Contains(strings.ToLower(item.Title), strings.ToLower(s.Match))
}

// FeedItem is one parsed entry before it's attached to a case.
type FeedItem struct {
	UID   string
	Title string
	Due   time.Time
}

// loadSources reads and validates the whole file, reporting every problem at
// once rather than stopping at the first.
func loadSources(path string) ([]Source, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read sources: %w", err)
	}
	var sources []Source
	if err := json.Unmarshal(raw, &sources); err != nil {
		return nil, fmt.Errorf("parse sources: %w", err)
	}

	seen := make(map[string]bool, len(sources))
	problems := make([]error, 0)
	for i, src := range sources {
		where := fmt.Sprintf("source %d", i)
		if src.ID != "" {
			where = fmt.Sprintf("source %q", src.ID)
		}
		for _, err := range validate(src, seen) {
			problems = append(problems, fmt.Errorf("%s: %w", where, err))
		}
		seen[src.ID] = true
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	return sources, nil
}

func validate(src Source, seen map[string]bool) []error {
	var problems []error
	switch {
	case src.ID == "":
		problems = append(problems, errors.New("id is required"))
	case !sourceIDPattern.MatchString(src.ID):
		problems = append(problems, errors.New("id may only hold lowercase letters, digits and hyphens"))
	case seen[src.ID]:
		problems = append(problems, errors.New("id is already used"))
	}
	if src.Kind != kindDocket && src.Kind != kindCalendar {
		problems = append(problems, fmt.Errorf("kind %q is not docket or calendar", src.Kind))
	}
	parsed, err := url.Parse(src.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		problems = append(problems, fmt.Errorf("url %q is not an http or https address", src.URL))
	}
	if src.CaseID < 1 {
		problems = append(problems, fmt.Errorf("case_id %d is not a positive integer", src.CaseID))
	}
	return problems
}

type docketFeed struct {
	Entries []docketEntry `json:"entries"`
}

type docketEntry struct {
	UID   string `json:"uid"`
	Title string `json:"title"`
	Due   string `json:"due"`
}

// parseDocket reads the JSON docket shape. An entry missing a field is skipped
// rather than failing the source, because one malformed row shouldn't cost the
// rest of the feed.
func parseDocket(r io.Reader, sourceID string) ([]FeedItem, error) {
	var feed docketFeed
	if err := json.NewDecoder(r).Decode(&feed); err != nil {
		return nil, fmt.Errorf("decode docket: %w", err)
	}

	items := make([]FeedItem, 0, len(feed.Entries))
	for _, entry := range feed.Entries {
		due, err := time.Parse(time.RFC3339, entry.Due)
		if entry.UID == "" || entry.Title == "" || err != nil {
			slog.Warn("skipping docket entry", "source", sourceID, "uid", entry.UID)
			continue
		}
		items = append(items, FeedItem{UID: entry.UID, Title: entry.Title, Due: due})
	}
	return items, nil
}

// parseCalendar reads VEVENTs. RRULE is ignored, so a recurring event counts
// only once, at its first occurrence.
func parseCalendar(r io.Reader, sourceID string, loc *time.Location) ([]FeedItem, error) {
	cal, err := ics.ParseCalendar(r)
	if err != nil {
		return nil, fmt.Errorf("decode calendar: %w", err)
	}

	events := cal.Events()
	items := make([]FeedItem, 0, len(events))
	for _, event := range events {
		uid := propertyValue(event, ics.ComponentPropertyUniqueId)
		start := event.GetProperty(ics.ComponentPropertyDtStart)
		if uid == "" || start == nil {
			slog.Warn("skipping calendar event", "source", sourceID, "uid", uid)
			continue
		}
		due, err := parseDTStart(start, loc)
		if err != nil {
			slog.Warn("skipping calendar event", "source", sourceID, "uid", uid, "err", err)
			continue
		}
		items = append(items, FeedItem{
			UID:   uid,
			Title: propertyValue(event, ics.ComponentPropertySummary),
			Due:   due,
		})
	}
	return items, nil
}

func propertyValue(event *ics.VEvent, name ics.ComponentProperty) string {
	prop := event.GetProperty(name)
	if prop == nil {
		return ""
	}
	return prop.Value
}

// parseDTStart covers the four forms a DTSTART takes. A floating time carries
// no zone at all, so it can only mean the zone the app runs in.
func parseDTStart(start *ics.IANAProperty, loc *time.Location) (time.Time, error) {
	const (
		dateLayout  = "20060102"
		localLayout = "20060102T150405"
		utcLayout   = "20060102T150405Z"
	)

	if hasParam(start, "VALUE", "DATE") {
		day, err := time.ParseInLocation(dateLayout, start.Value, loc)
		if err != nil {
			return time.Time{}, err
		}
		return time.Date(day.Year(), day.Month(), day.Day(), allDayHour, allDayMinute, 0, 0, loc), nil
	}
	if strings.HasSuffix(start.Value, "Z") {
		return time.ParseInLocation(utcLayout, start.Value, time.UTC)
	}
	if tzid := paramValue(start, "TZID"); tzid != "" {
		zone, err := time.LoadLocation(tzid)
		if err != nil {
			return time.Time{}, fmt.Errorf("unknown TZID %q: %w", tzid, err)
		}
		return time.ParseInLocation(localLayout, start.Value, zone)
	}
	return time.ParseInLocation(localLayout, start.Value, loc)
}

func paramValue(prop *ics.IANAProperty, key string) string {
	if values := prop.ICalParameters[key]; len(values) > 0 {
		return values[0]
	}
	return ""
}

func hasParam(prop *ics.IANAProperty, key, want string) bool {
	return strings.EqualFold(paramValue(prop, key), want)
}

package main

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"time"
)

const (
	bucketOverdue  = "overdue"
	bucketThisWeek = "this_week"
	bucketLater    = "later"

	sortDue     = "due"
	sortUrgency = "urgency"

	maxWindow = 720 * time.Hour
)

// Ranked is a deadline with the fields the API computes on every response.
type Ranked struct {
	Deadline
	DaysUntilDue int          `json:"days_until_due"`
	Urgency      urgencyScore `json:"urgency"`
	Bucket       string       `json:"bucket"`
}

// Conflict is a pair of deadlines close enough together to clash, ordered so
// that A is not after B.
type Conflict struct {
	A          Ranked `json:"a"`
	B          Ranked `json:"b"`
	GapMinutes int    `json:"gap_minutes"`
}

// urgencyScore carries the full score so sorting stays exact, and rounds to
// four places only on the way out, where the extra digits are noise.
type urgencyScore float64

func (u urgencyScore) MarshalJSON() ([]byte, error) {
	rounded := math.Round(float64(u)*10000) / 10000
	return []byte(strconv.FormatFloat(rounded, 'f', -1, 64)), nil
}

// rank attaches the day count, score and bucket to each deadline. Both times
// are read in loc, and nothing here looks at the clock or the database.
func rank(deadlines []Deadline, now time.Time, loc *time.Location) []Ranked {
	ranked := make([]Ranked, len(deadlines))
	for i, d := range deadlines {
		days := daysUntilDue(now, d.DueDate, loc)
		ranked[i] = Ranked{
			Deadline:     d,
			DaysUntilDue: days,
			Urgency:      urgency(days),
			Bucket:       bucketOf(now, d.DueDate, days),
		}
	}
	return ranked
}

// daysUntilDue counts calendar days in loc: 0 for later today, 1 for tomorrow
// at any hour, negative once the day has passed.
func daysUntilDue(now, due time.Time, loc *time.Location) int {
	// Count calendar days, not 24-hour blocks. due.Sub(now) gets this wrong
	// across DST changes and calls 1am tomorrow "0 days" at 11pm tonight.
	// Rebuilding both dates as UTC midnights makes the division exact.
	y, m, d := now.In(loc).Date()
	from := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	y, m, d = due.In(loc).Date()
	to := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return int(to.Sub(from) / (24 * time.Hour))
}

func urgency(days int) urgencyScore {
	// 1/(d+1) divides by zero at d = -1 and goes negative after that, so
	// overdue counts as due today. The due-date tie-break still puts the
	// oldest first.
	return urgencyScore(1 / float64(max(days, 0)+1))
}

func bucketOf(now, due time.Time, days int) string {
	// Overdue is by instant, not by day: a 9:00 deadline is overdue at 9:01.
	if due.Before(now) {
		return bucketOverdue
	}
	if days <= 6 {
		return bucketThisWeek
	}
	return bucketLater
}

func sortRanked(ranked []Ranked, order string) {
	if order == sortUrgency {
		slices.SortFunc(ranked, func(a, b Ranked) int {
			return cmp.Or(
				cmp.Compare(b.Urgency, a.Urgency),
				a.DueDate.Compare(b.DueDate),
				cmp.Compare(a.ID, b.ID),
			)
		})
		return
	}
	slices.SortFunc(ranked, func(a, b Ranked) int {
		return cmp.Or(a.DueDate.Compare(b.DueDate), cmp.Compare(a.ID, b.ID))
	})
}

// findConflicts reports every pair of deadlines that clash, in one forward
// sweep over the sorted slice.
//
// A window of zero means same-day mode: two deadlines clash when they fall on
// the same calendar date in loc. Otherwise they clash when they are at most
// window apart. Deadlines due before today are dropped first, since a clash
// that has already passed isn't actionable.
func findConflicts(ranked []Ranked, now time.Time, loc *time.Location, window time.Duration) []Conflict {
	y, m, d := now.In(loc).Date()
	startOfToday := time.Date(y, m, d, 0, 0, 0, 0, loc)

	upcoming := make([]Ranked, 0, len(ranked))
	for _, r := range ranked {
		if !r.DueDate.Before(startOfToday) {
			upcoming = append(upcoming, r)
		}
	}
	slices.SortFunc(upcoming, func(a, b Ranked) int {
		return cmp.Or(a.DueDate.Compare(b.DueDate), cmp.Compare(a.ID, b.ID))
	})

	conflicts := []Conflict{}
	for i := range upcoming {
		// Stopping at the neighbour would miss the first and third when three
		// deadlines share a day, so keep walking until we leave the window.
		// Sorted input means the first miss ends the walk.
		for j := i + 1; j < len(upcoming); j++ {
			if !inRange(upcoming[i], upcoming[j], loc, window) {
				break
			}
			conflicts = append(conflicts, Conflict{
				A:          upcoming[i],
				B:          upcoming[j],
				GapMinutes: int(upcoming[j].DueDate.Sub(upcoming[i].DueDate).Minutes()),
			})
		}
	}
	return conflicts
}

func inRange(a, b Ranked, loc *time.Location, window time.Duration) bool {
	if window == 0 {
		ay, am, ad := a.DueDate.In(loc).Date()
		by, bm, bd := b.DueDate.In(loc).Date()
		return ay == by && am == bm && ad == bd
	}
	return b.DueDate.Sub(a.DueDate) <= window
}

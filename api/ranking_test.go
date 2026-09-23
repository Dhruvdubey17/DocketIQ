package main

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// 1 November 2026 is the day US clocks go back, so any test spanning it also
// covers the 25-hour day.
var nyc = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(err)
	}
	return loc
}()

func at(t *testing.T, layout string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation("2006-01-02 15:04", layout, nyc)
	if err != nil {
		t.Fatalf("parse %q: %v", layout, err)
	}
	return parsed
}

func deadlineAt(id int, due time.Time) Deadline {
	return Deadline{
		ID:        id,
		CaseID:    1,
		CaseTitle: "Harlow v. Brightline Freight",
		Title:     "filing",
		DueDate:   due,
		Source:    sourceManual,
	}
}

func TestDaysUntilDue(t *testing.T) {
	tests := []struct {
		name string
		now  string
		due  string
		want int
	}{
		{"later today", "2026-10-06 09:00", "2026-10-06 17:00", 0},
		{"tomorrow at 1am from late tonight", "2026-11-10 23:30", "2026-11-11 01:00", 1},
		{"yesterday", "2026-10-06 09:00", "2026-10-05 17:00", -1},
		{"across the clock change is one day, not 25 hours", "2026-10-31 12:00", "2026-11-01 12:00", 1},
		{"two days across the clock change", "2026-10-31 12:00", "2026-11-02 12:00", 2},
		{"local date differs from the utc date", "2026-11-10 20:00", "2026-11-11 09:00", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := daysUntilDue(at(t, tt.now), at(t, tt.due), nyc)
			if got != tt.want {
				t.Errorf("daysUntilDue = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestUrgency(t *testing.T) {
	tests := []struct {
		name string
		days int
		want float64
	}{
		{"three days overdue clamps to one", -3, 1},
		{"one day overdue clamps to one", -1, 1},
		{"due today", 0, 1},
		{"due tomorrow", 1, 0.5},
		{"due in six days", 6, 1.0 / 7.0},
		{"due in a week", 7, 0.125},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := float64(urgency(tt.days)); got != tt.want {
				t.Errorf("urgency(%d) = %v, want %v", tt.days, got, tt.want)
			}
		})
	}
}

func TestUrgencyRoundsOnlyWhenEncoded(t *testing.T) {
	score := urgency(14)
	encoded, err := score.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, want := string(encoded), "0.0667"; got != want {
		t.Errorf("encoded urgency = %s, want %s", got, want)
	}
	if float64(score) == 0.0667 {
		t.Error("the in-memory score was rounded; sorting must see full precision")
	}
}

func TestBucketOf(t *testing.T) {
	now := at(t, "2026-10-06 09:00")
	tests := []struct {
		name string
		due  string
		want string
	}{
		{"one minute overdue", "2026-10-06 08:59", bucketOverdue},
		{"later today", "2026-10-06 23:00", bucketThisWeek},
		{"exactly six days out", "2026-10-12 09:00", bucketThisWeek},
		{"exactly seven days out", "2026-10-13 09:00", bucketLater},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			due := at(t, tt.due)
			got := bucketOf(now, due, daysUntilDue(now, due, nyc))
			if got != tt.want {
				t.Errorf("bucketOf = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSortRanked(t *testing.T) {
	now := at(t, "2026-10-06 09:00")

	t.Run("urgency puts the oldest overdue first", func(t *testing.T) {
		ranked := rank([]Deadline{
			deadlineAt(1, at(t, "2026-10-06 17:00")),
			deadlineAt(2, at(t, "2026-10-04 17:00")),
			deadlineAt(3, at(t, "2026-10-20 17:00")),
		}, now, nyc)
		sortRanked(ranked, sortUrgency)
		if got, want := ids(ranked), []int{2, 1, 3}; !cmp.Equal(got, want) {
			t.Errorf("order = %v, want %v", got, want)
		}
	})

	t.Run("identical due dates fall back to id", func(t *testing.T) {
		same := at(t, "2026-10-09 17:00")
		ranked := rank([]Deadline{
			deadlineAt(7, same),
			deadlineAt(3, same),
			deadlineAt(5, same),
		}, now, nyc)
		sortRanked(ranked, sortUrgency)
		if got, want := ids(ranked), []int{3, 5, 7}; !cmp.Equal(got, want) {
			t.Errorf("order = %v, want %v", got, want)
		}
	})

	t.Run("due orders by date then id", func(t *testing.T) {
		ranked := rank([]Deadline{
			deadlineAt(9, at(t, "2026-10-20 17:00")),
			deadlineAt(2, at(t, "2026-10-04 17:00")),
			deadlineAt(1, at(t, "2026-10-20 17:00")),
		}, now, nyc)
		sortRanked(ranked, sortDue)
		if got, want := ids(ranked), []int{2, 1, 9}; !cmp.Equal(got, want) {
			t.Errorf("order = %v, want %v", got, want)
		}
	})
}

func TestFindConflicts(t *testing.T) {
	now := at(t, "2026-10-06 08:00")

	tests := []struct {
		name   string
		due    []string
		window time.Duration
		want   [][2]int
	}{
		{
			name: "none when every deadline is on its own day",
			due:  []string{"2026-10-06 09:00", "2026-10-08 09:00", "2026-10-10 09:00"},
			want: nil,
		},
		{
			name: "one pair on one day",
			due:  []string{"2026-10-08 09:00", "2026-10-08 14:00", "2026-10-10 09:00"},
			want: [][2]int{{1, 2}},
		},
		{
			name: "three on one day gives three pairs",
			due:  []string{"2026-10-08 09:00", "2026-10-08 12:00", "2026-10-08 16:00"},
			want: [][2]int{{1, 2}, {1, 3}, {2, 3}},
		},
		{
			name: "unsorted input still pairs correctly",
			due:  []string{"2026-10-08 16:00", "2026-10-08 09:00", "2026-10-08 12:00"},
			want: [][2]int{{2, 3}, {2, 1}, {3, 1}},
		},
		{
			name: "deadlines before today are ignored",
			due:  []string{"2026-10-04 09:00", "2026-10-04 15:00"},
			want: nil,
		},
		{
			name: "same utc date but different local dates do not conflict",
			due:  []string{"2026-11-10 20:00", "2026-11-11 10:00"},
			want: nil,
		},
		{
			name:   "window just inside",
			due:    []string{"2026-10-08 09:00", "2026-10-09 20:00"},
			window: 36 * time.Hour,
			want:   [][2]int{{1, 2}},
		},
		{
			name:   "window just outside",
			due:    []string{"2026-10-08 09:00", "2026-10-09 22:00"},
			window: 36 * time.Hour,
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deadlines := make([]Deadline, len(tt.due))
			for i, due := range tt.due {
				deadlines[i] = deadlineAt(i+1, at(t, due))
			}
			conflicts := findConflicts(rank(deadlines, now, nyc), now, nyc, tt.window)

			got := [][2]int{}
			for _, c := range conflicts {
				got = append(got, [2]int{c.A.ID, c.B.ID})
			}
			want := tt.want
			if want == nil {
				want = [][2]int{}
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("pairs (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFindConflictsReportsTheGap(t *testing.T) {
	now := at(t, "2026-10-06 08:00")
	ranked := rank([]Deadline{
		deadlineAt(1, at(t, "2026-10-08 09:00")),
		deadlineAt(2, at(t, "2026-10-08 09:45")),
	}, now, nyc)

	conflicts := findConflicts(ranked, now, nyc, 0)
	if len(conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1", len(conflicts))
	}
	if got := conflicts[0].GapMinutes; got != 45 {
		t.Errorf("gap = %d minutes, want 45", got)
	}
}

func ids(ranked []Ranked) []int {
	out := make([]int, len(ranked))
	for i, r := range ranked {
		out[i] = r.ID
	}
	return out
}

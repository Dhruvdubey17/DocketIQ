package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeStore keeps the handler tests away from Postgres. It mirrors the rules
// the real store enforces: unknown ids are errNotFound, polled rows are
// errReadOnly.
type fakeStore struct {
	cases     map[int]Case
	deadlines map[int]Deadline
	nextID    int
	pingErr   error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		cases:     map[int]Case{1: {ID: 1, Title: "Harlow v. Brightline Freight", CreatedAt: testNow}},
		deadlines: map[int]Deadline{},
		nextID:    100,
	}
}

func (f *fakeStore) add(d Deadline) Deadline {
	f.nextID++
	d.ID = f.nextID
	d.CaseTitle = f.cases[d.CaseID].Title
	if d.Source == "" {
		d.Source = sourceManual
	}
	f.deadlines[d.ID] = d
	return d
}

func (f *fakeStore) ListCases(context.Context) ([]Case, error) {
	out := []Case{}
	for _, c := range f.cases {
		c.DeadlineCount = 0
		for _, d := range f.deadlines {
			if d.CaseID == c.ID {
				c.DeadlineCount++
			}
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Case) int { return strings.Compare(a.Title, b.Title) })
	return out, nil
}

func (f *fakeStore) CreateCase(_ context.Context, title string, deadlines []NewDeadline) (Case, []Deadline, error) {
	f.nextID++
	created := Case{ID: f.nextID, Title: title, CreatedAt: testNow, DeadlineCount: len(deadlines)}
	f.cases[created.ID] = created

	added := make([]Deadline, 0, len(deadlines))
	for _, d := range deadlines {
		added = append(added, f.add(Deadline{CaseID: created.ID, Title: d.Title, DueDate: d.DueDate, CreatedAt: testNow}))
	}
	return created, added, nil
}

func (f *fakeStore) DeleteCase(_ context.Context, id int) error {
	if _, ok := f.cases[id]; !ok {
		return errNotFound
	}
	delete(f.cases, id)
	return nil
}

func (f *fakeStore) CreateDeadline(_ context.Context, caseID int, d NewDeadline) (Deadline, error) {
	if _, ok := f.cases[caseID]; !ok {
		return Deadline{}, errNotFound
	}
	return f.add(Deadline{CaseID: caseID, Title: d.Title, DueDate: d.DueDate, CreatedAt: testNow}), nil
}

func (f *fakeStore) ListDeadlines(context.Context) ([]Deadline, error) {
	out := []Deadline{}
	for _, d := range f.deadlines {
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b Deadline) int { return a.ID - b.ID })
	return out, nil
}

func (f *fakeStore) UpdateDeadline(_ context.Context, id int, title *string, due *time.Time) (Deadline, error) {
	d, ok := f.deadlines[id]
	if !ok {
		return Deadline{}, errNotFound
	}
	if d.Source != sourceManual {
		return Deadline{}, errReadOnly
	}
	if title != nil {
		d.Title = *title
	}
	if due != nil {
		d.DueDate = *due
	}
	f.deadlines[id] = d
	return d, nil
}

func (f *fakeStore) DeleteDeadline(_ context.Context, id int) error {
	d, ok := f.deadlines[id]
	if !ok {
		return errNotFound
	}
	if d.Source != sourceManual {
		return errReadOnly
	}
	delete(f.deadlines, id)
	return nil
}

func (f *fakeStore) Ping(context.Context) error { return f.pingErr }

var testNow = time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)

func newTestServer(t *testing.T) (*fakeStore, http.Handler) {
	t.Helper()
	st := newFakeStore()
	return st, newServer(st, nyc, func() time.Time { return testNow }).routes()
}

func do(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return out
}

func checkStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, want, strings.TrimSpace(rec.Body.String()))
	}
}

func TestHealthz(t *testing.T) {
	st, h := newTestServer(t)

	rec := do(t, h, http.MethodGet, "/healthz", "")
	checkStatus(t, rec, http.StatusOK)
	if got := decode[map[string]string](t, rec)["status"]; got != "ok" {
		t.Errorf("status = %q, want ok", got)
	}

	st.pingErr = errors.New("connection refused")
	checkStatus(t, do(t, h, http.MethodGet, "/healthz", ""), http.StatusServiceUnavailable)
}

func TestListCases(t *testing.T) {
	st, h := newTestServer(t)
	st.add(Deadline{CaseID: 1, Title: "filing", DueDate: testNow, CreatedAt: testNow})

	rec := do(t, h, http.MethodGet, "/api/cases", "")
	checkStatus(t, rec, http.StatusOK)

	body := decode[casesResponse](t, rec)
	if len(body.Cases) != 1 {
		t.Fatalf("got %d cases, want 1", len(body.Cases))
	}
	if body.Cases[0].DeadlineCount != 1 {
		t.Errorf("deadline_count = %d, want 1", body.Cases[0].DeadlineCount)
	}
}

func TestCreateCase(t *testing.T) {
	t.Run("with deadlines in request order", func(t *testing.T) {
		_, h := newTestServer(t)
		rec := do(t, h, http.MethodPost, "/api/cases", `{
			"title": "Mendez v. City of Arden",
			"deadlines": [
				{"title": "Second", "due_date": "2026-10-20T17:00:00-04:00"},
				{"title": "First", "due_date": "2026-10-07T17:00:00-04:00"}
			]
		}`)
		checkStatus(t, rec, http.StatusCreated)

		body := decode[createCaseResponse](t, rec)
		if body.Case.DeadlineCount != 2 {
			t.Errorf("deadline_count = %d, want 2", body.Case.DeadlineCount)
		}
		if got := []string{body.Deadlines[0].Title, body.Deadlines[1].Title}; got[0] != "Second" || got[1] != "First" {
			t.Errorf("order = %v, want request order", got)
		}
		if body.Deadlines[1].Bucket != bucketThisWeek {
			t.Errorf("bucket = %s, want %s", body.Deadlines[1].Bucket, bucketThisWeek)
		}
	})

	t.Run("without deadlines", func(t *testing.T) {
		_, h := newTestServer(t)
		rec := do(t, h, http.MethodPost, "/api/cases", `{"title": "In re Kestrel Pharmaceuticals"}`)
		checkStatus(t, rec, http.StatusCreated)
		if got := decode[createCaseResponse](t, rec).Case.DeadlineCount; got != 0 {
			t.Errorf("deadline_count = %d, want 0", got)
		}
	})

	t.Run("rejects bad input", func(t *testing.T) {
		cases := map[string]string{
			"empty title":       `{"title": "   "}`,
			"missing due_date":  `{"title": "ok", "deadlines": [{"title": "no date"}]}`,
			"unknown field":     `{"title": "ok", "colour": "red"}`,
			"unknown nested":    `{"title": "ok", "deadlines": [{"title": "x", "due_date": "2026-10-07T17:00:00Z", "note": "y"}]}`,
			"oversized title":   `{"title": "` + strings.Repeat("x", maxTitleRunes+1) + `"}`,
			"too many deadline": `{"title": "ok", "deadlines": [` + strings.TrimSuffix(strings.Repeat(`{"title":"x","due_date":"2026-10-07T17:00:00Z"},`, maxCaseDeadlines+1), ",") + `]}`,
		}
		for name, body := range cases {
			t.Run(name, func(t *testing.T) {
				_, h := newTestServer(t)
				checkStatus(t, do(t, h, http.MethodPost, "/api/cases", body), http.StatusBadRequest)
			})
		}
	})
}

func TestDeleteCase(t *testing.T) {
	_, h := newTestServer(t)
	checkStatus(t, do(t, h, http.MethodDelete, "/api/cases/1", ""), http.StatusNoContent)
	checkStatus(t, do(t, h, http.MethodDelete, "/api/cases/1", ""), http.StatusNotFound)
	checkStatus(t, do(t, h, http.MethodDelete, "/api/cases/0", ""), http.StatusBadRequest)
	checkStatus(t, do(t, h, http.MethodDelete, "/api/cases/abc", ""), http.StatusBadRequest)
}

func TestCreateDeadline(t *testing.T) {
	_, h := newTestServer(t)
	body := `{"title": "Initial disclosures due", "due_date": "2026-10-07T17:00:00-04:00"}`

	rec := do(t, h, http.MethodPost, "/api/cases/1/deadlines", body)
	checkStatus(t, rec, http.StatusCreated)
	created := decode[Ranked](t, rec)
	if created.CaseTitle != "Harlow v. Brightline Freight" {
		t.Errorf("case_title = %q", created.CaseTitle)
	}
	if created.Source != sourceManual {
		t.Errorf("source = %q, want manual", created.Source)
	}

	checkStatus(t, do(t, h, http.MethodPost, "/api/cases/999/deadlines", body), http.StatusNotFound)
}

func TestListDeadlines(t *testing.T) {
	st, h := newTestServer(t)
	overdue := st.add(Deadline{CaseID: 1, Title: "overdue", DueDate: testNow.Add(-48 * time.Hour), CreatedAt: testNow})
	later := st.add(Deadline{CaseID: 1, Title: "later", DueDate: testNow.Add(30 * 24 * time.Hour), CreatedAt: testNow})

	t.Run("defaults to due order", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/api/deadlines", "")
		checkStatus(t, rec, http.StatusOK)
		body := decode[deadlinesResponse](t, rec)
		if body.Deadlines[0].ID != overdue.ID {
			t.Errorf("first = %d, want the overdue one (%d)", body.Deadlines[0].ID, overdue.ID)
		}
		if body.Deadlines[0].Bucket != bucketOverdue {
			t.Errorf("bucket = %s, want %s", body.Deadlines[0].Bucket, bucketOverdue)
		}
	})

	t.Run("urgency order", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/api/deadlines?sort=urgency", "")
		checkStatus(t, rec, http.StatusOK)
		body := decode[deadlinesResponse](t, rec)
		if body.Deadlines[0].ID != overdue.ID || body.Deadlines[1].ID != later.ID {
			t.Errorf("order = %d,%d", body.Deadlines[0].ID, body.Deadlines[1].ID)
		}
	})

	t.Run("rejects an unknown sort", func(t *testing.T) {
		checkStatus(t, do(t, h, http.MethodGet, "/api/deadlines?sort=alphabetical", ""), http.StatusBadRequest)
	})
}

func TestConflicts(t *testing.T) {
	st, h := newTestServer(t)
	day := time.Date(2026, 10, 8, 13, 0, 0, 0, time.UTC)
	st.add(Deadline{CaseID: 1, Title: "hearing", DueDate: day, CreatedAt: testNow})
	st.add(Deadline{CaseID: 1, Title: "filing", DueDate: day.Add(45 * time.Minute), CreatedAt: testNow})

	t.Run("same day is the default mode", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/api/deadlines/conflicts", "")
		checkStatus(t, rec, http.StatusOK)
		body := decode[conflictsResponse](t, rec)
		if body.Mode != "same_day" {
			t.Errorf("mode = %q, want same_day", body.Mode)
		}
		if body.WindowMinutes != nil {
			t.Error("window_minutes should be absent in same-day mode")
		}
		if len(body.Conflicts) != 1 || body.Conflicts[0].GapMinutes != 45 {
			t.Fatalf("conflicts = %+v", body.Conflicts)
		}
	})

	t.Run("window mode reports its size", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/api/deadlines/conflicts?window=48h", "")
		checkStatus(t, rec, http.StatusOK)
		body := decode[conflictsResponse](t, rec)
		if body.Mode != "window" {
			t.Errorf("mode = %q, want window", body.Mode)
		}
		if body.WindowMinutes == nil || *body.WindowMinutes != 2880 {
			t.Errorf("window_minutes = %v, want 2880", body.WindowMinutes)
		}
	})

	t.Run("rejects a bad window", func(t *testing.T) {
		for _, raw := range []string{"nonsense", "0h", "-1h", "721h"} {
			checkStatus(t, do(t, h, http.MethodGet, "/api/deadlines/conflicts?window="+raw, ""), http.StatusBadRequest)
		}
	})
}

func TestUpdateDeadline(t *testing.T) {
	st, h := newTestServer(t)
	manual := st.add(Deadline{CaseID: 1, Title: "filing", DueDate: testNow, CreatedAt: testNow})
	polled := st.add(Deadline{CaseID: 1, Title: "hearing", DueDate: testNow, Source: "docket", CreatedAt: testNow})

	rec := do(t, h, http.MethodPatch, deadlinePath(manual.ID), `{"title": "Amended filing"}`)
	checkStatus(t, rec, http.StatusOK)
	if got := decode[Ranked](t, rec).Title; got != "Amended filing" {
		t.Errorf("title = %q", got)
	}

	checkStatus(t, do(t, h, http.MethodPatch, deadlinePath(manual.ID), `{}`), http.StatusBadRequest)
	checkStatus(t, do(t, h, http.MethodPatch, deadlinePath(manual.ID), `{"title": "  "}`), http.StatusBadRequest)
	checkStatus(t, do(t, h, http.MethodPatch, "/api/deadlines/9999", `{"title": "x"}`), http.StatusNotFound)
	checkStatus(t, do(t, h, http.MethodPatch, deadlinePath(polled.ID), `{"title": "x"}`), http.StatusConflict)
}

func TestDeleteDeadline(t *testing.T) {
	st, h := newTestServer(t)
	manual := st.add(Deadline{CaseID: 1, Title: "filing", DueDate: testNow, CreatedAt: testNow})
	polled := st.add(Deadline{CaseID: 1, Title: "hearing", DueDate: testNow, Source: "calendar", CreatedAt: testNow})

	checkStatus(t, do(t, h, http.MethodDelete, deadlinePath(manual.ID), ""), http.StatusNoContent)
	checkStatus(t, do(t, h, http.MethodDelete, deadlinePath(manual.ID), ""), http.StatusNotFound)
	checkStatus(t, do(t, h, http.MethodDelete, deadlinePath(polled.ID), ""), http.StatusConflict)
}

func TestErrorBodyShape(t *testing.T) {
	_, h := newTestServer(t)
	rec := do(t, h, http.MethodGet, "/api/deadlines?sort=alphabetical", "")
	checkStatus(t, rec, http.StatusBadRequest)
	if got := decode[map[string]string](t, rec)["error"]; got == "" {
		t.Error("error body is missing the error field")
	}
}

func deadlinePath(id int) string {
	return "/api/deadlines/" + strconv.Itoa(id)
}

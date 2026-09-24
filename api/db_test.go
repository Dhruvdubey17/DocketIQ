//go:build integration

package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startDB brings up the real schema on a throwaway Postgres, so these tests
// check the constraints rather than a Go-side imitation of them.
func startDB(t *testing.T) *DB {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("docketiq"),
		postgres.WithUsername("docketiq"),
		postgres.WithPassword("docketiq"),
		postgres.WithInitScripts(filepath.Join("..", "db", "schema.sql")),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	db, err := NewDB(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestSchemaConstraints(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()

	created, _, err := db.CreateCase(ctx, "Harlow v. Brightline Freight", nil)
	if err != nil {
		t.Fatalf("create case: %v", err)
	}

	t.Run("source is restricted to the three known values", func(t *testing.T) {
		_, err := db.pool.Exec(ctx,
			`INSERT INTO deadlines (case_id, title, due_date, source) VALUES ($1, $2, now(), 'email')`,
			created.ID, "from an email")
		if err == nil {
			t.Fatal("expected the source check to reject 'email'")
		}
	})

	t.Run("external_id is unique but many manual rows stay null", func(t *testing.T) {
		insert := `INSERT INTO deadlines (case_id, title, due_date, source, external_id) VALUES ($1, $2, now(), $3, $4)`
		if _, err := db.pool.Exec(ctx, insert, created.ID, "polled", "docket", "harlow-docket:dkt-31"); err != nil {
			t.Fatalf("first polled row: %v", err)
		}
		if _, err := db.pool.Exec(ctx, insert, created.ID, "polled again", "docket", "harlow-docket:dkt-31"); err == nil {
			t.Fatal("expected the unique constraint to reject a repeated external_id")
		}
		for i := range 3 {
			if _, err := db.pool.Exec(ctx, insert, created.ID, "manual", sourceManual, nil); err != nil {
				t.Fatalf("manual row %d: %v", i, err)
			}
		}
	})
}

func TestDeleteCaseCascades(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()

	created, added, err := db.CreateCase(ctx, "In re Kestrel Pharmaceuticals", []NewDeadline{
		{Title: "Proof of claim filing", DueDate: time.Now().Add(72 * time.Hour)},
		{Title: "Plan objection", DueDate: time.Now().Add(96 * time.Hour)},
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	if len(added) != 2 {
		t.Fatalf("created %d deadlines, want 2", len(added))
	}

	if err := db.DeleteCase(ctx, created.ID); err != nil {
		t.Fatalf("delete case: %v", err)
	}
	var remaining int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM deadlines WHERE case_id = $1`, created.ID).Scan(&remaining); err != nil {
		t.Fatalf("count deadlines: %v", err)
	}
	if remaining != 0 {
		t.Errorf("%d deadlines survived the case", remaining)
	}
	if err := db.DeleteCase(ctx, created.ID); !errors.Is(err, errNotFound) {
		t.Errorf("second delete returned %v, want errNotFound", err)
	}
}

func TestCreateCaseRollsBackOnBadDeadline(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()

	// Postgres rejects a NUL byte in a text value, which fails the insert
	// after the case row already exists in the transaction.
	_, _, err := db.CreateCase(ctx, "Mendez v. City of Arden", []NewDeadline{
		{Title: "Expert witness disclosure", DueDate: time.Now().Add(24 * time.Hour)},
		{Title: "bad\x00title", DueDate: time.Now().Add(48 * time.Hour)},
	})
	if err == nil {
		t.Fatal("expected the bad deadline to fail the insert")
	}

	cases, err := db.ListCases(ctx)
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	if len(cases) != 0 {
		t.Errorf("%d cases survived a rolled-back create", len(cases))
	}
}

func TestListDeadlinesSkipsRowsWithoutACase(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()

	created, _, err := db.CreateCase(ctx, "Oakridge Tenants Assn. v. Pell Properties", []NewDeadline{
		{Title: "Response to habitability motion", DueDate: time.Now().Add(24 * time.Hour)},
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	if _, err := db.pool.Exec(ctx,
		`INSERT INTO deadlines (case_id, title, due_date) VALUES (NULL, 'orphan', now())`); err != nil {
		t.Fatalf("insert orphan: %v", err)
	}

	deadlines, err := db.ListDeadlines(ctx)
	if err != nil {
		t.Fatalf("list deadlines: %v", err)
	}
	if len(deadlines) != 1 || deadlines[0].CaseID != created.ID {
		t.Fatalf("got %d deadlines, want only the joined one", len(deadlines))
	}
}

func TestTimestampsComeBackInUTC(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()

	// A due date sent with an offset has to round-trip as the same instant in
	// UTC, because that is what the API promises callers.
	due := time.Date(2026, 10, 6, 17, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
	created, added, err := db.CreateCase(ctx, "Harlow v. Brightline Freight", []NewDeadline{
		{Title: "Opposition to motion to dismiss due", DueDate: due},
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}

	for name, got := range map[string]time.Time{
		"case created_at":     created.CreatedAt,
		"deadline created_at": added[0].CreatedAt,
		"deadline due_date":   added[0].DueDate,
	} {
		if got.Location() != time.UTC {
			t.Errorf("%s is in %s, want UTC", name, got.Location())
		}
	}
	if !added[0].DueDate.Equal(due) {
		t.Errorf("due_date = %s, want the same instant as %s", added[0].DueDate, due)
	}

	listed, err := db.ListDeadlines(ctx)
	if err != nil {
		t.Fatalf("list deadlines: %v", err)
	}
	if listed[0].DueDate.Location() != time.UTC {
		t.Errorf("listed due_date is in %s, want UTC", listed[0].DueDate.Location())
	}
}

func TestUpsertReportsInsertedThenUnchangedThenUpdated(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()

	created, _, err := db.CreateCase(ctx, "Harlow v. Brightline Freight", nil)
	if err != nil {
		t.Fatalf("create case: %v", err)
	}

	due := time.Date(2026, 10, 6, 21, 0, 0, 0, time.UTC)
	rows := []PolledDeadline{
		{
			CaseID:     created.ID,
			Title:      "Opposition to motion to dismiss due",
			DueDate:    due,
			Source:     kindDocket,
			ExternalID: "harlow-docket:dkt-31",
		},
		{
			CaseID:     created.ID,
			Title:      "Reply brief due",
			DueDate:    due.Add(14 * 24 * time.Hour),
			Source:     kindDocket,
			ExternalID: "harlow-docket:dkt-34",
		},
	}

	first, err := db.UpsertDeadlines(ctx, rows)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if first != (UpsertCounts{Inserted: 2}) {
		t.Errorf("first run = %+v, want two inserts", first)
	}

	second, err := db.UpsertDeadlines(ctx, rows)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if second != (UpsertCounts{Unchanged: 2}) {
		t.Errorf("second run = %+v, want two unchanged", second)
	}

	// A court moves one hearing and leaves the other alone.
	rows[0].DueDate = due.Add(48 * time.Hour)
	third, err := db.UpsertDeadlines(ctx, rows)
	if err != nil {
		t.Fatalf("third upsert: %v", err)
	}
	if third != (UpsertCounts{Updated: 1, Unchanged: 1}) {
		t.Errorf("third run = %+v, want one update and one unchanged", third)
	}

	var total int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM deadlines`).Scan(&total); err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 2 {
		t.Errorf("%d rows after three polls, want 2", total)
	}

	listed, err := db.ListDeadlines(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if listed[0].Source != kindDocket {
		t.Errorf("source = %q, want %q", listed[0].Source, kindDocket)
	}
}

func TestExistingCaseIDs(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()

	created, _, err := db.CreateCase(ctx, "In re Kestrel Pharmaceuticals", nil)
	if err != nil {
		t.Fatalf("create case: %v", err)
	}

	existing, err := db.ExistingCaseIDs(ctx, []int{created.ID, 9999})
	if err != nil {
		t.Fatalf("existing case ids: %v", err)
	}
	if !existing[created.ID] {
		t.Errorf("case %d should exist", created.ID)
	}
	if existing[9999] {
		t.Error("case 9999 should not exist")
	}
}

func TestUpsertRollsBackOnABadRow(t *testing.T) {
	db := startDB(t)
	ctx := context.Background()

	created, _, err := db.CreateCase(ctx, "Mendez v. City of Arden", nil)
	if err != nil {
		t.Fatalf("create case: %v", err)
	}

	due := time.Date(2026, 10, 6, 21, 0, 0, 0, time.UTC)
	_, err = db.UpsertDeadlines(ctx, []PolledDeadline{
		{CaseID: created.ID, Title: "Answer due", DueDate: due, Source: kindDocket, ExternalID: "mendez-docket:civ-07"},
		{CaseID: created.ID, Title: "Discovery cutoff", DueDate: due, Source: "email", ExternalID: "mendez-docket:civ-09"},
	})
	if err == nil {
		t.Fatal("expected the source check to reject the second row")
	}

	var total int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM deadlines`).Scan(&total); err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 0 {
		t.Errorf("%d rows survived a rolled-back batch, want 0", total)
	}
}

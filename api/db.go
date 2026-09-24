package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errNotFound = errors.New("not found")
	errReadOnly = errors.New("deadline comes from a feed and can't be edited")
)

// foreignKeyViolation is Postgres SQLSTATE 23503, raised when a deadline names
// a case that doesn't exist.
const foreignKeyViolation = "23503"

const sourceManual = "manual"

// Case is one matter, with the number of deadlines attached to it.
type Case struct {
	ID            int       `json:"id" db:"id"`
	Title         string    `json:"title" db:"title"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
	DeadlineCount int       `json:"deadline_count" db:"deadline_count"`
}

// Deadline is a stored deadline joined to its case title. The ranking fields
// the API also returns are computed per request and live on Ranked.
type Deadline struct {
	ID        int       `json:"id" db:"id"`
	CaseID    int       `json:"case_id" db:"case_id"`
	CaseTitle string    `json:"case_title" db:"case_title"`
	Title     string    `json:"title" db:"title"`
	DueDate   time.Time `json:"due_date" db:"due_date"`
	Source    string    `json:"source" db:"source"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// NewDeadline is a manual deadline before it has an id or a case.
type NewDeadline struct {
	Title   string
	DueDate time.Time
}

// DB is the Postgres-backed store the handlers use.
type DB struct {
	pool *pgxpool.Pool
}

func NewDB(ctx context.Context, url string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	// Without a scan location pgx hands timestamptz back in the process's
	// local zone, and the API answers in UTC. This changes the representation
	// only, never the instant.
	cfg.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		conn.TypeMap().RegisterType(&pgtype.Type{
			Name:  "timestamptz",
			OID:   pgtype.TimestamptzOID,
			Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
		})
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return &DB{pool: pool}, nil
}

func (db *DB) Close() { db.pool.Close() }

func (db *DB) Ping(ctx context.Context) error { return db.pool.Ping(ctx) }

const listCasesSQL = `
SELECT c.id, c.title, c.created_at, count(d.id) AS deadline_count
FROM cases c
LEFT JOIN deadlines d ON d.case_id = c.id
GROUP BY c.id
ORDER BY c.title`

func (db *DB) ListCases(ctx context.Context) ([]Case, error) {
	rows, err := db.pool.Query(ctx, listCasesSQL)
	if err != nil {
		return nil, fmt.Errorf("list cases: %w", err)
	}
	cases, err := pgx.CollectRows(rows, pgx.RowToStructByName[Case])
	if err != nil {
		return nil, fmt.Errorf("list cases: %w", err)
	}
	return cases, nil
}

const insertCaseSQL = `
INSERT INTO cases (title)
VALUES ($1)
RETURNING id, title, created_at`

func (db *DB) CreateCase(ctx context.Context, title string, deadlines []NewDeadline) (Case, []Deadline, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return Case{}, nil, fmt.Errorf("create case: %w", err)
	}
	// One transaction, so a bad deadline can't leave an empty case behind.
	defer func() { _ = tx.Rollback(ctx) }()

	var created Case
	if err := tx.QueryRow(ctx, insertCaseSQL, title).Scan(&created.ID, &created.Title, &created.CreatedAt); err != nil {
		return Case{}, nil, fmt.Errorf("create case: %w", err)
	}

	added := make([]Deadline, 0, len(deadlines))
	for _, d := range deadlines {
		row, err := insertDeadline(ctx, tx, created.ID, created.Title, d)
		if err != nil {
			return Case{}, nil, err
		}
		added = append(added, row)
	}
	created.DeadlineCount = len(added)

	if err := tx.Commit(ctx); err != nil {
		return Case{}, nil, fmt.Errorf("create case: %w", err)
	}
	return created, added, nil
}

const deleteCaseSQL = `DELETE FROM cases WHERE id = $1`

func (db *DB) DeleteCase(ctx context.Context, id int) error {
	tag, err := db.pool.Exec(ctx, deleteCaseSQL, id)
	if err != nil {
		return fmt.Errorf("delete case %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return nil
}

const insertDeadlineSQL = `
INSERT INTO deadlines (case_id, title, due_date)
VALUES ($1, $2, $3)
RETURNING id, case_id, title, due_date, source, created_at`

func insertDeadline(ctx context.Context, q pgx.Tx, caseID int, caseTitle string, d NewDeadline) (Deadline, error) {
	row := Deadline{CaseTitle: caseTitle}
	err := q.QueryRow(ctx, insertDeadlineSQL, caseID, d.Title, d.DueDate).
		Scan(&row.ID, &row.CaseID, &row.Title, &row.DueDate, &row.Source, &row.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
			return Deadline{}, errNotFound
		}
		return Deadline{}, fmt.Errorf("insert deadline: %w", err)
	}
	return row, nil
}

const caseTitleSQL = `SELECT title FROM cases WHERE id = $1`

func (db *DB) CreateDeadline(ctx context.Context, caseID int, d NewDeadline) (Deadline, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return Deadline{}, fmt.Errorf("create deadline: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var caseTitle string
	if err := tx.QueryRow(ctx, caseTitleSQL, caseID).Scan(&caseTitle); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Deadline{}, errNotFound
		}
		return Deadline{}, fmt.Errorf("create deadline: %w", err)
	}

	row, err := insertDeadline(ctx, tx, caseID, caseTitle, d)
	if err != nil {
		return Deadline{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Deadline{}, fmt.Errorf("create deadline: %w", err)
	}
	return row, nil
}

// The join is an inner one because every response carries case_title. It also
// means a row with a NULL case_id can never reach Deadline.CaseID, which is a
// plain int.
const listDeadlinesSQL = `
SELECT d.id, d.case_id, c.title AS case_title, d.title, d.due_date, d.source, d.created_at
FROM deadlines d
JOIN cases c ON c.id = d.case_id
ORDER BY d.due_date, d.id`

func (db *DB) ListDeadlines(ctx context.Context) ([]Deadline, error) {
	rows, err := db.pool.Query(ctx, listDeadlinesSQL)
	if err != nil {
		return nil, fmt.Errorf("list deadlines: %w", err)
	}
	deadlines, err := pgx.CollectRows(rows, pgx.RowToStructByName[Deadline])
	if err != nil {
		return nil, fmt.Errorf("list deadlines: %w", err)
	}
	return deadlines, nil
}

const getDeadlineSQL = `
SELECT d.id, d.case_id, c.title AS case_title, d.title, d.due_date, d.source, d.created_at
FROM deadlines d
JOIN cases c ON c.id = d.case_id
WHERE d.id = $1`

func (db *DB) getDeadline(ctx context.Context, id int) (Deadline, error) {
	rows, err := db.pool.Query(ctx, getDeadlineSQL, id)
	if err != nil {
		return Deadline{}, fmt.Errorf("get deadline %d: %w", id, err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Deadline])
	if errors.Is(err, pgx.ErrNoRows) {
		return Deadline{}, errNotFound
	}
	if err != nil {
		return Deadline{}, fmt.Errorf("get deadline %d: %w", id, err)
	}
	return row, nil
}

// The source test lives in the WHERE clause so there is no gap between
// checking a row and writing it. A NULL argument leaves that column alone,
// which is what makes PATCH partial.
const updateDeadlineSQL = `
WITH updated AS (
    UPDATE deadlines
    SET title    = COALESCE($2, title),
        due_date = COALESCE($3, due_date)
    WHERE id = $1 AND source = '` + sourceManual + `'
    RETURNING id, case_id, title, due_date, source, created_at
)
SELECT u.id, u.case_id, c.title AS case_title, u.title, u.due_date, u.source, u.created_at
FROM updated u
JOIN cases c ON c.id = u.case_id`

func (db *DB) UpdateDeadline(ctx context.Context, id int, title *string, due *time.Time) (Deadline, error) {
	rows, err := db.pool.Query(ctx, updateDeadlineSQL, id, title, due)
	if err != nil {
		return Deadline{}, fmt.Errorf("update deadline %d: %w", id, err)
	}
	updated, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Deadline])
	if errors.Is(err, pgx.ErrNoRows) {
		return Deadline{}, db.whyNotWritable(ctx, id)
	}
	if err != nil {
		return Deadline{}, fmt.Errorf("update deadline %d: %w", id, err)
	}
	return updated, nil
}

const deleteDeadlineSQL = `DELETE FROM deadlines WHERE id = $1 AND source = '` + sourceManual + `'`

func (db *DB) DeleteDeadline(ctx context.Context, id int) error {
	tag, err := db.pool.Exec(ctx, deleteDeadlineSQL, id)
	if err != nil {
		return fmt.Errorf("delete deadline %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return db.whyNotWritable(ctx, id)
	}
	return nil
}

// whyNotWritable separates a missing deadline from one a feed owns, and only
// runs when a conditional write matched nothing.
func (db *DB) whyNotWritable(ctx context.Context, id int) error {
	current, err := db.getDeadline(ctx, id)
	if err != nil {
		return err
	}
	if current.Source != sourceManual {
		return errReadOnly
	}
	return fmt.Errorf("deadline %d is manual but the write matched no row", id)
}

// UpsertCounts is what one batch of polled rows did to the table.
type UpsertCounts struct {
	Inserted  int
	Updated   int
	Unchanged int
}

const existingCaseIDsSQL = `SELECT id FROM cases WHERE id = ANY($1)`

func (db *DB) ExistingCaseIDs(ctx context.Context, ids []int) (map[int]bool, error) {
	rows, err := db.pool.Query(ctx, existingCaseIDsSQL, ids)
	if err != nil {
		return nil, fmt.Errorf("existing case ids: %w", err)
	}
	found, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return nil, fmt.Errorf("existing case ids: %w", err)
	}

	existing := make(map[int]bool, len(found))
	for _, id := range found {
		existing[id] = true
	}
	return existing, nil
}

// The WHERE clause on the update is what makes an unchanged row report
// nothing at all, which separates a quiet poll from one that moved a date.
const upsertDeadlineSQL = `
INSERT INTO deadlines (case_id, title, due_date, source, external_id)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (external_id) DO UPDATE
SET case_id  = EXCLUDED.case_id,
    title    = EXCLUDED.title,
    due_date = EXCLUDED.due_date
WHERE (deadlines.case_id, deadlines.title, deadlines.due_date)
      IS DISTINCT FROM (EXCLUDED.case_id, EXCLUDED.title, EXCLUDED.due_date)
-- xmax is 0 only on rows this statement inserted, which separates inserts
-- from updates without a second query.
RETURNING (xmax = 0) AS inserted`

// UpsertDeadlines writes every polled row in one transaction, so a poll is all
// or nothing.
func (db *DB) UpsertDeadlines(ctx context.Context, rows []PolledDeadline) (UpsertCounts, error) {
	var counts UpsertCounts
	if len(rows) == 0 {
		return counts, nil
	}

	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return counts, fmt.Errorf("upsert deadlines: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	batch := &pgx.Batch{}
	for _, row := range rows {
		batch.Queue(upsertDeadlineSQL, row.CaseID, row.Title, row.DueDate, row.Source, row.ExternalID)
	}

	results := tx.SendBatch(ctx, batch)
	for range rows {
		var inserted bool
		switch err := results.QueryRow().Scan(&inserted); {
		case errors.Is(err, pgx.ErrNoRows):
			counts.Unchanged++
		case err != nil:
			_ = results.Close()
			return UpsertCounts{}, fmt.Errorf("upsert deadlines: %w", err)
		case inserted:
			counts.Inserted++
		default:
			counts.Updated++
		}
	}
	if err := results.Close(); err != nil {
		return UpsertCounts{}, fmt.Errorf("upsert deadlines: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return UpsertCounts{}, fmt.Errorf("upsert deadlines: %w", err)
	}
	return counts, nil
}

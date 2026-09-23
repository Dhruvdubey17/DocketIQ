CREATE TABLE cases (
    id          SERIAL PRIMARY KEY,
    title       TEXT NOT NULL,
    created_at  TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE deadlines (
    id          SERIAL PRIMARY KEY,
    case_id     INT REFERENCES cases(id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    due_date    TIMESTAMPTZ NOT NULL,
    source      TEXT DEFAULT 'manual'
                CHECK (source IN ('manual', 'docket', 'calendar')),
    -- "<source id>:<feed uid>" for polled rows, NULL for manual ones.
    external_id TEXT UNIQUE,
    created_at  TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_deadlines_due ON deadlines(due_date);

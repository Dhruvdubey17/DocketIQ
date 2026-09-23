-- Explicit ids because sources.json binds each feed to a case by number.
INSERT INTO cases (id, title) VALUES
    (1, 'Harlow v. Brightline Freight'),
    (2, 'In re Kestrel Pharmaceuticals'),
    (3, 'Mendez v. City of Arden'),
    (4, 'Oakridge Tenants Assn. v. Pell Properties');

SELECT setval('cases_id_seq', (SELECT max(id) FROM cases));

-- Offsets from now() rather than fixed dates, so a fresh volume always has
-- one overdue row and a spread across the buckets. These age; the mock feeds
-- are what keep the demo current.
INSERT INTO deadlines (case_id, title, due_date) VALUES
    (1, 'Rule 26(f) conference report', now() - INTERVAL '2 days'),
    (2, 'Proof of claim filing', now() + INTERVAL '3 days'),
    (3, 'Expert witness disclosure', now() + INTERVAL '5 days'),
    (4, 'Response to habitability motion', now() + INTERVAL '12 days'),
    (1, 'Settlement conference statement', now() + INTERVAL '21 days');

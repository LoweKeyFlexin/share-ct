-- Broaden Reaction eligibility to 6F without losing any historical submission.
-- The runner wraps this table rebuild in a transaction. Preserve all columns and IDs;
-- ON DELETE CASCADE to players and retry uniqueness remain unchanged.
CREATE TABLE submissions_016 (
    id              INTEGER PRIMARY KEY,
    player_id       TEXT    NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    client_id       TEXT    NOT NULL,                       -- RTScore.id: retries are idempotent
    source          TEXT    NOT NULL CHECK (source IN ('touch', 'pad', 'keyboard')),
    best_ms         REAL    NOT NULL CHECK (best_ms >= 100.0 AND best_ms <= 3333.34),
    avg_ms          REAL    NOT NULL,
    accuracy        REAL    NOT NULL CHECK (accuracy >= 0 AND accuracy <= 1),
    score           INTEGER NOT NULL CHECK (score BETWEEN 0 AND 2500),
    attempts_ms     TEXT    NOT NULL,                       -- JSON array of ms
    misfires        INTEGER NOT NULL CHECK (misfires >= 0),
    client_best_ms  REAL    NOT NULL,
    client_avg_ms   REAL    NOT NULL,
    client_accuracy REAL    NOT NULL,
    client_score    INTEGER NOT NULL,
    app_build       TEXT    NOT NULL,
    platform        TEXT    NOT NULL CHECK (platform IN ('ios', 'mac', 'windows', 'android')),
    device_model    TEXT,                                   -- "iPhone17,2"
    device_label    TEXT,                                   -- RTScore.device
    trace           TEXT,                                   -- option 3
    created_at      INTEGER NOT NULL,                       -- unix seconds
    UNIQUE (player_id, client_id)
);
INSERT INTO submissions_016 SELECT * FROM submissions;
DROP TABLE submissions;
ALTER TABLE submissions_016 RENAME TO submissions;
CREATE INDEX ix_sub_board ON submissions(source, score DESC, best_ms ASC, created_at);
CREATE INDEX ix_sub_player ON submissions(player_id, created_at DESC);
CREATE INDEX ix_sub_fastest ON submissions(source, best_ms ASC, score DESC, created_at);

-- 004_leaderboard_submissions: one row per submitted reaction trial (design brief §C).
-- The ranked columns (best_ms, avg_ms, accuracy, score) are the SERVER's recompute of
-- attempts_ms + misfires with the app's scoreTrial; the client_* columns keep what the
-- app claimed, for audit only, and are never ranked. trace stays NULL until input
-- traces ship (brief option 3). ON DELETE CASCADE, with foreign_keys on in the store's
-- DSN, makes DELETE /v1/players/{id} erase every submission with the player.
-- The best_ms CHECK is a backstop a hair outside the Go band check (10 to 200 frames),
-- so the two can never disagree at the boundary.
CREATE TABLE IF NOT EXISTS submissions (
    id              INTEGER PRIMARY KEY,
    player_id       TEXT    NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    client_id       TEXT    NOT NULL,                       -- RTScore.id: retries are idempotent
    source          TEXT    NOT NULL CHECK (source IN ('touch', 'pad', 'keyboard')),
    best_ms         REAL    NOT NULL CHECK (best_ms >= 166.66 AND best_ms <= 3333.34),
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
CREATE INDEX IF NOT EXISTS ix_sub_board  ON submissions(source, score DESC, best_ms ASC, created_at);
CREATE INDEX IF NOT EXISTS ix_sub_player ON submissions(player_id, created_at DESC);

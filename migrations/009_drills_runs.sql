-- Existing score submissions retain their best-only sharing scope. Only the
-- explicit /v1/drills/runs route may append to the new public Recent feed.
ALTER TABLE drill_receipts ADD COLUMN publishes_run INTEGER NOT NULL DEFAULT 0 CHECK(publishes_run IN (0,1));
CREATE TABLE drill_runs (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 player_id TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
 client_id TEXT NOT NULL,
 receipt_id INTEGER NOT NULL,
 body_hash TEXT NOT NULL,
 drill_id TEXT NOT NULL CHECK(drill_id IN ('whiff-punish','hit-confirm','anti-air','crosscut','drive-impact')),
 score INTEGER NOT NULL CHECK(score BETWEEN 0 AND 1000000),
 source TEXT NOT NULL CHECK(source IN ('touch','pad','keyboard')),
 app_build TEXT NOT NULL,
 platform TEXT NOT NULL CHECK(platform IN ('ios','mac','windows','android')),
 device_model TEXT NOT NULL,
 device_label TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 UNIQUE(player_id,client_id)
);
CREATE INDEX ix_drill_runs_recent ON drill_runs(drill_id,created_at DESC,id DESC);
CREATE INDEX ix_drill_runs_player ON drill_runs(player_id,id DESC);
CREATE INDEX ix_drill_runs_expiry ON drill_runs(created_at);
-- No backfill: a previous best-only submission did not authorize feed publication.

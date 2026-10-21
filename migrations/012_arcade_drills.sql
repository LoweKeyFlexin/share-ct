-- Arcade has independent PBs and explicit Recent publishing. No historical backfill.
CREATE TABLE arcade_bests (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 player_id TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
 client_id TEXT NOT NULL,
 receipt_id INTEGER NOT NULL,
 body_hash TEXT NOT NULL,
 drill_id TEXT NOT NULL CHECK(drill_id IN ('whiff-punish','hit-confirm','anti-air','crosscut','drive-impact')),
 score INTEGER NOT NULL CHECK(score BETWEEN 0 AND 1000000),
 enemies_defeated INTEGER NOT NULL CHECK(enemies_defeated BETWEEN 0 AND total_opponents),
 total_opponents INTEGER NOT NULL CHECK(total_opponents BETWEEN 12 AND 13),
 completed INTEGER NOT NULL CHECK(completed IN (0,1) AND completed=(enemies_defeated=total_opponents)),
 elapsed_ms INTEGER NOT NULL CHECK(elapsed_ms BETWEEN 1 AND 86400000),
 perfect_opponents INTEGER NOT NULL CHECK(perfect_opponents BETWEEN 0 AND enemies_defeated),
 hits INTEGER NOT NULL CHECK(hits BETWEEN enemies_defeated AND 1000000),
 source TEXT NOT NULL CHECK(source IN ('touch','pad','keyboard')),
 app_build TEXT NOT NULL,
 platform TEXT NOT NULL CHECK(platform IN ('ios','mac','windows','android')),
 device_model TEXT NOT NULL,
 device_label TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 publishes_run INTEGER NOT NULL CHECK(publishes_run IN (0,1)),
 UNIQUE(player_id,drill_id)
);
CREATE TABLE arcade_runs (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 player_id TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
 client_id TEXT NOT NULL,
 receipt_id INTEGER NOT NULL,
 body_hash TEXT NOT NULL,
 drill_id TEXT NOT NULL CHECK(drill_id IN ('whiff-punish','hit-confirm','anti-air','crosscut','drive-impact')),
 score INTEGER NOT NULL CHECK(score BETWEEN 0 AND 1000000),
 enemies_defeated INTEGER NOT NULL CHECK(enemies_defeated BETWEEN 0 AND total_opponents),
 total_opponents INTEGER NOT NULL CHECK(total_opponents BETWEEN 12 AND 13),
 completed INTEGER NOT NULL CHECK(completed IN (0,1) AND completed=(enemies_defeated=total_opponents)),
 elapsed_ms INTEGER NOT NULL CHECK(elapsed_ms BETWEEN 1 AND 86400000),
 perfect_opponents INTEGER NOT NULL CHECK(perfect_opponents BETWEEN 0 AND enemies_defeated),
 hits INTEGER NOT NULL CHECK(hits BETWEEN enemies_defeated AND 1000000),
 source TEXT NOT NULL CHECK(source IN ('touch','pad','keyboard')),
 app_build TEXT NOT NULL,
 platform TEXT NOT NULL CHECK(platform IN ('ios','mac','windows','android')),
 device_model TEXT NOT NULL,
 device_label TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 publishes_run INTEGER NOT NULL CHECK(publishes_run IN (0,1)),
 UNIQUE(player_id,client_id)
);
CREATE INDEX ix_arcade_board ON arcade_bests(drill_id,completed DESC,enemies_defeated DESC,elapsed_ms,score DESC);
CREATE INDEX ix_arcade_runs_recent ON arcade_runs(drill_id,created_at DESC,id DESC);
CREATE INDEX ix_arcade_runs_player ON arcade_runs(player_id,id DESC);
CREATE INDEX ix_arcade_runs_expiry ON arcade_runs(created_at);
CREATE TABLE arcade_receipts (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 player_id TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
 client_id TEXT NOT NULL,
 body_hash TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 publishes_run INTEGER NOT NULL CHECK(publishes_run IN (0,1)),
 UNIQUE(player_id,client_id)
);
CREATE INDEX ix_arcade_receipts_player ON arcade_receipts(player_id,id DESC);

-- A bounded shared arrival index enforces the consented cap across both modes,
-- including same-second arrivals whose per-table IDs are not comparable.
CREATE TABLE drill_feed_order (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 player_id TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
 mode TEXT NOT NULL CHECK(mode IN ('endless','arcade')),
 run_id INTEGER NOT NULL,
 created_at INTEGER NOT NULL,
 UNIQUE(mode,run_id)
);
INSERT INTO drill_feed_order(player_id,mode,run_id,created_at)
 SELECT player_id,'endless',id,created_at FROM drill_runs ORDER BY created_at,id;
CREATE INDEX ix_drill_feed_order_player ON drill_feed_order(player_id,id DESC);
CREATE INDEX ix_drill_feed_order_expiry ON drill_feed_order(created_at);

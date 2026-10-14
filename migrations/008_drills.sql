-- One personal best per player/drill; aggregate runs never retain event histories.
CREATE TABLE drill_bests (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 player_id TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
 drill_id TEXT NOT NULL CHECK(drill_id IN ('whiff-punish','hit-confirm','anti-air','crosscut','drive-impact')),
 client_id TEXT NOT NULL,
 score INTEGER NOT NULL CHECK(score BETWEEN 0 AND 1000000),
 source TEXT NOT NULL CHECK(source IN ('touch','pad','keyboard')),
 app_build TEXT NOT NULL,
 platform TEXT NOT NULL CHECK(platform IN ('ios','mac','windows','android')),
 device_model TEXT NOT NULL,
 device_label TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 UNIQUE(player_id,drill_id)
);
CREATE INDEX ix_drill_board ON drill_bests(drill_id,score DESC,created_at,id);
-- At most 100 receipts per player: repeats within that window are idempotent.
-- Older repeats still cannot duplicate a PB or alter the time of an equal score.
CREATE TABLE drill_receipts (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 player_id TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
 client_id TEXT NOT NULL,
 body_hash TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 UNIQUE(player_id,client_id)
);
CREATE INDEX ix_drill_receipt_player ON drill_receipts(player_id,id DESC);

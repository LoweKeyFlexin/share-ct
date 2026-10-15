-- Expand the existing category constraint without changing stored score identities.
-- The migration runner executes this entire rebuild atomically. Keep high-water
-- AUTOINCREMENT values even when the highest original row has already been pruned.
CREATE TEMP TABLE drill_sequence_010 AS
 SELECT name,seq FROM sqlite_sequence WHERE name IN ('drill_bests','drill_runs');

CREATE TABLE drill_bests_010 (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 player_id TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
 drill_id TEXT NOT NULL CHECK(drill_id IN ('whiff-punish','hit-confirm','anti-air','crosscut','drive-impact','mental-stack')),
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
INSERT INTO drill_bests_010 (id,player_id,drill_id,client_id,score,source,app_build,platform,device_model,device_label,created_at) SELECT id,player_id,drill_id,client_id,score,source,app_build,platform,device_model,device_label,created_at FROM drill_bests;
DROP TABLE drill_bests;
ALTER TABLE drill_bests_010 RENAME TO drill_bests;
CREATE INDEX ix_drill_board ON drill_bests(drill_id,score DESC,created_at,id);
UPDATE sqlite_sequence SET seq = MAX(seq, COALESCE(
 (SELECT seq FROM drill_sequence_010 WHERE name='drill_bests'), 0)) WHERE name='drill_bests';
INSERT INTO sqlite_sequence(name,seq)
 SELECT name,seq FROM drill_sequence_010 WHERE name='drill_bests'
 AND NOT EXISTS(SELECT 1 FROM sqlite_sequence WHERE name='drill_bests');

CREATE TABLE drill_runs_010 (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 player_id TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
 client_id TEXT NOT NULL,
 receipt_id INTEGER NOT NULL,
 body_hash TEXT NOT NULL,
 drill_id TEXT NOT NULL CHECK(drill_id IN ('whiff-punish','hit-confirm','anti-air','crosscut','drive-impact','mental-stack')),
 score INTEGER NOT NULL CHECK(score BETWEEN 0 AND 1000000),
 source TEXT NOT NULL CHECK(source IN ('touch','pad','keyboard')),
 app_build TEXT NOT NULL,
 platform TEXT NOT NULL CHECK(platform IN ('ios','mac','windows','android')),
 device_model TEXT NOT NULL,
 device_label TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 UNIQUE(player_id,client_id)
);
INSERT INTO drill_runs_010 (id,player_id,client_id,receipt_id,body_hash,drill_id,score,source,app_build,platform,device_model,device_label,created_at) SELECT id,player_id,client_id,receipt_id,body_hash,drill_id,score,source,app_build,platform,device_model,device_label,created_at FROM drill_runs;
DROP TABLE drill_runs;
ALTER TABLE drill_runs_010 RENAME TO drill_runs;
CREATE INDEX ix_drill_runs_recent ON drill_runs(drill_id,created_at DESC,id DESC);
CREATE INDEX ix_drill_runs_player ON drill_runs(player_id,id DESC);
CREATE INDEX ix_drill_runs_expiry ON drill_runs(created_at);
UPDATE sqlite_sequence SET seq = MAX(seq, COALESCE(
 (SELECT seq FROM drill_sequence_010 WHERE name='drill_runs'), 0)) WHERE name='drill_runs';
INSERT INTO sqlite_sequence(name,seq)
 SELECT name,seq FROM drill_sequence_010 WHERE name='drill_runs'
 AND NOT EXISTS(SELECT 1 FROM sqlite_sequence WHERE name='drill_runs');

DROP TABLE drill_sequence_010;

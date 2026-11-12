-- Original Arcade ladders also field fourteen opponents. Preserve row/receipt
-- identities and AUTOINCREMENT high-water marks; the runner is transactional.
CREATE TEMP TABLE arcade_sequence_015 AS
 SELECT name,seq FROM sqlite_sequence WHERE name IN ('arcade_bests','arcade_runs');

CREATE TABLE arcade_bests_015 (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 player_id TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
 client_id TEXT NOT NULL,
 receipt_id INTEGER NOT NULL,
 body_hash TEXT NOT NULL,
 drill_id TEXT NOT NULL CHECK(drill_id IN ('whiff-punish','hit-confirm','anti-air','crosscut','drive-impact','whiff-punish-ii','hit-confirm-ii','anti-air-ii','crosscut-ii','drive-impact-ii','parry')),
 score INTEGER NOT NULL CHECK(score BETWEEN 0 AND 1000000),
 enemies_defeated INTEGER NOT NULL CHECK(enemies_defeated BETWEEN 0 AND total_opponents),
 total_opponents INTEGER NOT NULL CHECK(((drill_id IN ('whiff-punish-ii','hit-confirm-ii','anti-air-ii','crosscut-ii','drive-impact-ii','parry') AND total_opponents BETWEEN 13 AND 14) OR (drill_id NOT IN ('whiff-punish-ii','hit-confirm-ii','anti-air-ii','crosscut-ii','drive-impact-ii','parry') AND total_opponents BETWEEN 12 AND 14))),
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
INSERT INTO arcade_bests_015 SELECT * FROM arcade_bests;
DROP TABLE arcade_bests;
ALTER TABLE arcade_bests_015 RENAME TO arcade_bests;
CREATE INDEX ix_arcade_board ON arcade_bests(drill_id,completed DESC,enemies_defeated DESC,elapsed_ms,score DESC);
UPDATE sqlite_sequence SET seq=MAX(seq,COALESCE((SELECT seq FROM arcade_sequence_015 WHERE name='arcade_bests'),0)) WHERE name='arcade_bests';
INSERT INTO sqlite_sequence(name,seq) SELECT name,seq FROM arcade_sequence_015 WHERE name='arcade_bests' AND NOT EXISTS(SELECT 1 FROM sqlite_sequence WHERE name='arcade_bests');

CREATE TABLE arcade_runs_015 (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 player_id TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
 client_id TEXT NOT NULL,
 receipt_id INTEGER NOT NULL,
 body_hash TEXT NOT NULL,
 drill_id TEXT NOT NULL CHECK(drill_id IN ('whiff-punish','hit-confirm','anti-air','crosscut','drive-impact','whiff-punish-ii','hit-confirm-ii','anti-air-ii','crosscut-ii','drive-impact-ii','parry')),
 score INTEGER NOT NULL CHECK(score BETWEEN 0 AND 1000000),
 enemies_defeated INTEGER NOT NULL CHECK(enemies_defeated BETWEEN 0 AND total_opponents),
 total_opponents INTEGER NOT NULL CHECK(((drill_id IN ('whiff-punish-ii','hit-confirm-ii','anti-air-ii','crosscut-ii','drive-impact-ii','parry') AND total_opponents BETWEEN 13 AND 14) OR (drill_id NOT IN ('whiff-punish-ii','hit-confirm-ii','anti-air-ii','crosscut-ii','drive-impact-ii','parry') AND total_opponents BETWEEN 12 AND 14))),
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
INSERT INTO arcade_runs_015 SELECT * FROM arcade_runs;
DROP TABLE arcade_runs;
ALTER TABLE arcade_runs_015 RENAME TO arcade_runs;
CREATE INDEX ix_arcade_runs_recent ON arcade_runs(drill_id,created_at DESC,id DESC);
CREATE INDEX ix_arcade_runs_player ON arcade_runs(player_id,id DESC);
CREATE INDEX ix_arcade_runs_expiry ON arcade_runs(created_at);
UPDATE sqlite_sequence SET seq=MAX(seq,COALESCE((SELECT seq FROM arcade_sequence_015 WHERE name='arcade_runs'),0)) WHERE name='arcade_runs';
INSERT INTO sqlite_sequence(name,seq) SELECT name,seq FROM arcade_sequence_015 WHERE name='arcade_runs' AND NOT EXISTS(SELECT 1 FROM sqlite_sequence WHERE name='arcade_runs');

DROP TABLE arcade_sequence_015;

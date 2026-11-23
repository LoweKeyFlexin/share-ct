-- Allow 'linux' in all five score-table platform CHECK constraints to match
-- share-ct #70 (players.ValidPlatform). Preserves all columns (including
-- submissions.reaction_crown_earned from 017), indexes, ON DELETE CASCADE, and
-- sqlite_sequence high-water marks atomically inside the migration transaction.
CREATE TEMP TABLE linux_sequence_018 AS
 SELECT name,seq FROM sqlite_sequence WHERE name IN ('drill_bests','drill_runs','arcade_bests','arcade_runs');

CREATE TABLE submissions_018 (
    id                    INTEGER PRIMARY KEY,
    player_id             TEXT    NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    client_id             TEXT    NOT NULL,
    source                TEXT    NOT NULL CHECK (source IN ('touch', 'pad', 'keyboard')),
    best_ms               REAL    NOT NULL CHECK (best_ms >= 100.0 AND best_ms <= 3333.34),
    avg_ms                REAL    NOT NULL,
    accuracy              REAL    NOT NULL CHECK (accuracy >= 0 AND accuracy <= 1),
    score                 INTEGER NOT NULL CHECK (score BETWEEN 0 AND 2500),
    attempts_ms           TEXT    NOT NULL,
    misfires              INTEGER NOT NULL CHECK (misfires >= 0),
    client_best_ms        REAL    NOT NULL,
    client_avg_ms         REAL    NOT NULL,
    client_accuracy       REAL    NOT NULL,
    client_score          INTEGER NOT NULL,
    app_build             TEXT    NOT NULL,
    platform              TEXT    NOT NULL CHECK (platform IN ('ios', 'mac', 'windows', 'android', 'linux')),
    device_model          TEXT,
    device_label          TEXT,
    trace                 TEXT,
    created_at            INTEGER NOT NULL,
    reaction_crown_earned INTEGER NOT NULL DEFAULT 0 CHECK (reaction_crown_earned IN (0, 1)),
    UNIQUE (player_id, client_id)
);
INSERT INTO submissions_018 (
    id, player_id, client_id, source, best_ms, avg_ms, accuracy, score,
    attempts_ms, misfires, client_best_ms, client_avg_ms, client_accuracy,
    client_score, app_build, platform, device_model, device_label, trace,
    created_at, reaction_crown_earned
) SELECT
    id, player_id, client_id, source, best_ms, avg_ms, accuracy, score,
    attempts_ms, misfires, client_best_ms, client_avg_ms, client_accuracy,
    client_score, app_build, platform, device_model, device_label, trace,
    created_at, reaction_crown_earned
FROM submissions;
DROP TABLE submissions;
ALTER TABLE submissions_018 RENAME TO submissions;
CREATE INDEX ix_sub_board ON submissions(source, score DESC, best_ms ASC, created_at);
CREATE INDEX ix_sub_player ON submissions(player_id, created_at DESC);
CREATE INDEX ix_sub_fastest ON submissions(source, best_ms ASC, score DESC, created_at);

CREATE TABLE drill_bests_018 (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 player_id TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
 drill_id TEXT NOT NULL CHECK(drill_id IN ('whiff-punish','hit-confirm','anti-air','crosscut','drive-impact','mental-stack','whiff-punish-ii','hit-confirm-ii','anti-air-ii','crosscut-ii','drive-impact-ii','parry')),
 client_id TEXT NOT NULL,
 score INTEGER NOT NULL CHECK(score BETWEEN 0 AND 1000000),
 source TEXT NOT NULL CHECK(source IN ('touch','pad','keyboard')),
 app_build TEXT NOT NULL,
 platform TEXT NOT NULL CHECK(platform IN ('ios','mac','windows','android','linux')),
 device_model TEXT NOT NULL,
 device_label TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 UNIQUE(player_id,drill_id)
);
INSERT INTO drill_bests_018 (id,player_id,drill_id,client_id,score,source,app_build,platform,device_model,device_label,created_at)
 SELECT id,player_id,drill_id,client_id,score,source,app_build,platform,device_model,device_label,created_at FROM drill_bests;
DROP TABLE drill_bests;
ALTER TABLE drill_bests_018 RENAME TO drill_bests;
CREATE INDEX ix_drill_board ON drill_bests(drill_id,score DESC,created_at,id);
UPDATE sqlite_sequence SET seq = MAX(seq, COALESCE(
 (SELECT seq FROM linux_sequence_018 WHERE name='drill_bests'), 0)) WHERE name='drill_bests';
INSERT INTO sqlite_sequence(name,seq)
 SELECT name,seq FROM linux_sequence_018 WHERE name='drill_bests'
 AND NOT EXISTS(SELECT 1 FROM sqlite_sequence WHERE name='drill_bests');

CREATE TABLE drill_runs_018 (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 player_id TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
 client_id TEXT NOT NULL,
 receipt_id INTEGER NOT NULL,
 body_hash TEXT NOT NULL,
 drill_id TEXT NOT NULL CHECK(drill_id IN ('whiff-punish','hit-confirm','anti-air','crosscut','drive-impact','mental-stack','whiff-punish-ii','hit-confirm-ii','anti-air-ii','crosscut-ii','drive-impact-ii','parry')),
 score INTEGER NOT NULL CHECK(score BETWEEN 0 AND 1000000),
 source TEXT NOT NULL CHECK(source IN ('touch','pad','keyboard')),
 app_build TEXT NOT NULL,
 platform TEXT NOT NULL CHECK(platform IN ('ios','mac','windows','android','linux')),
 device_model TEXT NOT NULL,
 device_label TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 UNIQUE(player_id,client_id)
);
INSERT INTO drill_runs_018 (id,player_id,client_id,receipt_id,body_hash,drill_id,score,source,app_build,platform,device_model,device_label,created_at)
 SELECT id,player_id,client_id,receipt_id,body_hash,drill_id,score,source,app_build,platform,device_model,device_label,created_at FROM drill_runs;
DROP TABLE drill_runs;
ALTER TABLE drill_runs_018 RENAME TO drill_runs;
CREATE INDEX ix_drill_runs_recent ON drill_runs(drill_id,created_at DESC,id DESC);
CREATE INDEX ix_drill_runs_player ON drill_runs(player_id,id DESC);
CREATE INDEX ix_drill_runs_expiry ON drill_runs(created_at);
UPDATE sqlite_sequence SET seq = MAX(seq, COALESCE(
 (SELECT seq FROM linux_sequence_018 WHERE name='drill_runs'), 0)) WHERE name='drill_runs';
INSERT INTO sqlite_sequence(name,seq)
 SELECT name,seq FROM linux_sequence_018 WHERE name='drill_runs'
 AND NOT EXISTS(SELECT 1 FROM sqlite_sequence WHERE name='drill_runs');

CREATE TABLE arcade_bests_018 (
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
 platform TEXT NOT NULL CHECK(platform IN ('ios','mac','windows','android','linux')),
 device_model TEXT NOT NULL,
 device_label TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 publishes_run INTEGER NOT NULL CHECK(publishes_run IN (0,1)),
 UNIQUE(player_id,drill_id)
);
INSERT INTO arcade_bests_018 SELECT * FROM arcade_bests;
DROP TABLE arcade_bests;
ALTER TABLE arcade_bests_018 RENAME TO arcade_bests;
CREATE INDEX ix_arcade_board ON arcade_bests(drill_id,completed DESC,enemies_defeated DESC,elapsed_ms,score DESC);
UPDATE sqlite_sequence SET seq=MAX(seq,COALESCE((SELECT seq FROM linux_sequence_018 WHERE name='arcade_bests'),0)) WHERE name='arcade_bests';
INSERT INTO sqlite_sequence(name,seq) SELECT name,seq FROM linux_sequence_018 WHERE name='arcade_bests' AND NOT EXISTS(SELECT 1 FROM sqlite_sequence WHERE name='arcade_bests');

CREATE TABLE arcade_runs_018 (
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
 platform TEXT NOT NULL CHECK(platform IN ('ios','mac','windows','android','linux')),
 device_model TEXT NOT NULL,
 device_label TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 publishes_run INTEGER NOT NULL CHECK(publishes_run IN (0,1)),
 UNIQUE(player_id,client_id)
);
INSERT INTO arcade_runs_018 SELECT * FROM arcade_runs;
DROP TABLE arcade_runs;
ALTER TABLE arcade_runs_018 RENAME TO arcade_runs;
CREATE INDEX ix_arcade_runs_recent ON arcade_runs(drill_id,created_at DESC,id DESC);
CREATE INDEX ix_arcade_runs_player ON arcade_runs(player_id,id DESC);
CREATE INDEX ix_arcade_runs_expiry ON arcade_runs(created_at);
UPDATE sqlite_sequence SET seq=MAX(seq,COALESCE((SELECT seq FROM linux_sequence_018 WHERE name='arcade_runs'),0)) WHERE name='arcade_runs';
INSERT INTO sqlite_sequence(name,seq) SELECT name,seq FROM linux_sequence_018 WHERE name='arcade_runs' AND NOT EXISTS(SELECT 1 FROM sqlite_sequence WHERE name='arcade_runs');

DROP TABLE linux_sequence_018;

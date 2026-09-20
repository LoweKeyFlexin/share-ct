-- The backfill is completed with crypto/rand in store.apply, in this same migration
-- transaction. SQLite's randomblob is deliberately not used for public identities.
ALTER TABLE players ADD COLUMN player_ref TEXT;
CREATE UNIQUE INDEX ux_players_player_ref ON players(player_ref);
CREATE TRIGGER players_require_ref_insert BEFORE INSERT ON players
    WHEN NEW.player_ref IS NULL OR length(NEW.player_ref) <> 32
    BEGIN SELECT RAISE(ABORT, 'player_ref required'); END;
CREATE TRIGGER players_require_ref_update BEFORE UPDATE OF player_ref ON players
    WHEN NEW.player_ref IS NULL OR length(NEW.player_ref) <> 32
    BEGIN SELECT RAISE(ABORT, 'player_ref required'); END;

CREATE TABLE reports (
    id          TEXT PRIMARY KEY,
    reporter_id TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    target_id   TEXT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    reason      TEXT NOT NULL CHECK (reason IN ('spam', 'harassment', 'inappropriate')),
    state       TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'dismissed', 'upheld')),
    created_at  INTEGER NOT NULL,
    CHECK (reporter_id <> target_id),
    UNIQUE (reporter_id, target_id, reason)
);
CREATE INDEX ix_reports_reporter_time ON reports(reporter_id, created_at);
CREATE INDEX ix_reports_target ON reports(target_id);

-- No address, report body, bearer, or copied player reference is retained here.
-- Both FKs above cascade to reports, which in turn cascades to this outbox.
CREATE TABLE report_alert_outbox (
    report_id       TEXT PRIMARY KEY REFERENCES reports(id) ON DELETE CASCADE,
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL,
    sent_at         INTEGER
);
CREATE INDEX ix_report_alert_due ON report_alert_outbox(next_attempt_at) WHERE sent_at IS NULL;

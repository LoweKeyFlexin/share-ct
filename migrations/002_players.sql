-- 002_players: the identity shared by every Share CT feature (design brief §B.2, §C).
-- A device mints an opaque id on first opt-in and receives a bearer token; only the
-- token's hash is stored. linked_account stays NULL until a later account link.
CREATE TABLE IF NOT EXISTS players (
    id             TEXT    PRIMARY KEY,
    token_hash     TEXT    NOT NULL,   -- sha256(bearer)
    display_name   TEXT    NOT NULL,
    platform       TEXT    NOT NULL,   -- ios | mac | windows | android
    created_at     INTEGER NOT NULL,   -- unix seconds
    last_seen_at   INTEGER NOT NULL,
    linked_account TEXT
);

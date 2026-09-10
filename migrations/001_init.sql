-- 001_init: the migrations ledger.
-- The runner treats a missing schema_migrations table as "nothing applied yet", so this
-- is the only migration that ever runs against an empty file.
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL,
    applied_at INTEGER NOT NULL   -- unix seconds
);

-- 003_players_token_hash: the bearer lookup. Authentication finds the player by the
-- sha256 of the presented token, so the hash is unique and indexed.
CREATE UNIQUE INDEX IF NOT EXISTS ux_players_token_hash ON players(token_hash);

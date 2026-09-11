-- 005_leaderboard_fastest_index: the fastest board order (M1 delta §2, Aaron's pick A).
-- The board ranks the fastest single attempt by default now, on the app's local board
-- and here alike, so GET /v1/board's winning-row window and its ORDER BY both lead on
-- best_ms. ix_sub_board leads on score and keeps serving sort=score; this index is the
-- same shape with the two measures swapped.
CREATE INDEX IF NOT EXISTS ix_sub_fastest ON submissions(source, best_ms ASC, score DESC, created_at);

-- 006_collapse_identical_reruns: one row per RUN, not per submission of that run.
--
-- WHY THIS IS NEEDED WHEN THE TABLE ALREADY HAS `UNIQUE (player_id, client_id)`.
-- That constraint held. It makes a RETRY idempotent — same id, one row — and Insert
-- already answers ON CONFLICT DO NOTHING. What it cannot catch is the same finished
-- trial arriving under a DIFFERENT client_id, which is what happened: on 2026-09-20 the
-- live feed carried one player's run nine times and another's twice, every copy
-- identical on all twelve public fields, each with its own id. The app is where that is
-- fixed. This repairs rows already written, and it is idempotent, so it is safe to ship
-- before or after the app fix.
--
-- THE RULE IS AARON'S, VERBATIM: "only remove duplicates if every single time is
-- identical in the run." Two different trials can share a score, or a best_ms, by
-- coincidence — three attempt times identical in the same order cannot. So a run's
-- identity is the exact `attempts_ms` JSON text, plus every ranked and claimed column.
-- Anything differing by a single field is left alone even if it looks like a duplicate.
--
-- created_at is NOT part of the identity: differing submission times are the symptom,
-- not a distinguishing feature. `source` IS — the same times on a pad and on touch are
-- two legitimate entries. misfires IS, because it is an input to the score.
--
-- THE SURVIVOR IS THE LOWEST `id`, which is the earliest submission: `id` is the
-- autoincrement rowid and `created_at` is stamped in the same INSERT, so lowest id is
-- always oldest. That is the copy taken closest to the trial and the one whose rank the
-- player already saw; keeping the newest would move ranks for no reason. MIN(id) makes
-- the choice total and reproducible with no tiebreak needed.
--
-- Deleting rather than tombstoning: the board ranks with queries over this table, and a
-- flag column would have to be honoured by every existing and future query. The deleted
-- rows carry nothing the survivor lacks.

DELETE FROM submissions
WHERE id NOT IN (
    SELECT MIN(id)
    FROM submissions
    GROUP BY player_id, source, attempts_ms, misfires,
             best_ms, avg_ms, accuracy, score,
             client_best_ms, client_avg_ms, client_accuracy, client_score
);

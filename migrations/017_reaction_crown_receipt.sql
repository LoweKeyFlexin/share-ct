-- Derived from the existing qualified rolling-30-day ranking, not client input.
-- Kept on its existing submission so an idempotent retry cannot lose a verified
-- achievement after another player takes the lead. Existing player deletion
-- cascades through submissions and erases this bit with the score.
ALTER TABLE submissions ADD COLUMN reaction_crown_earned INTEGER NOT NULL DEFAULT 0
    CHECK (reaction_crown_earned IN (0, 1));

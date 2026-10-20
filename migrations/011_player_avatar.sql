-- controller-tester-fgc#7853: one board avatar per player. A short descriptor of ids
-- (fighter, outfit, color, gear), never an image. NULL is "no avatar": the app draws its
-- default. Nullable and additive, so every existing row and every older app is unaffected.
ALTER TABLE players ADD COLUMN avatar TEXT CHECK (avatar IS NULL OR length(avatar) <= 64);

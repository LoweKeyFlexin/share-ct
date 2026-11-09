-- Keep the original 64-byte column and all existing data intact. Only complete
-- multi-item descriptors above that legacy cap use the additive bounded column.
-- Both columns belong to the player row and follow its existing erasure cascade.
ALTER TABLE players ADD COLUMN avatar_complete TEXT CHECK (avatar_complete IS NULL OR length(avatar_complete) <= 224);

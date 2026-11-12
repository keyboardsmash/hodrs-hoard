-- Covers (spec 3.5): the SHA-256 hash of the item's cover image, which is
-- stored as covers/ab/cd/<hash>.jpg in the data folder. NULL: no cover.
-- Several items may share a cover. The check keeps anything that isn't a
-- hash out of file paths.
ALTER TABLE items ADD COLUMN cover TEXT
    CHECK (cover IS NULL OR (length(cover) = 64 AND cover NOT GLOB '*[^0-9a-f]*'));

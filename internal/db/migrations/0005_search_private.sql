-- Searchable text that only logged-in users may search: notes and who an
-- item is loaned to (spec 3.2: hidden from anonymous visitors). Until now
-- notes were part of search_text, which anonymous searches use.
--
-- Filled by Go, like search_text. The server rebuilds both for existing
-- items at startup (see item.SearchVersion), since lowercasing non-ASCII
-- text can't be done in SQLite.
ALTER TABLE items ADD COLUMN search_private TEXT NOT NULL DEFAULT '';

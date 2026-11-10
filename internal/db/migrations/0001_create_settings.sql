-- Application settings set by admins at runtime (for example whether
-- anonymous visitors may browse). Values are stored as text.
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

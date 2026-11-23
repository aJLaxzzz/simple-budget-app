-- Verified imports are retained for source auditing and snapshot restoration.
CREATE TABLE calendar_imports (
 id TEXT PRIMARY KEY NOT NULL,
 year INTEGER NOT NULL REFERENCES production_calendar_years(year),
 calendar_revision INTEGER NOT NULL CHECK(calendar_revision>0),
 source TEXT NOT NULL, published_at TEXT NOT NULL,
 version TEXT NOT NULL, checksum TEXT NOT NULL,
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)),
 operation_id TEXT NOT NULL, created_at TEXT NOT NULL,
 UNIQUE(year,calendar_revision)
);
CREATE INDEX calendar_imports_year ON calendar_imports(year,calendar_revision DESC);
CREATE TRIGGER calendar_imports_immutable BEFORE UPDATE ON calendar_imports BEGIN SELECT RAISE(ABORT,'calendar imports are immutable'); END;

-- Inclusive upper boundary for automatic creation; existing instances remain independent.
ALTER TABLE series_runtime ADD COLUMN end_date TEXT CHECK(end_date IS NULL OR (length(end_date)=10 AND date(end_date,'+0 days') IS NOT NULL AND date(end_date,'+0 days')=end_date));

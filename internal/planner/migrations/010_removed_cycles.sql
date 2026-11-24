-- Reservations only, not retained objects: no title, payload or restoration data.
CREATE TABLE removed_cycles (
 series_id TEXT NOT NULL REFERENCES series(id),
 cycle_index INTEGER NOT NULL CHECK(cycle_index>=0),
 PRIMARY KEY(series_id,cycle_index)
);
CREATE TRIGGER occurrence_removed_cycle BEFORE INSERT ON occurrences
WHEN EXISTS(SELECT 1 FROM removed_cycles WHERE series_id=NEW.series_id AND cycle_index=NEW.cycle_index)
BEGIN SELECT RAISE(ABORT,'permanently removed cycle'); END;

-- Task-series creation originally keeps its idempotency key on the series row.
-- Retain only used command keys after physical deletion, never source payloads.
CREATE TABLE retired_series_requests (
 request_key TEXT PRIMARY KEY NOT NULL
);
CREATE TRIGGER series_retired_request BEFORE INSERT ON series
WHEN NEW.request_key IS NOT NULL AND EXISTS(SELECT 1 FROM retired_series_requests WHERE request_key=NEW.request_key)
BEGIN SELECT RAISE(ABORT,'retired series creation request'); END;

ALTER TABLE sessions ADD COLUMN completed_date TEXT CHECK(completed_date IS NULL OR (length(completed_date)=10 AND date(completed_date,'+0 days') IS NOT NULL AND date(completed_date,'+0 days')=completed_date));
CREATE TRIGGER sessions_interval_insert BEFORE INSERT ON sessions WHEN (NEW.start_time IS NULL)!=(NEW.end_time IS NULL) BEGIN SELECT RAISE(ABORT,'session requires both time boundaries or neither'); END;
CREATE TRIGGER sessions_interval_update BEFORE UPDATE ON sessions WHEN (NEW.start_time IS NULL)!=(NEW.end_time IS NULL) BEGIN SELECT RAISE(ABORT,'session requires both time boundaries or neither'); END;
CREATE INDEX history_session_time ON history_events(session_id,created_at);

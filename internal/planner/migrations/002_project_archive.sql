ALTER TABLE projects ADD COLUMN archived_at TEXT;
CREATE INDEX projects_status_archive ON projects(status,archived_at);
CREATE INDEX history_pagination ON history_events(created_at,id);

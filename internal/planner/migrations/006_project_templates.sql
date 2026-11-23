-- Project cycles may be compact closed records or reciprocally linked projects.
ALTER TABLE occurrences ADD COLUMN project_link TEXT REFERENCES projects(occurrence_id) DEFERRABLE INITIALLY DEFERRED CHECK(project_link IS NULL OR project_link=id);
ALTER TABLE occurrences ADD COLUMN project_state TEXT CHECK(
 (project_state IS NULL AND project_link IS NULL) OR
 (project_state IS 'compact' AND project_link IS NULL AND outcome IS NOT NULL AND outcome IN('skipped','cancelled')) OR
 (project_state IS 'materialized' AND project_link IS id));
-- Preserve any project occurrences already populated through earlier storage APIs.
UPDATE occurrences SET project_link=id,project_state='materialized' WHERE series_id IN(SELECT id FROM series WHERE source_type='project') AND EXISTS(SELECT 1 FROM projects p WHERE p.occurrence_id=occurrences.id);
UPDATE occurrences SET project_state='compact' WHERE series_id IN(SELECT id FROM series WHERE source_type='project') AND project_link IS NULL AND outcome IN('skipped','cancelled');
CREATE TEMP TABLE project_migration_guard(n INTEGER CHECK(n=0));
INSERT INTO project_migration_guard SELECT count(*) FROM occurrences WHERE series_id IN(SELECT id FROM series WHERE source_type='project') AND project_state IS NULL;
DROP TABLE project_migration_guard;
CREATE TRIGGER occurrence_project_kind_insert BEFORE INSERT ON occurrences BEGIN
 SELECT CASE WHEN (SELECT source_type FROM series WHERE id=NEW.series_id)='project' AND (NEW.project_state IS NULL OR NEW.task_link IS NOT NULL) THEN RAISE(ABORT,'project occurrence requires project state') END;
 SELECT CASE WHEN (SELECT source_type FROM series WHERE id=NEW.series_id)='task' AND NEW.project_state IS NOT NULL THEN RAISE(ABORT,'task occurrence cannot link a project') END;
END;
CREATE TRIGGER occurrence_project_link_update BEFORE UPDATE OF project_link,project_state ON occurrences BEGIN
 SELECT CASE WHEN (SELECT source_type FROM series WHERE id=NEW.series_id)='project' AND NEW.project_state IS NULL THEN RAISE(ABORT,'project occurrence state is required') END;
 SELECT CASE WHEN OLD.project_link IS NOT NULL AND NEW.project_link IS NOT OLD.project_link THEN RAISE(ABORT,'project occurrence link is immutable') END;
 SELECT CASE WHEN (SELECT source_type FROM series WHERE id=NEW.series_id)!='project' AND (NEW.project_link IS NOT NULL OR NEW.project_state IS NOT NULL) THEN RAISE(ABORT,'not a project cycle') END;
END;
CREATE TRIGGER project_occurrence_link_insert BEFORE INSERT ON projects WHEN NEW.occurrence_id IS NOT NULL BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM occurrences o JOIN series s ON s.id=o.series_id WHERE o.id=NEW.occurrence_id AND s.source_type='project' AND o.project_link=o.id AND o.project_state='materialized') THEN RAISE(ABORT,'project requires reciprocal project cycle') END;
END;
CREATE TRIGGER project_occurrence_link_update BEFORE UPDATE OF occurrence_id ON projects WHEN NEW.occurrence_id IS NOT OLD.occurrence_id BEGIN SELECT RAISE(ABORT,'project occurrence link is immutable'); END;
CREATE TABLE project_instances (
 project_id TEXT PRIMARY KEY REFERENCES projects(id),
 baseline_json TEXT NOT NULL CHECK(json_valid(baseline_json)),
 task_keys_json TEXT NOT NULL CHECK(json_valid(task_keys_json))
);
CREATE TABLE project_closed_tasks (
 occurrence_id TEXT NOT NULL REFERENCES occurrences(id), task_id TEXT NOT NULL REFERENCES tasks(id),
 closed_revision INTEGER NOT NULL CHECK(closed_revision>0), previous_status TEXT NOT NULL CHECK(previous_status IN('planned','in_progress')),
 restored_at TEXT, PRIMARY KEY(occurrence_id,task_id)
);
-- Rebuild only history to admit a template FK in the existing one-object check.
CREATE TABLE history_events_next (
 id TEXT PRIMARY KEY NOT NULL, task_id TEXT REFERENCES tasks(id), project_id TEXT REFERENCES projects(id), session_id TEXT REFERENCES sessions(id), series_id TEXT REFERENCES series(id), occurrence_id TEXT REFERENCES occurrences(id), template_id TEXT REFERENCES project_templates(id),
 actor TEXT NOT NULL, action TEXT NOT NULL, payload_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(payload_json)), operation_id TEXT,
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 CHECK(task_id IS NOT NULL OR project_id IS NOT NULL OR session_id IS NOT NULL OR series_id IS NOT NULL OR occurrence_id IS NOT NULL OR template_id IS NOT NULL)
);
INSERT INTO history_events_next(id,task_id,project_id,session_id,series_id,occurrence_id,actor,action,payload_json,operation_id,created_at) SELECT id,task_id,project_id,session_id,series_id,occurrence_id,actor,action,payload_json,operation_id,created_at FROM history_events;
DROP TABLE history_events;
ALTER TABLE history_events_next RENAME TO history_events;
CREATE TRIGGER history_immutable BEFORE UPDATE ON history_events BEGIN SELECT RAISE(ABORT,'history events are immutable'); END;
CREATE INDEX history_task_time ON history_events(task_id,created_at);
CREATE INDEX history_project_time ON history_events(project_id,created_at);
CREATE INDEX history_series_time ON history_events(series_id,created_at);
CREATE INDEX history_session_time ON history_events(session_id,created_at);
CREATE INDEX history_occurrence_time ON history_events(occurrence_id,created_at);
CREATE INDEX history_template_time ON history_events(template_id,created_at);
CREATE TRIGGER project_schedule_protected_insert BEFORE INSERT ON occurrence_schedule BEGIN
 SELECT CASE WHEN EXISTS(SELECT 1 FROM projects p JOIN occurrences o ON o.id=p.occurrence_id WHERE o.id=NEW.occurrence_id AND (p.status IN('completed','in_progress') OR o.manual_override=1 OR EXISTS(SELECT 1 FROM tasks t WHERE t.project_id=p.id AND t.status IN('completed','in_progress')))) THEN RAISE(ABORT,'protected project schedule') END;
END;
CREATE TRIGGER project_schedule_protected_update BEFORE UPDATE ON occurrence_schedule BEGIN
 SELECT CASE WHEN EXISTS(SELECT 1 FROM projects p JOIN occurrences o ON o.id=p.occurrence_id WHERE o.id=NEW.occurrence_id AND (p.status IN('completed','in_progress') OR o.manual_override=1 OR EXISTS(SELECT 1 FROM tasks t WHERE t.project_id=p.id AND t.status IN('completed','in_progress')))) THEN RAISE(ABORT,'protected project schedule') END;
END;

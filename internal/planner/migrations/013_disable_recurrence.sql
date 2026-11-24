-- Stopping a property is distinct from archiving the technical source.
ALTER TABLE series ADD COLUMN disabled_at TEXT;
-- Immutable audit snapshot: detached entity IDs deliberately have no FK, so the
-- ordinary entity may later be deleted without erasing recurrence provenance.
CREATE TABLE detached_occurrences (
 occurrence_id TEXT PRIMARY KEY REFERENCES occurrences(id),
 entity_id TEXT NOT NULL,
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)),
 detached_at TEXT NOT NULL
);
CREATE TRIGGER detached_immutable BEFORE UPDATE ON detached_occurrences BEGIN SELECT RAISE(ABORT,'detachment history is immutable'); END;
DROP TRIGGER task_occurrence_link_immutable;
CREATE TRIGGER task_occurrence_link_immutable BEFORE UPDATE OF occurrence_id ON tasks
 WHEN OLD.occurrence_id IS NOT NULL AND NEW.occurrence_id IS NOT OLD.occurrence_id
 AND NOT (NEW.occurrence_id IS NULL AND EXISTS(SELECT 1 FROM detached_occurrences d JOIN occurrences o ON o.id=d.occurrence_id JOIN series s ON s.id=o.series_id WHERE d.occurrence_id=OLD.occurrence_id AND d.entity_id=OLD.id AND s.disabled_at IS NOT NULL))
 BEGIN SELECT RAISE(ABORT,'task occurrence link is immutable'); END;
DROP TRIGGER occurrence_identity_immutable;
CREATE TRIGGER occurrence_identity_immutable BEFORE UPDATE OF id,series_id,rule_version_id,cycle_index,base_date,adjusted_date,calculation_json ON occurrences BEGIN SELECT RAISE(ABORT,'occurrence provenance is immutable'); END;
CREATE TRIGGER occurrence_task_link_immutable BEFORE UPDATE OF task_link ON occurrences
 WHEN NEW.task_link IS NOT OLD.task_link AND NOT (NEW.task_link IS NULL AND EXISTS(SELECT 1 FROM detached_occurrences WHERE occurrence_id=OLD.id))
 BEGIN SELECT RAISE(ABORT,'occurrence task link is immutable'); END;
DROP TRIGGER project_occurrence_link_update;
CREATE TRIGGER project_occurrence_link_update BEFORE UPDATE OF occurrence_id ON projects WHEN NEW.occurrence_id IS NOT OLD.occurrence_id
 AND NOT (NEW.occurrence_id IS NULL AND EXISTS(SELECT 1 FROM detached_occurrences d JOIN occurrences o ON o.id=d.occurrence_id JOIN series s ON s.id=o.series_id WHERE d.occurrence_id=OLD.occurrence_id AND d.entity_id=OLD.id AND s.disabled_at IS NOT NULL))
 BEGIN SELECT RAISE(ABORT,'project occurrence link is immutable'); END;
DROP TRIGGER occurrence_project_link_update;
CREATE TRIGGER occurrence_project_link_update BEFORE UPDATE OF project_link,project_state ON occurrences
 WHEN NOT EXISTS(SELECT 1 FROM detached_occurrences WHERE occurrence_id=OLD.id)
 BEGIN
 SELECT CASE WHEN (SELECT source_type FROM series WHERE id=NEW.series_id)='project' AND NEW.project_state IS NULL THEN RAISE(ABORT,'project occurrence state is required') END;
 SELECT CASE WHEN OLD.project_link IS NOT NULL AND NEW.project_link IS NOT OLD.project_link THEN RAISE(ABORT,'project occurrence link is immutable') END;
 SELECT CASE WHEN (SELECT source_type FROM series WHERE id=NEW.series_id)!='project' AND (NEW.project_link IS NOT NULL OR NEW.project_state IS NOT NULL) THEN RAISE(ABORT,'not a project cycle') END;
 END;

ALTER TABLE series ADD COLUMN calculation_error TEXT;
-- Restore imports audit snapshots before occurrences under deferred FK checking.
-- Only a disabled source plus a detachment receipt may omit reciprocal links.
DROP TRIGGER occurrence_task_link_insert;
CREATE TRIGGER occurrence_task_link_insert BEFORE INSERT ON occurrences BEGIN
 SELECT CASE WHEN (SELECT source_type FROM series WHERE id=NEW.series_id)='task' AND (NEW.task_link IS NULL OR NEW.task_link!=NEW.id)
 AND NOT EXISTS(SELECT 1 FROM detached_occurrences d JOIN series s ON s.id=NEW.series_id WHERE d.occurrence_id=NEW.id AND s.disabled_at IS NOT NULL)
 THEN RAISE(ABORT,'task occurrence requires a reciprocal task link') END;
 SELECT CASE WHEN NEW.pending_completion=1 AND NOT EXISTS(SELECT 1 FROM series WHERE id=NEW.series_id AND source_type='task' AND anchor_mode='completion') THEN RAISE(ABORT,'pending completion requires completion series') END;
END;
DROP TRIGGER occurrence_project_kind_insert;
CREATE TRIGGER occurrence_project_kind_insert BEFORE INSERT ON occurrences BEGIN
 SELECT CASE WHEN (SELECT source_type FROM series WHERE id=NEW.series_id)='project' AND (NEW.project_state IS NULL OR NEW.task_link IS NOT NULL)
 AND NOT EXISTS(SELECT 1 FROM detached_occurrences d JOIN series s ON s.id=NEW.series_id WHERE d.occurrence_id=NEW.id AND s.disabled_at IS NOT NULL)
 THEN RAISE(ABORT,'project occurrence requires project state') END;
 SELECT CASE WHEN (SELECT source_type FROM series WHERE id=NEW.series_id)='task' AND NEW.project_state IS NOT NULL THEN RAISE(ABORT,'task occurrence cannot link a project') END;
END;

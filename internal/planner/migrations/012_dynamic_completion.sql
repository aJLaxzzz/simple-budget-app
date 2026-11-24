-- Keep one CURRENT completion cycle; dependent future rows are real tasks.
ALTER TABLE occurrences ADD COLUMN dynamic INTEGER NOT NULL DEFAULT 0 CHECK(dynamic IN(0,1));
CREATE INDEX occurrences_dynamic_chain ON occurrences(series_id,dynamic,cycle_index);
DROP TRIGGER task_occurrence_closed;
DROP TRIGGER task_occurrence_pending_insert;
DROP TRIGGER task_occurrence_pending_link;
CREATE TRIGGER task_occurrence_closed AFTER UPDATE OF status ON tasks WHEN NEW.occurrence_id IS NOT NULL AND NEW.status IS NOT OLD.status BEGIN
 UPDATE occurrences SET revision=revision+1,updated_at=NEW.updated_at,pending_completion=CASE WHEN dynamic=0 AND NEW.status IN('planned','in_progress') AND series_id IN(SELECT id FROM series WHERE anchor_mode='completion') THEN 1 ELSE 0 END WHERE id=NEW.occurrence_id;
END;
CREATE TRIGGER task_occurrence_pending_insert AFTER INSERT ON tasks WHEN NEW.occurrence_id IS NOT NULL BEGIN
 UPDATE occurrences SET pending_completion=CASE WHEN dynamic=0 AND NEW.status IN('planned','in_progress') AND series_id IN(SELECT id FROM series WHERE anchor_mode='completion') THEN 1 ELSE 0 END WHERE id=NEW.occurrence_id;
END;
CREATE TRIGGER task_occurrence_pending_link AFTER UPDATE OF occurrence_id ON tasks WHEN NEW.occurrence_id IS NOT NULL BEGIN
 UPDATE occurrences SET pending_completion=CASE WHEN dynamic=0 AND NEW.status IN('planned','in_progress') AND series_id IN(SELECT id FROM series WHERE anchor_mode='completion') THEN 1 ELSE 0 END WHERE id=NEW.occurrence_id;
END;
CREATE TRIGGER dynamic_current_exclusive BEFORE UPDATE OF dynamic,pending_completion ON occurrences WHEN NEW.dynamic=1 AND NEW.pending_completion=1 BEGIN
 SELECT RAISE(ABORT,'dependent occurrence cannot be current');
END;
CREATE TABLE series_assignments (
 id TEXT PRIMARY KEY NOT NULL,
 series_id TEXT NOT NULL REFERENCES series(id),
 version INTEGER NOT NULL CHECK(version>0),
 project_id TEXT REFERENCES projects(id),
 created_at TEXT NOT NULL,
 UNIQUE(series_id,version)
);
CREATE TRIGGER assignment_immutable BEFORE UPDATE ON series_assignments BEGIN SELECT RAISE(ABORT,'assignment version is immutable'); END;

CREATE TRIGGER dynamic_completion_insert BEFORE INSERT ON occurrences WHEN NEW.dynamic=1 BEGIN
 SELECT CASE WHEN NEW.pending_completion=1 OR NOT EXISTS(SELECT 1 FROM series WHERE id=NEW.series_id AND source_type='task' AND anchor_mode='completion') THEN RAISE(ABORT,'invalid dependent completion occurrence') END;
END;
CREATE TRIGGER dynamic_completion_update BEFORE UPDATE OF dynamic ON occurrences WHEN NEW.dynamic=1 BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM series WHERE id=NEW.series_id AND source_type='task' AND anchor_mode='completion') THEN RAISE(ABORT,'invalid dependent completion occurrence') END;
END;

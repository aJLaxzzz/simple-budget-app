-- A cycle belongs to the series, not to a particular revision of its rule.
-- Existing conflicting rows make migration fail transactionally; never deduplicate history.
CREATE UNIQUE INDEX occurrences_series_cycle ON occurrences(series_id,cycle_index);
ALTER TABLE occurrences ADD COLUMN task_link TEXT REFERENCES tasks(occurrence_id) DEFERRABLE INITIALLY DEFERRED CHECK(task_link IS NULL OR task_link=id);
ALTER TABLE occurrences ADD COLUMN calculation_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(calculation_json));
ALTER TABLE occurrences ADD COLUMN pending_completion INTEGER NOT NULL DEFAULT 0 CHECK(pending_completion IN(0,1));
UPDATE occurrences SET task_link=id WHERE series_id IN(SELECT id FROM series WHERE source_type='task');
CREATE UNIQUE INDEX occurrences_one_pending_completion ON occurrences(series_id) WHERE pending_completion=1;
CREATE TRIGGER occurrence_task_link_insert BEFORE INSERT ON occurrences BEGIN
 SELECT CASE WHEN (SELECT source_type FROM series WHERE id=NEW.series_id)='task' AND (NEW.task_link IS NULL OR NEW.task_link!=NEW.id) THEN RAISE(ABORT,'task occurrence requires a reciprocal task link') END;
 SELECT CASE WHEN NEW.pending_completion=1 AND NOT EXISTS(SELECT 1 FROM series WHERE id=NEW.series_id AND source_type='task' AND anchor_mode='completion') THEN RAISE(ABORT,'pending completion requires completion series') END;
END;
CREATE TRIGGER occurrence_identity_immutable BEFORE UPDATE OF id,series_id,rule_version_id,cycle_index,base_date,adjusted_date,task_link,calculation_json ON occurrences BEGIN
 SELECT RAISE(ABORT,'occurrence provenance is immutable');
END;
CREATE TRIGGER task_occurrence_link_immutable BEFORE UPDATE OF occurrence_id ON tasks WHEN OLD.occurrence_id IS NOT NULL AND NEW.occurrence_id IS NOT OLD.occurrence_id BEGIN
 SELECT RAISE(ABORT,'task occurrence link is immutable');
END;
CREATE TRIGGER task_occurrence_closed AFTER UPDATE OF status ON tasks WHEN NEW.occurrence_id IS NOT NULL AND NEW.status IS NOT OLD.status BEGIN
 UPDATE occurrences SET revision=revision+1,updated_at=NEW.updated_at,pending_completion=CASE WHEN NEW.status IN('planned','in_progress') AND series_id IN(SELECT id FROM series WHERE anchor_mode='completion') THEN 1 ELSE 0 END WHERE id=NEW.occurrence_id AND pending_completion != CASE WHEN NEW.status IN('planned','in_progress') AND series_id IN(SELECT id FROM series WHERE anchor_mode='completion') THEN 1 ELSE 0 END;
END;
-- Keep the partial unique key correct for every way a task can be linked.
CREATE TRIGGER task_occurrence_pending_insert AFTER INSERT ON tasks WHEN NEW.occurrence_id IS NOT NULL BEGIN
 UPDATE occurrences SET pending_completion=CASE WHEN NEW.status IN('planned','in_progress') AND series_id IN(SELECT id FROM series WHERE anchor_mode='completion') THEN 1 ELSE 0 END WHERE id=NEW.occurrence_id;
END;
CREATE TRIGGER task_occurrence_pending_link AFTER UPDATE OF occurrence_id ON tasks WHEN NEW.occurrence_id IS NOT NULL BEGIN
 UPDATE occurrences SET pending_completion=CASE WHEN NEW.status IN('planned','in_progress') AND series_id IN(SELECT id FROM series WHERE anchor_mode='completion') THEN 1 ELSE 0 END WHERE id=NEW.occurrence_id;
END;
UPDATE occurrences SET pending_completion=1 WHERE series_id IN(SELECT id FROM series WHERE anchor_mode='completion') AND id IN(SELECT occurrence_id FROM tasks WHERE status IN('planned','in_progress'));
ALTER TABLE series ADD COLUMN request_key TEXT;
ALTER TABLE series ADD COLUMN request_hash TEXT;
CREATE UNIQUE INDEX series_request_key ON series(request_key) WHERE request_key IS NOT NULL;
CREATE TABLE series_runtime (
 series_id TEXT PRIMARY KEY NOT NULL REFERENCES series(id),
 next_cycle INTEGER NOT NULL CHECK(next_cycle>=1),
 anchor_date TEXT CHECK(anchor_date IS NULL OR (date(anchor_date,'+0 days') IS NOT NULL AND date(anchor_date,'+0 days')=anchor_date AND length(anchor_date)=10)),
 anchor_cycle INTEGER NOT NULL DEFAULT 0 CHECK(anchor_cycle>=0),
 waiting_occurrence_id TEXT UNIQUE REFERENCES occurrences(id),
 horizon_date TEXT NOT NULL CHECK(date(horizon_date,'+0 days') IS NOT NULL AND date(horizon_date,'+0 days')=horizon_date AND length(horizon_date)=10)
);
CREATE TRIGGER series_runtime_waiting_insert BEFORE INSERT ON series_runtime WHEN NEW.waiting_occurrence_id IS NOT NULL BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM occurrences WHERE id=NEW.waiting_occurrence_id AND series_id=NEW.series_id AND pending_completion=1) THEN RAISE(ABORT,'invalid waiting occurrence') END;
END;
CREATE TRIGGER series_runtime_waiting_update BEFORE UPDATE OF waiting_occurrence_id ON series_runtime WHEN NEW.waiting_occurrence_id IS NOT NULL BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM occurrences WHERE id=NEW.waiting_occurrence_id AND series_id=NEW.series_id AND pending_completion=1) THEN RAISE(ABORT,'invalid waiting occurrence') END;
END;

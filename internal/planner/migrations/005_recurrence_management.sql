-- Immutable rule payloads and original occurrence provenance remain intact.
CREATE TABLE series_version_scopes (
 version_id TEXT PRIMARY KEY NOT NULL,
 series_id TEXT NOT NULL REFERENCES series(id),
 from_cycle INTEGER NOT NULL CHECK(from_cycle>=0),
 until_cycle INTEGER CHECK(until_cycle IS NULL OR until_cycle>=from_cycle),
 FOREIGN KEY(version_id,series_id) REFERENCES series_versions(id,series_id)
);
INSERT INTO series_version_scopes(version_id,series_id,from_cycle,until_cycle)
 SELECT id,series_id,COALESCE(json_extract(rule_json,'$.first_cycle'),version-1),
 lead(COALESCE(json_extract(rule_json,'$.first_cycle'),version-1)) OVER(PARTITION BY series_id ORDER BY version) FROM series_versions;
CREATE TRIGGER scopes_no_overlap_insert BEFORE INSERT ON series_version_scopes WHEN NEW.until_cycle IS NULL OR NEW.until_cycle>NEW.from_cycle BEGIN
 SELECT CASE WHEN EXISTS(SELECT 1 FROM series_version_scopes s WHERE s.series_id=NEW.series_id AND (s.until_cycle IS NULL OR s.until_cycle>s.from_cycle) AND (s.until_cycle IS NULL OR NEW.from_cycle<s.until_cycle) AND (NEW.until_cycle IS NULL OR s.from_cycle<NEW.until_cycle)) THEN RAISE(ABORT,'rule cycle scopes overlap') END;
END;
CREATE TRIGGER scopes_no_overlap_update BEFORE UPDATE ON series_version_scopes WHEN NEW.until_cycle IS NULL OR NEW.until_cycle>NEW.from_cycle BEGIN
 SELECT CASE WHEN EXISTS(SELECT 1 FROM series_version_scopes s WHERE s.version_id!=OLD.version_id AND s.series_id=NEW.series_id AND (s.until_cycle IS NULL OR s.until_cycle>s.from_cycle) AND (s.until_cycle IS NULL OR NEW.from_cycle<s.until_cycle) AND (NEW.until_cycle IS NULL OR s.from_cycle<NEW.until_cycle)) THEN RAISE(ABORT,'rule cycle scopes overlap') END;
END;
ALTER TABLE occurrences ADD COLUMN manual_override INTEGER NOT NULL DEFAULT 0 CHECK(manual_override IN(0,1));
UPDATE occurrences SET manual_override=1 WHERE manual_date IS NOT NULL;
CREATE UNIQUE INDEX occurrences_id_series ON occurrences(id,series_id);
CREATE TABLE occurrence_schedule (
 occurrence_id TEXT PRIMARY KEY NOT NULL,
 series_id TEXT NOT NULL,
 rule_version_id TEXT NOT NULL,
 base_date TEXT NOT NULL CHECK(length(base_date)=10 AND date(base_date,'+0 days') IS NOT NULL AND date(base_date,'+0 days')=base_date),
 adjusted_date TEXT NOT NULL CHECK(length(adjusted_date)=10 AND date(adjusted_date,'+0 days') IS NOT NULL AND date(adjusted_date,'+0 days')=adjusted_date),
 calculation_json TEXT NOT NULL CHECK(json_valid(calculation_json)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 FOREIGN KEY(occurrence_id,series_id) REFERENCES occurrences(id,series_id),
 FOREIGN KEY(rule_version_id,series_id) REFERENCES series_versions(id,series_id)
);
CREATE VIEW occurrence_current AS SELECT o.*,COALESCE(s.rule_version_id,o.rule_version_id) current_rule_version_id,COALESCE(s.base_date,o.base_date) current_base_date,COALESCE(s.adjusted_date,o.adjusted_date) current_adjusted_date FROM occurrences o LEFT JOIN occurrence_schedule s ON s.occurrence_id=o.id;
ALTER TABLE series_pauses ADD COLUMN resumed_on TEXT CHECK(resumed_on IS NULL OR (length(resumed_on)=10 AND date(resumed_on,'+0 days') IS NOT NULL AND date(resumed_on,'+0 days')=resumed_on));
CREATE TABLE occurrence_incidents (
 id TEXT PRIMARY KEY NOT NULL,
 occurrence_id TEXT NOT NULL REFERENCES occurrences(id),
 kind TEXT NOT NULL CHECK(kind IN('skipped','cancelled')),
 source TEXT NOT NULL CHECK(source IN('pause','user')),
 pause_id TEXT REFERENCES series_pauses(id),
 planned_date TEXT NOT NULL CHECK(length(planned_date)=10 AND date(planned_date,'+0 days') IS NOT NULL AND date(planned_date,'+0 days')=planned_date),
 reason TEXT NOT NULL,
 disposition TEXT NOT NULL DEFAULT 'needs_decision' CHECK(disposition IN('needs_decision','processed')),
 resolution TEXT NOT NULL DEFAULT 'pending' CHECK(resolution IN('pending','rescheduled','restored','completed','cancelled','acknowledged')),
 reviewed_at TEXT,
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0), created_at TEXT NOT NULL,updated_at TEXT NOT NULL,
 CHECK((source='pause' AND pause_id IS NOT NULL) OR (source='user' AND pause_id IS NULL)),
 UNIQUE(occurrence_id,pause_id)
);
CREATE INDEX incidents_disposition ON occurrence_incidents(disposition,created_at,id);
CREATE INDEX incidents_occurrence ON occurrence_incidents(occurrence_id,created_at);
CREATE INDEX history_occurrence_time ON history_events(occurrence_id,created_at);
CREATE TRIGGER incident_fact_immutable BEFORE UPDATE OF id,occurrence_id,kind,source,pause_id,planned_date,reason,created_at ON occurrence_incidents BEGIN SELECT RAISE(ABORT,'incident fact is immutable'); END;
CREATE TABLE planner_commands (
 request_id TEXT PRIMARY KEY NOT NULL CHECK(length(request_id) BETWEEN 1 AND 128),
 target TEXT NOT NULL,action TEXT NOT NULL,request_hash TEXT NOT NULL,
 response_json TEXT NOT NULL CHECK(json_valid(response_json)),created_at TEXT NOT NULL
);
ALTER TABLE series_runtime ADD COLUMN anchor_kind TEXT NOT NULL DEFAULT 'completion' CHECK(anchor_kind IN('completion','reanchor'));
CREATE TRIGGER schedule_protected_insert BEFORE INSERT ON occurrence_schedule WHEN EXISTS(SELECT 1 FROM tasks WHERE occurrence_id=NEW.occurrence_id) BEGIN
 SELECT CASE WHEN EXISTS(SELECT 1 FROM tasks t JOIN occurrences o ON o.id=t.occurrence_id WHERE o.id=NEW.occurrence_id AND (t.status IN('completed','in_progress') OR o.manual_override=1 OR EXISTS(SELECT 1 FROM sessions s WHERE s.task_id=t.id AND s.status IN('completed','in_progress')))) THEN RAISE(ABORT,'protected occurrence schedule') END;
END;
CREATE TRIGGER schedule_protected_update BEFORE UPDATE ON occurrence_schedule BEGIN
 SELECT CASE WHEN EXISTS(SELECT 1 FROM tasks t JOIN occurrences o ON o.id=t.occurrence_id WHERE o.id=NEW.occurrence_id AND (t.status IN('completed','in_progress') OR o.manual_override=1 OR EXISTS(SELECT 1 FROM sessions s WHERE s.task_id=t.id AND s.status IN('completed','in_progress')))) THEN RAISE(ABORT,'protected occurrence schedule') END;
END;
CREATE TRIGGER incident_pause_series BEFORE INSERT ON occurrence_incidents WHEN NEW.pause_id IS NOT NULL BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM series_pauses p JOIN occurrences o ON o.series_id=p.series_id WHERE p.id=NEW.pause_id AND o.id=NEW.occurrence_id) THEN RAISE(ABORT,'pause and occurrence belong to different series') END;
END;
-- Import known closed states from v4. Registration is now; the original task
-- history is retained, rather than guessing the time of an earlier cancellation.
INSERT INTO occurrence_incidents(id,occurrence_id,kind,source,planned_date,reason,disposition,resolution,reviewed_at,created_at,updated_at)
 SELECT lower(hex(randomblob(16))),o.id,t.status,'user',COALESCE(t.planned_date,o.manual_date,o.adjusted_date),
 COALESCE(NULLIF(o.reason,''),'Состояние сохранено при обновлении хранилища с версии 4'),
 CASE t.status WHEN 'cancelled' THEN 'processed' ELSE 'needs_decision' END,
 CASE t.status WHEN 'cancelled' THEN 'cancelled' ELSE 'pending' END,
 CASE WHEN t.status='cancelled' THEN strftime('%Y-%m-%dT%H:%M:%fZ','now') END,
 strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now')
 FROM occurrences o JOIN tasks t ON t.occurrence_id=o.id WHERE t.status IN('skipped','cancelled');
INSERT INTO attention_state(id,occurrence_id,disposition,created_at,updated_at)
 SELECT lower(hex(randomblob(16))),occurrence_id,disposition,created_at,updated_at FROM occurrence_incidents WHERE true
 ON CONFLICT(occurrence_id) DO UPDATE SET disposition=excluded.disposition,revision=attention_state.revision+1,updated_at=excluded.updated_at;
INSERT INTO history_events(id,occurrence_id,actor,action,payload_json,operation_id,created_at)
 SELECT lower(hex(randomblob(16))),occurrence_id,'system','incident_imported',
 json_object('incident_id',json_object('before',null,'after',id),'kind',json_object('before',null,'after',kind),'source_schema',json_object('before',null,'after',4)),
 id,created_at FROM occurrence_incidents;

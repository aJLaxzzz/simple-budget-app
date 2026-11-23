CREATE TABLE planner_settings (
 key TEXT PRIMARY KEY NOT NULL, value TEXT NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
INSERT INTO planner_settings(key,value) VALUES ('timezone','Europe/Moscow'),('generation_horizon_days','60');
CREATE TABLE project_templates (
 id TEXT PRIMARY KEY NOT NULL CHECK(length(id)>0), title TEXT NOT NULL CHECK(length(trim(title))>0), archived INTEGER NOT NULL DEFAULT 0 CHECK(archived IN(0,1)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0), created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')), updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE TABLE template_versions (
 id TEXT PRIMARY KEY NOT NULL, template_id TEXT NOT NULL REFERENCES project_templates(id), version INTEGER NOT NULL CHECK(version>0),
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)), created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')), UNIQUE(template_id,version)
);
CREATE TABLE series (
 id TEXT PRIMARY KEY NOT NULL, source_type TEXT NOT NULL CHECK(source_type IN('task','project')),
 anchor_mode TEXT NOT NULL CHECK(anchor_mode IN('schedule','completion')), state TEXT NOT NULL DEFAULT 'active' CHECK(state IN('active','paused','needs_anchor','archived')) CHECK(source_type!='project' OR anchor_mode='schedule'),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0), created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')), updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE TABLE series_versions (
 id TEXT PRIMARY KEY NOT NULL, series_id TEXT NOT NULL REFERENCES series(id), version INTEGER NOT NULL CHECK(version>0),
 effective_from TEXT CHECK(effective_from IS NULL OR (length(effective_from)=10 AND date(effective_from,'+0 days') IS NOT NULL AND date(effective_from,'+0 days')=effective_from)) NOT NULL, effective_until TEXT CHECK(effective_until IS NULL OR (length(effective_until)=10 AND date(effective_until,'+0 days') IS NOT NULL AND date(effective_until,'+0 days')=effective_until)), rule_json TEXT NOT NULL CHECK(json_valid(rule_json)),
 template_version_id TEXT REFERENCES template_versions(id), source_snapshot_json TEXT NOT NULL CHECK(json_valid(source_snapshot_json)),
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')), UNIQUE(series_id,version), UNIQUE(id,series_id), CHECK(effective_until IS NULL OR effective_until>effective_from)
);
CREATE TABLE occurrences (
 id TEXT PRIMARY KEY NOT NULL, series_id TEXT NOT NULL REFERENCES series(id), rule_version_id TEXT NOT NULL REFERENCES series_versions(id),
 cycle_index INTEGER NOT NULL CHECK(cycle_index>=0), base_date TEXT CHECK(base_date IS NULL OR (length(base_date)=10 AND date(base_date,'+0 days') IS NOT NULL AND date(base_date,'+0 days')=base_date)) NOT NULL, adjusted_date TEXT CHECK(adjusted_date IS NULL OR (length(adjusted_date)=10 AND date(adjusted_date,'+0 days') IS NOT NULL AND date(adjusted_date,'+0 days')=adjusted_date)) NOT NULL, manual_date TEXT CHECK(manual_date IS NULL OR (length(manual_date)=10 AND date(manual_date,'+0 days') IS NOT NULL AND date(manual_date,'+0 days')=manual_date)),
 outcome TEXT CHECK(outcome IN('skipped','cancelled')), reason TEXT,
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0), created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')), updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 FOREIGN KEY(rule_version_id,series_id) REFERENCES series_versions(id,series_id), UNIQUE(series_id,rule_version_id,cycle_index)
);
CREATE TABLE projects (
 id TEXT PRIMARY KEY NOT NULL CHECK(length(id)>0), title TEXT NOT NULL CHECK(length(trim(title))>0), description TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT 'planned' CHECK(status IN('planned','in_progress','completed','skipped','cancelled')),
 start_date TEXT CHECK(start_date IS NULL OR (length(start_date)=10 AND date(start_date,'+0 days') IS NOT NULL AND date(start_date,'+0 days')=start_date)), due_date TEXT CHECK(due_date IS NULL OR (length(due_date)=10 AND date(due_date,'+0 days') IS NOT NULL AND date(due_date,'+0 days')=due_date)), closed_at TEXT,
 occurrence_id TEXT UNIQUE REFERENCES occurrences(id), template_version_id TEXT REFERENCES template_versions(id),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0), created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')), updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE TABLE tasks (
 id TEXT PRIMARY KEY NOT NULL CHECK(length(id)>0), project_id TEXT REFERENCES projects(id), parent_task_id TEXT REFERENCES tasks(id) DEFERRABLE INITIALLY DEFERRED,
 title TEXT NOT NULL CHECK(length(trim(title))>0), description TEXT NOT NULL DEFAULT '', priority TEXT NOT NULL DEFAULT 'normal' CHECK(priority IN('low','normal','high')),
 status TEXT NOT NULL DEFAULT 'planned' CHECK(status IN('planned','in_progress','completed','skipped','cancelled')),
 planned_date TEXT CHECK(planned_date IS NULL OR (length(planned_date)=10 AND date(planned_date,'+0 days') IS NOT NULL AND date(planned_date,'+0 days')=planned_date)), due_date TEXT CHECK(due_date IS NULL OR (length(due_date)=10 AND date(due_date,'+0 days') IS NOT NULL AND date(due_date,'+0 days')=due_date)), scheduled_time TEXT CHECK(scheduled_time IS NULL OR (scheduled_time GLOB '[0-2][0-9]:[0-5][0-9]' AND substr(scheduled_time,1,2)<='23')), completed_at TEXT, completed_date TEXT CHECK(completed_date IS NULL OR (length(completed_date)=10 AND date(completed_date,'+0 days') IS NOT NULL AND date(completed_date,'+0 days')=completed_date)),
 occurrence_id TEXT UNIQUE REFERENCES occurrences(id),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0), created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')), updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 CHECK(parent_task_id IS NULL OR (parent_task_id!=id AND project_id IS NOT NULL)), CHECK(scheduled_time IS NULL OR planned_date IS NOT NULL)
);
CREATE TRIGGER tasks_parent_insert BEFORE INSERT ON tasks WHEN NEW.parent_task_id IS NOT NULL BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM tasks p WHERE p.id=NEW.parent_task_id AND p.parent_task_id IS NULL AND p.project_id=NEW.project_id) THEN RAISE(ABORT,'parent must be a root task in the same project') END;
END;
CREATE TRIGGER tasks_parent_update BEFORE UPDATE OF parent_task_id,project_id ON tasks BEGIN
 SELECT CASE WHEN NEW.parent_task_id IS NOT NULL AND (EXISTS(SELECT 1 FROM tasks WHERE parent_task_id=NEW.id) OR NOT EXISTS(SELECT 1 FROM tasks p WHERE p.id=NEW.parent_task_id AND p.parent_task_id IS NULL AND p.project_id=NEW.project_id)) THEN RAISE(ABORT,'invalid task nesting') END;
END;
CREATE TRIGGER tasks_project_update AFTER UPDATE OF project_id ON tasks BEGIN
 -- Moving a root preserves its children and their project relationship atomically.
 UPDATE tasks SET project_id=NEW.project_id, revision=revision+1,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE parent_task_id=OLD.id AND project_id IS NOT NEW.project_id;
END;
CREATE TABLE sessions (
 id TEXT PRIMARY KEY NOT NULL, task_id TEXT NOT NULL REFERENCES tasks(id), date TEXT CHECK(date IS NULL OR (length(date)=10 AND date(date,'+0 days') IS NOT NULL AND date(date,'+0 days')=date)) NOT NULL,
 start_time TEXT CHECK(start_time IS NULL OR (start_time GLOB '[0-2][0-9]:[0-5][0-9]' AND substr(start_time,1,2)<='23')), end_time TEXT CHECK(end_time IS NULL OR (end_time GLOB '[0-2][0-9]:[0-5][0-9]' AND substr(end_time,1,2)<='23')), timezone TEXT NOT NULL DEFAULT 'Europe/Moscow' CHECK(timezone='Europe/Moscow'),
 status TEXT NOT NULL DEFAULT 'planned' CHECK(status IN('planned','in_progress','completed','cancelled')), completed_at TEXT,
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0), created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')), updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 CHECK(end_time IS NULL OR (start_time IS NOT NULL AND end_time>start_time))
);
CREATE TABLE series_pauses (
 id TEXT PRIMARY KEY NOT NULL, series_id TEXT NOT NULL REFERENCES series(id), start_date TEXT CHECK(start_date IS NULL OR (length(start_date)=10 AND date(start_date,'+0 days') IS NOT NULL AND date(start_date,'+0 days')=start_date)) NOT NULL, end_date TEXT CHECK(end_date IS NULL OR (length(end_date)=10 AND date(end_date,'+0 days') IS NOT NULL AND date(end_date,'+0 days')=end_date)), reason TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0), created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')), updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')), CHECK(end_date IS NULL OR end_date>=start_date)
);
CREATE TABLE history_events (
 id TEXT PRIMARY KEY NOT NULL, task_id TEXT REFERENCES tasks(id), project_id TEXT REFERENCES projects(id), session_id TEXT REFERENCES sessions(id), series_id TEXT REFERENCES series(id), occurrence_id TEXT REFERENCES occurrences(id),
 actor TEXT NOT NULL, action TEXT NOT NULL, payload_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(payload_json)), operation_id TEXT,
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 CHECK(task_id IS NOT NULL OR project_id IS NOT NULL OR session_id IS NOT NULL OR series_id IS NOT NULL OR occurrence_id IS NOT NULL)
);
CREATE TABLE attention_state (
 id TEXT PRIMARY KEY NOT NULL, task_id TEXT UNIQUE REFERENCES tasks(id), project_id TEXT UNIQUE REFERENCES projects(id), occurrence_id TEXT UNIQUE REFERENCES occurrences(id), series_id TEXT UNIQUE REFERENCES series(id),
 disposition TEXT NOT NULL DEFAULT 'needs_decision' CHECK(disposition IN('needs_decision','processed')), viewed_at TEXT, snoozed_until TEXT CHECK(snoozed_until IS NULL OR (length(snoozed_until)=10 AND date(snoozed_until,'+0 days') IS NOT NULL AND date(snoozed_until,'+0 days')=snoozed_until)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0), created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')), updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 CHECK((task_id IS NOT NULL)+(project_id IS NOT NULL)+(occurrence_id IS NOT NULL)+(series_id IS NOT NULL)=1)
);
CREATE TABLE production_calendar_years (
 year INTEGER PRIMARY KEY CHECK(year BETWEEN 1900 AND 9999), source TEXT NOT NULL CHECK(length(trim(source))>0), source_checksum TEXT NOT NULL, imported_at TEXT NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0), created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')), updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE TABLE production_calendar_days (
 date TEXT CHECK(date IS NULL OR (length(date)=10 AND date(date,'+0 days') IS NOT NULL AND date(date,'+0 days')=date)) PRIMARY KEY NOT NULL, year INTEGER NOT NULL REFERENCES production_calendar_years(year),
 kind TEXT NOT NULL CHECK(kind IN('working','nonworking','shortened')), reason TEXT NOT NULL,
 CHECK(substr(date,1,4)=printf('%04d',year)), CHECK(length(date)=10)
);
CREATE INDEX tasks_project_parent ON tasks(project_id,parent_task_id);
CREATE INDEX tasks_parent ON tasks(parent_task_id);
CREATE INDEX tasks_planned ON tasks(planned_date,status);
CREATE INDEX tasks_due ON tasks(due_date,status);
CREATE INDEX sessions_task ON sessions(task_id);
CREATE INDEX sessions_date ON sessions(date,status);
CREATE INDEX projects_due ON projects(due_date,status);
CREATE INDEX occurrences_date ON occurrences(adjusted_date);
CREATE INDEX pauses_series_dates ON series_pauses(series_id,start_date,end_date);
CREATE INDEX history_task_time ON history_events(task_id,created_at);
CREATE INDEX history_project_time ON history_events(project_id,created_at);
CREATE INDEX history_series_time ON history_events(series_id,created_at);
CREATE INDEX calendar_year ON production_calendar_days(year);
CREATE TRIGGER template_versions_immutable BEFORE UPDATE ON template_versions BEGIN SELECT RAISE(ABORT,'template versions are immutable'); END;
CREATE TRIGGER series_versions_immutable BEFORE UPDATE ON series_versions BEGIN SELECT RAISE(ABORT,'rule versions are immutable'); END;
CREATE TRIGGER history_immutable BEFORE UPDATE ON history_events BEGIN SELECT RAISE(ABORT,'history events are immutable'); END;
CREATE TRIGGER tasks_occurrence_insert BEFORE INSERT ON tasks WHEN NEW.occurrence_id IS NOT NULL BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM occurrences o JOIN series s ON s.id=o.series_id WHERE o.id=NEW.occurrence_id AND s.source_type='task') THEN RAISE(ABORT,'task requires a task-series occurrence') END;
END;
CREATE TRIGGER tasks_occurrence_update BEFORE UPDATE OF occurrence_id ON tasks WHEN NEW.occurrence_id IS NOT NULL BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM occurrences o JOIN series s ON s.id=o.series_id WHERE o.id=NEW.occurrence_id AND s.source_type='task') THEN RAISE(ABORT,'task requires a task-series occurrence') END;
END;
CREATE TRIGGER projects_occurrence_insert BEFORE INSERT ON projects WHEN NEW.occurrence_id IS NOT NULL BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM occurrences o JOIN series s ON s.id=o.series_id WHERE o.id=NEW.occurrence_id AND s.source_type='project') THEN RAISE(ABORT,'project requires a project-series occurrence') END;
END;
CREATE TRIGGER projects_occurrence_update BEFORE UPDATE OF occurrence_id ON projects WHEN NEW.occurrence_id IS NOT NULL BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM occurrences o JOIN series s ON s.id=o.series_id WHERE o.id=NEW.occurrence_id AND s.source_type='project') THEN RAISE(ABORT,'project requires a project-series occurrence') END;
END;

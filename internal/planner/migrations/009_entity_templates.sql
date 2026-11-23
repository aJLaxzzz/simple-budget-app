-- Shared immutable snapshots; task templates use one root task without dates.
ALTER TABLE project_templates ADD COLUMN template_kind TEXT NOT NULL DEFAULT 'project' CHECK(template_kind IN ('project','task'));
CREATE INDEX templates_kind_archive ON project_templates(template_kind,archived,created_at,id);
CREATE TABLE entity_template_links (
 id INTEGER PRIMARY KEY,
 task_id TEXT REFERENCES tasks(id),
 project_id TEXT REFERENCES projects(id),
 template_id TEXT NOT NULL REFERENCES project_templates(id),
 version_id TEXT NOT NULL REFERENCES template_versions(id),
 role TEXT NOT NULL CHECK(role IN ('saved','source')),
 series_id TEXT REFERENCES series(id),
 created_at TEXT NOT NULL,
 CHECK((task_id IS NOT NULL) != (project_id IS NOT NULL)),
 UNIQUE(task_id,template_id,role), UNIQUE(project_id,template_id,role)
);
CREATE INDEX entity_templates_task ON entity_template_links(task_id,role);
CREATE INDEX entity_templates_project ON entity_template_links(project_id,role);
CREATE TRIGGER entity_template_version_check BEFORE INSERT ON entity_template_links BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM template_versions WHERE id=NEW.version_id AND template_id=NEW.template_id) THEN RAISE(ABORT,'template version mismatch') END;
 SELECT CASE WHEN (SELECT template_kind FROM project_templates WHERE id=NEW.template_id) != CASE WHEN NEW.task_id IS NULL THEN 'project' ELSE 'task' END THEN RAISE(ABORT,'template kind mismatch') END;
END;

-- Existing repetitions retain their independent lifecycle. New UI workflows opt in.
ALTER TABLE project_templates ADD COLUMN internal INTEGER NOT NULL DEFAULT 0 CHECK(internal IN(0,1));
ALTER TABLE tasks ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0;
ALTER TABLE series ADD COLUMN task_order INTEGER;
CREATE TABLE project_repeat_state (
 series_id TEXT PRIMARY KEY REFERENCES series(id),
 mode TEXT NOT NULL CHECK(mode IN('sequential','independent')),
 source_project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,
 current_project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,
 needs_decision INTEGER NOT NULL DEFAULT 0 CHECK(needs_decision IN(0,1))
);
CREATE VIEW project_repeat_projection AS
 SELECT p.id,COALESCE(o.series_id,l.series_id) AS series_id,COALESCE(r.mode,'independent') AS mode,
 (r.series_id IS NOT NULL) AS managed,r.current_project_id,COALESCE(r.needs_decision,0) AS needs_decision,
 CASE WHEN r.mode='sequential' AND r.current_project_id IS NOT p.id AND p.status IN('planned','in_progress') THEN 1 ELSE 0 END AS preliminary
 FROM projects p LEFT JOIN occurrences o ON o.id=p.occurrence_id
 LEFT JOIN entity_template_links l ON l.id=(SELECT max(x.id) FROM entity_template_links x JOIN series s ON s.id=x.series_id WHERE x.project_id=p.id AND s.disabled_at IS NULL)
 LEFT JOIN project_repeat_state r ON r.series_id=COALESCE(o.series_id,l.series_id);

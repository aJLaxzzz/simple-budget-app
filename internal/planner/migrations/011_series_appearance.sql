-- Expand owned colors without rewriting earlier migrations or entity IDs.
CREATE TEMP TABLE saved_tasks_colors AS SELECT id,color_id FROM tasks;
ALTER TABLE tasks DROP COLUMN color_id;
ALTER TABLE tasks ADD COLUMN color_id TEXT CHECK(color_id IS NULL OR color_id IN ('slate','blue','cyan','teal','green','lime','yellow','orange','coral','rose','purple','indigo','red','burgundy','pink','magenta','amber','emerald','mint','aqua','sky','navy','violet','brown','sand','silver') OR (length(color_id)=7 AND substr(color_id,1,1)='#' AND substr(color_id,2) NOT GLOB '*[^0-9a-fA-F]*'));
UPDATE tasks SET color_id=(SELECT color_id FROM saved_tasks_colors WHERE id=tasks.id);
DROP TABLE saved_tasks_colors;
CREATE TEMP TABLE saved_projects_colors AS SELECT id,color_id FROM projects;
ALTER TABLE projects DROP COLUMN color_id;
ALTER TABLE projects ADD COLUMN color_id TEXT CHECK(color_id IS NULL OR color_id IN ('slate','blue','cyan','teal','green','lime','yellow','orange','coral','rose','purple','indigo','red','burgundy','pink','magenta','amber','emerald','mint','aqua','sky','navy','violet','brown','sand','silver') OR (length(color_id)=7 AND substr(color_id,1,1)='#' AND substr(color_id,2) NOT GLOB '*[^0-9a-fA-F]*'));
UPDATE projects SET color_id=(SELECT color_id FROM saved_projects_colors WHERE id=projects.id);
DROP TABLE saved_projects_colors;
-- Immutable appearance revisions are independent of schedule versions.
CREATE TABLE series_appearances (
 id TEXT PRIMARY KEY NOT NULL,
 series_id TEXT NOT NULL REFERENCES series(id),
 version INTEGER NOT NULL CHECK(version>0),
 appearance_json TEXT NOT NULL CHECK(json_valid(appearance_json)),
 created_at TEXT NOT NULL,
 UNIQUE(series_id,version)
);
CREATE TRIGGER series_appearances_immutable BEFORE UPDATE ON series_appearances BEGIN SELECT RAISE(ABORT,'immutable appearance version'); END;

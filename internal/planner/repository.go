package planner

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
)

type rowScanner interface{ Scan(...any) error }

const projectColumns = "id,revision,created_at,updated_at,title,description,status,start_date,due_date,closed_at,archived_at,occurrence_id,template_version_id,color_id,icon_id,COALESCE(color_id,'slate'),COALESCE(icon_id,'lucide:folder'),(SELECT template_id FROM entity_template_links WHERE project_id=projects.id AND role='saved' ORDER BY id DESC LIMIT 1),(SELECT template_id FROM template_versions WHERE id=projects.template_version_id),COALESCE((SELECT series_id FROM occurrences WHERE id=projects.occurrence_id),(SELECT l.series_id FROM entity_template_links l JOIN series s ON s.id=l.series_id WHERE l.project_id=projects.id AND s.state!='archived' ORDER BY l.id DESC LIMIT 1))"
const taskColumns = "id,revision,created_at,updated_at,project_id,parent_task_id,title,description,priority,status,planned_date,scheduled_time,due_date,completed_date,completed_at,occurrence_id,(SELECT series_id FROM occurrences WHERE id=tasks.occurrence_id),(SELECT current_rule_version_id FROM occurrence_current WHERE id=tasks.occurrence_id),(SELECT cycle_index FROM occurrences WHERE id=tasks.occurrence_id),(SELECT parent.title FROM tasks parent WHERE parent.id=tasks.parent_task_id),color_id,icon_id,COALESCE(tasks.color_id,(SELECT color_id FROM tasks parent WHERE parent.id=tasks.parent_task_id),(SELECT color_id FROM projects WHERE id=tasks.project_id),'slate'),COALESCE(tasks.icon_id,(SELECT icon_id FROM tasks parent WHERE parent.id=tasks.parent_task_id),(SELECT icon_id FROM projects WHERE id=tasks.project_id),'lucide:circle-check'),(SELECT template_id FROM entity_template_links WHERE task_id=tasks.id AND role='saved' ORDER BY id DESC LIMIT 1),(SELECT template_id FROM entity_template_links WHERE task_id=tasks.id AND role='source' ORDER BY id DESC LIMIT 1)"

func scanProject(row rowScanner) (ProjectDTO, error) {
	var p ProjectDTO
	e := row.Scan(&p.ID, &p.Revision, &p.CreatedAt, &p.UpdatedAt, &p.Title, &p.Description, &p.Status, &p.StartDate, &p.DueDate, &p.ClosedAt, &p.ArchivedAt, &p.OccurrenceID, &p.TemplateVersionID, &p.ColorID, &p.IconID, &p.EffectiveColorID, &p.EffectiveIconID, &p.SavedTemplateID, &p.SourceTemplateID, &p.RepeatSeriesID)
	return p, e
}
func scanTask(row rowScanner) (TaskDTO, error) {
	var t TaskDTO
	e := row.Scan(&t.ID, &t.Revision, &t.CreatedAt, &t.UpdatedAt, &t.ProjectID, &t.ParentTaskID, &t.Title, &t.Description, &t.Priority, &t.Status, &t.PlannedDate, &t.ScheduledTime, &t.DueDate, &t.CompletedDate, &t.CompletedAt, &t.OccurrenceID, &t.SeriesID, &t.RuleVersionID, &t.CycleIndex, &t.ParentTitle, &t.ColorID, &t.IconID, &t.EffectiveColorID, &t.EffectiveIconID, &t.SavedTemplateID, &t.SourceTemplateID)
	return t, e
}
func getProject(tx *sql.Tx, id string) (ProjectDTO, error) {
	p, e := scanProject(tx.QueryRow("SELECT "+projectColumns+" FROM projects WHERE id=?", id))
	if errors.Is(e, sql.ErrNoRows) {
		e = notFound("Проект", id)
	}
	return p, e
}
func getTask(tx *sql.Tx, id string) (TaskDTO, error) {
	t, e := scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM tasks WHERE id=?", id))
	if errors.Is(e, sql.ErrNoRows) {
		e = notFound("Задача", id)
	}
	return t, e
}
func listTasks(tx *sql.Tx, where string, args ...any) ([]TaskDTO, error) {
	rows, e := tx.Query("SELECT "+taskColumns+" FROM tasks "+where+" ORDER BY created_at,id", args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []TaskDTO{}
	for rows.Next() {
		t, e := scanTask(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, t)
	}
	return items, rows.Err()
}
func decorateTask(tx *sql.Tx, t *TaskDTO) error {
	if e := tx.QueryRow("SELECT count(*) FROM tasks WHERE parent_task_id=? AND status IN ('planned','in_progress')", t.ID).Scan(&t.OpenChildren); e != nil {
		return e
	}
	t.CompletedWithOpenChildren = t.Status == "completed" && t.OpenChildren > 0
	return nil
}
func decorateProject(tx *sql.Tx, p *ProjectDTO, includeTasks bool) error {
	p.EffectiveColorID = "slate"
	p.EffectiveIconID = "lucide:folder"
	if p.ColorID != nil {
		p.EffectiveColorID = *p.ColorID
	}
	if p.IconID != nil {
		p.EffectiveIconID = *p.IconID
	}
	p.Progress = Progress{}
	tasks, e := listTasks(tx, "WHERE project_id=?", p.ID)
	if e != nil {
		return e
	}
	p.TaskCount = len(tasks)
	parents := map[string]bool{}
	for _, t := range tasks {
		if t.ParentTaskID != nil {
			parents[*t.ParentTaskID] = true
		}
		if active(t.Status) {
			p.Progress.OpenTasks++
		}
	}
	for i := range tasks {
		t := &tasks[i]
		if e = decorateTask(tx, t); e != nil {
			return e
		}
		if parents[t.ID] {
			continue
		}
		switch t.Status {
		case "cancelled":
			p.Progress.Cancelled++
		case "skipped":
			p.Progress.Skipped++
		default:
			p.Progress.Total++
			if t.Status == "completed" {
				p.Progress.Completed++
			}
		}
	}
	if p.Progress.Total > 0 {
		p.Progress.Percent = 100 * float64(p.Progress.Completed) / float64(p.Progress.Total)
	}
	if includeTasks {
		p.Tasks = tasks
	}
	return nil
}
func insertProject(tx *sql.Tx, p ProjectDTO) error {
	if e := validateAppearance(tx, p.Appearance); e != nil {
		return e
	}
	_, e := tx.Exec("INSERT INTO projects(id,title,description,status,start_date,due_date,created_at,updated_at,occurrence_id,template_version_id,color_id,icon_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", p.ID, p.Title, p.Description, p.Status, p.StartDate, p.DueDate, p.CreatedAt, p.UpdatedAt, p.OccurrenceID, p.TemplateVersionID, p.ColorID, p.IconID)
	return e
}
func saveProject(tx *sql.Tx, p ProjectDTO) error {
	if e := validateAppearance(tx, p.Appearance); e != nil {
		return e
	}
	_, e := tx.Exec("UPDATE projects SET title=?,description=?,status=?,start_date=?,due_date=?,closed_at=?,archived_at=?,revision=revision+1,updated_at=?,color_id=?,icon_id=? WHERE id=?", p.Title, p.Description, p.Status, p.StartDate, p.DueDate, p.ClosedAt, p.ArchivedAt, p.UpdatedAt, p.ColorID, p.IconID, p.ID)
	return e
}
func insertTask(tx *sql.Tx, t TaskDTO) error {
	if e := validateAppearance(tx, t.Appearance); e != nil {
		return e
	}
	_, e := tx.Exec("INSERT INTO tasks(id,project_id,parent_task_id,title,description,priority,status,planned_date,scheduled_time,due_date,created_at,updated_at,color_id,icon_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)", t.ID, t.ProjectID, t.ParentTaskID, t.Title, t.Description, t.Priority, t.Status, t.PlannedDate, t.ScheduledTime, t.DueDate, t.CreatedAt, t.UpdatedAt, t.ColorID, t.IconID)
	return e
}
func saveTask(tx *sql.Tx, t TaskDTO) error {
	if e := validateAppearance(tx, t.Appearance); e != nil {
		return e
	}
	_, e := tx.Exec("UPDATE tasks SET project_id=?,parent_task_id=?,title=?,description=?,priority=?,status=?,planned_date=?,scheduled_time=?,due_date=?,completed_date=?,completed_at=?,revision=revision+1,updated_at=?,color_id=?,icon_id=? WHERE id=?", t.ProjectID, t.ParentTaskID, t.Title, t.Description, t.Priority, t.Status, t.PlannedDate, t.ScheduledTime, t.DueDate, t.CompletedDate, t.CompletedAt, t.UpdatedAt, t.ColorID, t.IconID, t.ID)
	return e
}
func event(tx *sql.Tx, kind, id, action, op, now string, before, after any) error {
	return eventAs(tx, kind, id, action, "owner", op, now, before, after)
}
func eventAs(tx *sql.Tx, kind, id, action, actor, op, now string, before, after any) error {
	changes := map[string]any{}
	a, b := map[string]any{}, map[string]any{}
	raw, e := json.Marshal(before)
	if e != nil {
		return e
	}
	if before != nil {
		if e = json.Unmarshal(raw, &a); e != nil {
			return e
		}
	}
	raw, e = json.Marshal(after)
	if e != nil {
		return e
	}
	if e = json.Unmarshal(raw, &b); e != nil {
		return e
	}
	// Own overrides use omitempty in immutable snapshots. Record an explicit
	// reset only for complete task/project values, not for partial event payloads.
	switch after.(type) {
	case TaskDTO, ProjectDTO, *TaskDTO, *ProjectDTO:
		for _, key := range []string{"color_id", "icon_id"} {
			if _, ok := b[key]; !ok {
				b[key] = nil
			}
		}
	}
	for key, value := range b {
		if key == "effective_color_id" || key == "effective_icon_id" {
			continue
		}
		if key == "progress" || key == "tasks" || key == "open_children" || key == "completed_with_open_children" {
			continue
		}
		if !reflect.DeepEqual(a[key], value) {
			changes[key] = map[string]any{"before": a[key], "after": value}
		}
	}
	payload, e := json.Marshal(changes)
	if e != nil {
		return e
	}
	eid, e := NewID()
	if e != nil {
		return e
	}
	column := "task_id"
	if kind == "template" {
		column = "template_id"
	}
	if kind == "occurrence" {
		column = "occurrence_id"
	}
	if kind == "series" {
		column = "series_id"
	}
	if kind == "session" {
		column = "session_id"
	}
	if kind == "project" {
		column = "project_id"
	}
	_, e = tx.Exec("INSERT INTO history_events(id,"+column+",actor,action,payload_json,operation_id,created_at) VALUES(?,?,?,?,?,?,?)", eid, id, actor, action, string(payload), op, now)
	return e
}

func listProjects(tx *sql.Tx, status string, archived bool) ([]ProjectDTO, error) {
	rows, e := tx.Query("SELECT "+projectColumns+" FROM projects WHERE (?='' OR status=?) AND (archived_at IS NOT NULL)=? ORDER BY created_at,id", status, status, archived)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []ProjectDTO{}
	for rows.Next() {
		p, e := scanProject(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, p)
	}
	return items, rows.Err()
}
func taskChildCount(tx *sql.Tx, id string, count *int) error {
	return tx.QueryRow("SELECT count(*) FROM tasks WHERE parent_task_id=?", id).Scan(count)
}
func stampTask(tx *sql.Tx, id, now string) (sql.Result, error) {
	return tx.Exec("UPDATE tasks SET updated_at=? WHERE id=?", now, id)
}
func listHistory(tx *sql.Tx, kind, id string, limit, offset int) ([]HistoryDTO, error) {
	rows, e := tx.Query("SELECT id,action,actor,created_at,operation_id,payload_json FROM history_events WHERE "+kind+"_id=? ORDER BY created_at DESC,rowid DESC LIMIT ? OFFSET ?", id, limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []HistoryDTO{}
	for rows.Next() {
		h := HistoryDTO{ObjectType: kind, ObjectID: id}
		var payload string
		var operation sql.NullString
		if e = rows.Scan(&h.ID, &h.Action, &h.Actor, &h.CreatedAt, &operation, &payload); e != nil {
			return nil, e
		}
		h.OperationID = operation.String
		h.Changes = json.RawMessage(payload)
		items = append(items, h)
	}
	return items, rows.Err()
}

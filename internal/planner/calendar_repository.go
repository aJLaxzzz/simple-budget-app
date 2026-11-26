package planner

import (
	"context"
	"database/sql"
)

// Each UNION arm selects by an indexed date. Session existence is checked across
// all dates so the meaning of a planning marker never depends on viewport size.
// Cancelled sessions remain visible history but do not replace a planned task.
const calendarSQL = `
SELECT t.id, CASE WHEN EXISTS(SELECT 1 FROM sessions x WHERE x.task_id=t.id AND x.status!='cancelled') THEN 'planning' ELSE 'task' END,
 t.revision,t.id,t.revision,t.parent_task_id,t.title,t.planned_date,t.scheduled_time,NULL,t.status,t.status,t.priority,t.project_id,p.title,p.archived_at IS NOT NULL,t.occurrence_id,o.series_id,o.current_rule_version_id,o.cycle_index,COALESCE(t.color_id,(SELECT color_id FROM tasks parent WHERE parent.id=t.parent_task_id),p.color_id,'slate'),COALESCE(t.icon_id,(SELECT icon_id FROM tasks parent WHERE parent.id=t.parent_task_id),p.icon_id,'lucide:circle-check'),COALESCE(o.dynamic,0),COALESCE((SELECT preliminary FROM project_repeat_projection WHERE id=p.id),0)
 FROM tasks t LEFT JOIN projects p ON p.id=t.project_id LEFT JOIN occurrence_current o ON o.id=t.occurrence_id
 WHERE t.status!='skipped' AND t.planned_date BETWEEN ?1 AND ?2
 AND NOT EXISTS(SELECT 1 FROM sessions x WHERE x.task_id=t.id AND x.date=t.planned_date AND x.status!='cancelled')
UNION ALL
SELECT t.id,'deadline',t.revision,t.id,t.revision,t.parent_task_id,t.title,t.due_date,NULL,NULL,t.status,t.status,t.priority,t.project_id,p.title,p.archived_at IS NOT NULL,t.occurrence_id,o.series_id,o.current_rule_version_id,o.cycle_index,COALESCE(t.color_id,(SELECT color_id FROM tasks parent WHERE parent.id=t.parent_task_id),p.color_id,'slate'),COALESCE(t.icon_id,(SELECT icon_id FROM tasks parent WHERE parent.id=t.parent_task_id),p.icon_id,'lucide:circle-check'),COALESCE(o.dynamic,0),COALESCE((SELECT preliminary FROM project_repeat_projection WHERE id=p.id),0)
 FROM tasks t LEFT JOIN projects p ON p.id=t.project_id LEFT JOIN occurrence_current o ON o.id=t.occurrence_id
 WHERE t.status!='skipped' AND t.due_date BETWEEN ?1 AND ?2 AND (t.planned_date IS NULL OR t.due_date!=t.planned_date)
UNION ALL
SELECT s.id,'session',s.revision,t.id,t.revision,t.parent_task_id,t.title,s.date,s.start_time,s.end_time,s.status,t.status,t.priority,t.project_id,p.title,p.archived_at IS NOT NULL,t.occurrence_id,o.series_id,o.current_rule_version_id,o.cycle_index,COALESCE(t.color_id,(SELECT color_id FROM tasks parent WHERE parent.id=t.parent_task_id),p.color_id,'slate'),COALESCE(t.icon_id,(SELECT icon_id FROM tasks parent WHERE parent.id=t.parent_task_id),p.icon_id,'lucide:circle-check'),COALESCE(o.dynamic,0),COALESCE((SELECT preliminary FROM project_repeat_projection WHERE id=p.id),0)
 FROM sessions s JOIN tasks t ON t.id=s.task_id LEFT JOIN projects p ON p.id=t.project_id LEFT JOIN occurrence_current o ON o.id=t.occurrence_id
 WHERE t.status!='skipped' AND s.date BETWEEN ?1 AND ?2
ORDER BY 8,9,7,2,1 LIMIT 5001`

func calendarItems(ctx context.Context, tx *sql.Tx, from, to string) ([]CalendarItem, error) {
	rows, e := tx.QueryContext(ctx, calendarSQL, from, to)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []CalendarItem{}
	for rows.Next() {
		var v CalendarItem
		if e = rows.Scan(&v.ID, &v.Type, &v.Revision, &v.TaskID, &v.TaskRevision, &v.ParentTaskID, &v.Title, &v.Date, &v.StartTime, &v.EndTime, &v.Status, &v.TaskStatus, &v.Priority, &v.ProjectID, &v.ProjectTitle, &v.ProjectArchived, &v.OccurrenceID, &v.SeriesID, &v.RuleVersionID, &v.CycleIndex, &v.EffectiveColorID, &v.EffectiveIconID, &v.Dynamic, &v.ProjectPending); e != nil {
			return nil, e
		}
		items = append(items, v)
		if len(items) > 5000 {
			return nil, invalid("В диапазоне более 5000 событий; выберите меньший период")
		}
	}
	return items, rows.Err()
}
func calendarProjects(ctx context.Context, tx *sql.Tx) ([]CalendarProject, error) {
	// Only names for the filter; do not compute project progress or read task history.
	rows, e := tx.QueryContext(ctx, "SELECT p.id,p.title,p.archived_at,r.series_id FROM projects p LEFT JOIN project_repeat_projection r ON r.id=p.id ORDER BY p.title,p.id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []CalendarProject{}
	for rows.Next() {
		var p CalendarProject
		if e = rows.Scan(&p.ID, &p.Title, &p.ArchivedAt, &p.RepeatSeriesID); e != nil {
			return nil, e
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

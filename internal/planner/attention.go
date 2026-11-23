package planner

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Overdue is a projection, never a destructive transition at midnight. A dated
// open task is late when either its deadline or its planned day has passed.
const attentionCTE = `WITH problems AS (
 SELECT 'task' AS kind,t.id AS id,t.title,t.status,CASE WHEN t.due_date<?1 THEN t.due_date ELSE t.planned_date END AS planned_date,
 t.id AS task_id,t.project_id,t.occurrence_id,o.series_id,'needs_decision' AS disposition,'overdue' AS source,
 CASE WHEN t.due_date<?1 THEN 'Просрочен крайний срок' ELSE 'Не выполнено по плану' END AS reason,t.updated_at AS occurred_at,CASE WHEN COALESCE(a.snoozed_until,'')>?1 THEN 0 ELSE 1 END AS actionable,a.snoozed_until
 FROM tasks t LEFT JOIN occurrences o ON o.id=t.occurrence_id LEFT JOIN attention_state a ON a.task_id=t.id
 WHERE t.status IN('planned','in_progress') AND ((t.due_date IS NOT NULL AND t.due_date<?1) OR (t.planned_date IS NOT NULL AND t.planned_date<?1))
 AND NOT EXISTS(SELECT 1 FROM occurrence_incidents i WHERE i.occurrence_id=t.occurrence_id AND i.disposition='needs_decision')
 UNION ALL
 SELECT 'project',p.id,p.title,p.status,p.due_date,NULL,p.id,p.occurrence_id,o.series_id,'needs_decision','overdue','Просрочен крайний срок',p.updated_at,
 CASE WHEN COALESCE(a.snoozed_until,'')>?1 THEN 0 ELSE 1 END,a.snoozed_until
 FROM projects p LEFT JOIN occurrences o ON o.id=p.occurrence_id LEFT JOIN attention_state a ON a.project_id=p.id
 WHERE p.status IN('planned','in_progress') AND p.archived_at IS NULL AND p.due_date<?1
 AND NOT EXISTS(SELECT 1 FROM occurrence_incidents i WHERE i.occurrence_id=p.occurrence_id AND i.disposition='needs_decision')
 UNION ALL
 SELECT i.kind,i.id,COALESCE(t.title,p.title,json_extract(v.source_snapshot_json,'$.title'),'Регулярное дело'),COALESCE(t.status,p.status,o.outcome),
 i.planned_date,t.id,p.id,o.id,o.series_id,i.disposition,i.source,i.reason,i.created_at,
 CASE WHEN i.disposition='needs_decision' AND COALESCE(a.snoozed_until,'')<=?1 THEN 1 ELSE 0 END,a.snoozed_until
 FROM occurrence_incidents i JOIN occurrences o ON o.id=i.occurrence_id JOIN series_versions v ON v.id=o.rule_version_id
 LEFT JOIN tasks t ON t.occurrence_id=o.id LEFT JOIN projects p ON p.occurrence_id=o.id LEFT JOIN attention_state a ON a.occurrence_id=o.id
 WHERE NOT EXISTS(SELECT 1 FROM occurrence_incidents newer WHERE newer.occurrence_id=i.occurrence_id AND (newer.created_at>i.created_at OR (newer.created_at=i.created_at AND newer.rowid>i.rowid)))
 ) `

func attentionRoute(s *Service, w http.ResponseWriter, r *http.Request) (int, any, error) {
	if r.Method != "GET" {
		return 0, nil, methodError(w, "GET")
	}
	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "needs_decision"
	}
	where := map[string]string{"needs_decision": "actionable=1", "overdue": "kind IN('task','project')", "skipped": "kind='skipped'", "processed": "disposition='processed'"}[tab]
	if where == "" {
		return 0, nil, invalid("Неизвестный фильтр внимания")
	}
	limit, offset := 50, 0
	for key, dst := range map[string]*int{"limit": &limit, "offset": &offset} {
		if raw := r.URL.Query().Get(key); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil {
				return 0, nil, invalid("Некорректная пагинация")
			}
			*dst = n
		}
	}
	if limit < 1 || limit > 100 || offset < 0 {
		return 0, nil, invalid("limit: 1..100, offset >=0")
	}
	loc, _ := time.LoadLocation("Europe/Moscow")
	today := s.now().In(loc).Format("2006-01-02")
	items := []map[string]any{}
	count := 0
	e := s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
		args := []any{today}
		if e := tx.QueryRow(attentionCTE+`SELECT count(*) FROM problems WHERE actionable=1`, args...).Scan(&count); e != nil {
			return e
		}
		if series := r.URL.Query().Get("series_id"); series != "" {
			where += " AND series_id=?"
			args = append(args, series)
		}
		args = append(args, limit, offset)
		query := attentionCTE
		if tab == "skipped" {
			query = strings.Replace(query, "newer.occurrence_id=i.occurrence_id AND", "newer.occurrence_id=i.occurrence_id AND newer.kind='skipped' AND", 1)
		}
		rows, e := tx.Query(query+`SELECT kind,id,title,status,planned_date,task_id,project_id,occurrence_id,series_id,disposition,source,reason,occurred_at,snoozed_until FROM problems WHERE `+where+` ORDER BY planned_date,id LIMIT ? OFFSET ?`, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var kind, id, title, status, date, disposition, source, reason, stamp string
			var task, project, occurrence, series, snoozedUntil *string
			if e = rows.Scan(&kind, &id, &title, &status, &date, &task, &project, &occurrence, &series, &disposition, &source, &reason, &stamp, &snoozedUntil); e != nil {
				return e
			}
			items = append(items, map[string]any{"kind": kind, "id": id, "title": title, "status": status, "planned_date": date, "task_id": task, "project_id": project, "occurrence_id": occurrence, "series_id": series, "disposition": disposition, "source": source, "reason": reason, "created_at": stamp, "snoozed_until": snoozedUntil})
		}
		return rows.Err()
	})
	return 200, map[string]any{"items": items, "count": count, "limit": limit, "offset": offset}, e
}

package planner

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type DeletionInput struct {
	ExpectedRevision int64  `json:"expected_revision"`
	RequestID        string `json:"request_id"`
	PreviewToken     string `json:"preview_token,omitempty"`
	Confirm          bool   `json:"confirm,omitempty"`
}
type DeletionDependency struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Title string `json:"title"`
}
type DeletionPreview struct {
	Dependencies []DeletionDependency `json:"dependencies"`
	Kind         string               `json:"kind"`
	ID           string               `json:"id"`
	Title        string               `json:"title"`
	Revision     int64                `json:"revision"`
	Counts       map[string]int       `json:"counts"`
	Blockers     []string             `json:"blockers"`
	Consequences []string             `json:"consequences"`
	PreviewToken string               `json:"preview_token"`
}
type deletionPlan struct {
	DeletionPreview
	rows       map[string][]int64
	snapshot   map[string][][]any
	occurrence *TaskOccurrenceDTO
}

// Table names and predicates are internal constants; object identifiers are bound.
func (p *deletionPlan) collect(tx *sql.Tx, table, where string, args ...any) error {
	rows, e := tx.Query("SELECT rowid,* FROM "+table+" WHERE "+where+" ORDER BY rowid", args...)
	if e != nil {
		return e
	}
	defer rows.Close()
	cols, e := rows.Columns()
	if e != nil {
		return e
	}
	p.rows[table] = []int64{}
	p.snapshot[table] = [][]any{}
	for rows.Next() {
		values := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range values {
			ptr[i] = &values[i]
		}
		if e = rows.Scan(ptr...); e != nil {
			return e
		}
		p.rows[table] = append(p.rows[table], values[0].(int64))
		p.snapshot[table] = append(p.snapshot[table], values)
	}
	return rows.Err()
}
func (p *deletionPlan) selected(table string) string {
	ids := []string{}
	for _, id := range p.rows[table] {
		ids = append(ids, strconv.FormatInt(id, 10))
	}
	if len(ids) == 0 {
		return "NULL"
	}
	return strings.Join(ids, ",")
}
func (p *deletionPlan) ids(table string) string {
	return "(SELECT id FROM " + table + " WHERE rowid IN (" + p.selected(table) + "))"
}
func (p *deletionPlan) blockIf(tx *sql.Tx, query, message string, args ...any) error {
	var n int
	if e := tx.QueryRow(query, args...).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		p.Blockers = append(p.Blockers, fmt.Sprintf("%s (%d)", message, n))
	}
	return nil
}
func deletionTarget(tx *sql.Tx, kind, id string) (RecordMeta, string, error) {
	switch kind {
	case "tasks":
		v, e := getTask(tx, id)
		return v.RecordMeta, v.Title, e
	case "projects":
		v, e := getProject(tx, id)
		return v.RecordMeta, v.Title, e
	case "templates", "task-templates":
		k := "project"
		if kind == "task-templates" {
			k = "task"
		}
		v, e := getTemplateKind(tx, id, "", k)
		return v.RecordMeta, v.Snapshot.Title, e
	case "series":
		v, e := getSeries(tx, id)
		title := v.Template.Title
		if v.ProjectTemplate != nil {
			title = v.ProjectTemplate.Title
		}
		return v.RecordMeta, title, e
	case "occurrences":
		v, e := getOccurrence(tx, id)
		title := v.Task.Title
		if v.Project != nil {
			title = v.Project.Title
		}
		if title == "" {
			_, title, e = deletionTarget(tx, "series", v.SeriesID)
		}
		return v.RecordMeta, title, e
	}
	return RecordMeta{}, "", invalid("Этот тип объекта не поддерживает удаление")
}
func deletionPlanTx(tx *sql.Tx, kind, id string) (deletionPlan, error) {
	p := deletionPlan{DeletionPreview: DeletionPreview{Kind: kind, ID: id, Counts: map[string]int{}, Blockers: []string{}, Consequences: []string{"Объект, его периоды работы, инциденты и собственная история будут физически удалены. Восстановление через интерфейс невозможно."}}, rows: map[string][]int64{}, snapshot: map[string][][]any{}}
	m, title, e := deletionTarget(tx, kind, id)
	if e != nil {
		return p, e
	}
	p.Title = title
	p.Revision = m.Revision
	primary := kind
	if kind == "templates" || kind == "task-templates" {
		primary = "project_templates"
	}
	if e = p.collect(tx, primary, "id=?", id); e != nil {
		return p, e
	}
	if kind == "series" {
		if e = p.collect(tx, "occurrences", "series_id IN "+p.ids("series")); e != nil {
			return p, e
		}
	}
	if kind == "occurrences" {
		o, e := getOccurrence(tx, id)
		if e != nil {
			return p, e
		}
		p.occurrence = &o
		// A series revision/anchor change invalidates the confirmation as well.
		if e = p.collect(tx, "series", "id=?", o.SeriesID); e != nil {
			return p, e
		}
		p.Consequences = append(p.Consequences, "Удаляется только выбранное выполнение. Номер выполнения резервируется навсегда; остальные выполнения сохраняются. При удалении текущего ожидания отсчёт от выполнения потребует новой даты продолжения.")
	}
	if kind == "series" || kind == "occurrences" {
		where := "occurrence_id IN " + p.ids("occurrences")
		if kind == "series" {
			where += " OR id IN (SELECT source_project_id FROM project_repeat_state WHERE series_id IN " + p.ids("series") + ")"
		}
		if e = p.collect(tx, "projects", where); e != nil {
			return p, e
		}
	}
	if kind == "projects" || kind == "series" || kind == "occurrences" {
		if e = p.collect(tx, "tasks", "project_id IN "+p.ids("projects")+" OR occurrence_id IN "+p.ids("occurrences")); e != nil {
			return p, e
		}
	}
	if kind == "tasks" || kind == "series" || kind == "occurrences" {
		base := p.ids("tasks")
		if e = p.collect(tx, "tasks", "id IN "+base+" OR parent_task_id IN "+base); e != nil {
			return p, e
		}
	}
	tasks, projects, occ := p.ids("tasks"), p.ids("projects"), p.ids("occurrences")
	// Never silently remove a separate repeating task, nor leave its source dangling.
	if e = p.blockIf(tx, "SELECT count(*) FROM tasks WHERE id IN "+tasks+" AND occurrence_id IS NOT NULL AND occurrence_id NOT IN "+occ, "Есть независимые регулярные задачи: сначала удалите их повторение либо выберите удаление конкретного выполнения"); e != nil {
		return p, e
	}
	if e = p.blockIf(tx, "SELECT count(*) FROM projects WHERE id IN "+projects+" AND occurrence_id IS NOT NULL AND occurrence_id NOT IN "+occ, "Это регулярный проект: выберите удаление выполнения или всего повторения"); e != nil {
		return p, e
	}
	excluded := "(SELECT id FROM series WHERE 0)"
	if kind == "series" {
		excluded = p.ids("series")
	}
	if e = p.blockIf(tx, "SELECT count(DISTINCT series_id) FROM series_versions WHERE series_id NOT IN "+excluded+" AND (json_extract(source_snapshot_json,'$.project_id') IN "+projects+" OR json_extract(source_snapshot_json,'$.parent_task_id') IN "+tasks+")", "Сохранённые правила повторений ссылаются на проект или родителя. Сначала удалите соответствующие повторения"); e != nil {
		return p, e
	}
	if e = p.blockIf(tx, "SELECT count(*) FROM series_assignments WHERE series_id NOT IN "+excluded+" AND project_id IN "+projects, "История назначения повторений ссылается на проект. Сначала удалите связанные повторения"); e != nil {
		return p, e
	}
	// Name the independent repetitions so the owner can resolve a blocker.
	linked, e := tx.Query("SELECT DISTINCT s.id FROM series s JOIN series_versions v ON v.series_id=s.id WHERE s.id NOT IN " + excluded + " AND (json_extract(v.source_snapshot_json,'$.project_id') IN " + projects + " OR json_extract(v.source_snapshot_json,'$.parent_task_id') IN " + tasks + " OR EXISTS(SELECT 1 FROM occurrences o JOIN tasks t ON t.occurrence_id=o.id WHERE o.series_id=s.id AND t.id IN " + tasks + " AND o.id NOT IN " + occ + ") OR EXISTS(SELECT 1 FROM entity_template_links l WHERE l.series_id=s.id AND (l.task_id IN " + tasks + " OR l.project_id IN " + projects + "))) ORDER BY s.id")
	if e != nil {
		return p, e
	}
	linkedIDs := []string{}
	for linked.Next() {
		var x string
		if e = linked.Scan(&x); e != nil {
			linked.Close()
			return p, e
		}
		linkedIDs = append(linkedIDs, x)
	}
	e = linked.Err()
	linked.Close()
	if e != nil {
		return p, e
	}
	for _, x := range linkedIDs {
		_, name, e := deletionTarget(tx, "series", x)
		if e != nil {
			return p, e
		}
		p.Dependencies = append(p.Dependencies, DeletionDependency{Kind: "series", ID: x, Title: name})
	}
	if kind != "series" && len(p.Dependencies) > 0 {
		p.Consequences = append(p.Consequences, "Связанные самостоятельные повторения и их шаблоны автоматически не удаляются. Для удаления всего повторения откройте его настройки.")
	}

	if kind == "templates" || kind == "task-templates" {
		if e = p.collect(tx, "template_versions", "template_id=?", id); e != nil {
			return p, e
		}
		for _, q := range []string{"SELECT count(*) FROM series_versions WHERE template_version_id IN " + p.ids("template_versions"), "SELECT count(*) FROM projects WHERE template_version_id IN " + p.ids("template_versions"), "SELECT count(*) FROM entity_template_links WHERE template_id IN " + p.ids("project_templates")} {
			if e = p.blockIf(tx, q, "Шаблон используется объектами или историческими повторениями. Сохраните его в архиве либо сначала удалите зависимые объекты"); e != nil {
				return p, e
			}
		}
	}
	if kind == "series" || kind == "projects" || kind == "tasks" || kind == "occurrences" {
		if e = p.collect(tx, "project_templates", `internal=1 AND (id IN (SELECT template_id FROM template_versions WHERE id IN (SELECT template_version_id FROM series_versions WHERE series_id IN `+excluded+`)) OR id IN (SELECT template_id FROM template_versions WHERE id IN (SELECT template_version_id FROM projects WHERE id IN `+projects+`)) OR id IN (SELECT template_id FROM entity_template_links WHERE project_id IN `+projects+` OR task_id IN `+tasks+`)) AND NOT EXISTS(SELECT 1 FROM series_versions v JOIN template_versions tv ON tv.id=v.template_version_id WHERE tv.template_id=project_templates.id AND v.series_id NOT IN `+excluded+`) AND NOT EXISTS(SELECT 1 FROM projects p JOIN template_versions tv ON tv.id=p.template_version_id WHERE tv.template_id=project_templates.id AND p.id NOT IN `+projects+`) AND NOT EXISTS(SELECT 1 FROM entity_template_links l WHERE l.template_id=project_templates.id AND (l.project_id IS NULL OR l.project_id NOT IN `+projects+`) AND (l.task_id IS NULL OR l.task_id NOT IN `+tasks+`))`); e != nil {
			return p, e
		}
		if e = p.collect(tx, "template_versions", "template_id IN "+p.ids("project_templates")); e != nil {
			return p, e
		}
	}
	if e = p.collect(tx, "sessions", "task_id IN "+tasks); e != nil {
		return p, e
	}
	relations := "task_id IN " + tasks + " OR project_id IN " + projects + " OR occurrence_id IN " + occ
	if kind == "series" {
		relations += " OR series_id IN " + p.ids("series")
	}
	if e = p.collect(tx, "history_events", relations+" OR session_id IN "+p.ids("sessions")+" OR template_id IN "+p.ids("project_templates")); e != nil {
		return p, e
	}
	if e = p.collect(tx, "attention_state", relations); e != nil {
		return p, e
	}
	selectors := map[string]string{
		"occurrence_incidents":  "occurrence_id IN " + occ,
		"occurrence_schedule":   "occurrence_id IN " + occ,
		"project_closed_tasks":  "occurrence_id IN " + occ + " OR task_id IN " + tasks,
		"project_instances":     "project_id IN " + projects,
		"entity_template_links": "task_id IN " + tasks + " OR project_id IN " + projects,
	}
	if kind == "series" {
		for _, t := range []string{"series_runtime", "series_pauses", "series_version_scopes", "series_versions", "removed_cycles", "series_appearances", "series_assignments", "project_repeat_state"} {
			selectors[t] = "series_id IN " + p.ids("series")
		}
		// Original standalone project survives a conversion; its template link also survives.
	}
	for t, q := range selectors {
		if e = p.collect(tx, t, q); e != nil {
			return p, e
		}
	}
	if e = p.collect(tx, "detached_occurrences", "occurrence_id IN "+occ); e != nil {
		return p, e
	}
	if kind != "series" {
		if e = p.collect(tx, "project_repeat_state", "current_project_id IN "+projects+" OR source_project_id IN "+projects); e != nil {
			return p, e
		}
	}
	// Capture surviving links for preview invalidation, not for deletion.
	if kind == "series" {
		if e = p.collect(tx, "entity_template_links", "task_id IN "+tasks+" OR project_id IN "+projects+" OR series_id IN "+p.ids("series")); e != nil {
			return p, e
		}
		p.Consequences = append(p.Consequences, "Все выполнения повторения и история повторения удаляются, автоматическое создание прекращается. Самостоятельные ранее отсоединённые объекты и общие заготовки сохраняются. Неиспользуемые внутренние снимки удаляются вместе с повторением.")
	}
	for t, rows := range p.rows {
		p.Counts[t] = len(rows)
	}
	if kind == "occurrences" {
		p.Counts["series"] = 0
	}
	token, e := digest(struct {
		Kind, ID string
		Rows     map[string][][]any
		Blockers []string
	}{kind, id, p.snapshot, p.Blockers})
	p.PreviewToken = token
	return p, e
}
func (s *Service) PreviewDeletion(ctx context.Context, kind, id string) (DeletionPreview, error) {
	var p deletionPlan
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error { var e error; p, e = deletionPlanTx(tx, kind, id); return e })
	return p.DeletionPreview, e
}
func (s *Service) DeleteForever(ctx context.Context, kind, id string, in DeletionInput) (CommandResult, error) {
	var out CommandResult
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error { var e error; out, e = s.deleteForeverTx(ctx, tx, kind, id, in); return e })
	return out, e
}
func (s *Service) deleteForeverTx(ctx context.Context, tx *sql.Tx, kind, id string, in DeletionInput) (CommandResult, error) {
	var result CommandResult
	now := stamp(s.now())
	e := func() error {
		var e error
		result, e = commandOnce(tx, in.RequestID, kind+"/"+id, "delete", now, in, func() (CommandResult, error) {
			if !in.Confirm {
				return CommandResult{}, invalid("Требуется явное подтверждение окончательного удаления")
			}
			p, e := deletionPlanTx(tx, kind, id)
			if e != nil {
				return CommandResult{}, e
			}
			if e = checkRevision(in.ExpectedRevision, RecordMeta{ID: id, Revision: p.Revision}); e != nil {
				return CommandResult{}, e
			}
			if len(p.Blockers) > 0 {
				return CommandResult{}, conflict("deletion_dependencies", strings.Join(p.Blockers, ". "), p.DeletionPreview)
			}
			if in.PreviewToken == "" || in.PreviewToken != p.PreviewToken {
				return CommandResult{}, conflict("preview_changed", "Состав удаления изменился. Получите новый предпросмотр и подтвердите его.", p.DeletionPreview)
			}
			currentStarts := map[string]string{}
			if kind != "series" {
				rows, err := tx.Query(`SELECT r.series_id,coalesce(p.start_date,'') FROM project_repeat_state r JOIN projects p ON p.id=r.current_project_id WHERE r.mode='sequential' AND p.id IN ` + p.ids("projects"))
				if err != nil {
					return CommandResult{}, err
				}
				for rows.Next() {
					var sid, start string
					if err = rows.Scan(&sid, &start); err != nil {
						rows.Close()
						return CommandResult{}, err
					}
					currentStarts[sid] = start
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return CommandResult{}, err
				}
			}
			// Reciprocal occurrence/entity FKs are checked at commit, after both are gone.
			if _, e = tx.Exec("PRAGMA defer_foreign_keys=ON"); e != nil {
				return CommandResult{}, e
			}
			if o := p.occurrence; o != nil {
				if _, e = tx.Exec("INSERT INTO removed_cycles(series_id,cycle_index) VALUES(?,?)", o.SeriesID, o.CycleIndex); e != nil {
					return CommandResult{}, e
				}
				v, e := getSeries(tx, o.SeriesID)
				if e != nil {
					return CommandResult{}, e
				}
				before := v
				if v.WaitingOccurrenceID != nil && *v.WaitingOccurrenceID == id {
					v.WaitingOccurrenceID = nil
					v.AnchorDate = nil
					if v.State != "archived" {
						v.State = "needs_anchor"
					}
				}
				if e = saveSeriesRuntime(tx, v, now); e != nil {
					return CommandResult{}, e
				}
				v.Revision++
				if e = event(tx, "series", v.ID, "occurrence_deleted", in.RequestID, now, map[string]any{"state": before.State}, map[string]any{"cycle_index": o.CycleIndex, "state": v.State}); e != nil {
					return CommandResult{}, e
				}
			}
			if kind == "series" {
				if _, e = tx.Exec("INSERT INTO retired_series_requests(request_key) SELECT request_key FROM series WHERE id=? AND request_key IS NOT NULL", id); e != nil {
					return CommandResult{}, e
				}
				// Remove only the repeat association of a surviving source. Keep its template provenance.
				for _, entity := range []string{"tasks", "projects"} {
					column := "task_id"
					object := "task"
					if entity == "projects" {
						column = "project_id"
						object = "project"
					}
					rows, e := tx.Query("SELECT DISTINCT "+column+" FROM entity_template_links WHERE series_id=? AND "+column+" IS NOT NULL AND "+column+" NOT IN "+p.ids(entity), id)
					if e != nil {
						return CommandResult{}, e
					}
					ids := []string{}
					for rows.Next() {
						var x string
						if e = rows.Scan(&x); e != nil {
							rows.Close()
							return CommandResult{}, e
						}
						ids = append(ids, x)
					}
					e = rows.Err()
					rows.Close()
					if e != nil {
						return CommandResult{}, e
					}
					for _, x := range ids {
						if _, e = tx.Exec("UPDATE "+entity+" SET revision=revision+1,updated_at=? WHERE id=?", now, x); e != nil {
							return CommandResult{}, e
						}
						if e = event(tx, object, x, "repeat_deleted", in.RequestID, now, nil, map[string]any{"series_id": id}); e != nil {
							return CommandResult{}, e
						}
					}
				}
				if _, e = tx.Exec("UPDATE entity_template_links SET series_id=NULL WHERE series_id=? AND (task_id NOT IN "+p.ids("tasks")+" OR project_id NOT IN "+p.ids("projects")+")", id); e != nil {
					return CommandResult{}, e
				}
				// Re-select links to delete, preserving detached links on the standalone source.
				if e = p.collect(tx, "entity_template_links", "task_id IN "+p.ids("tasks")+" OR project_id IN "+p.ids("projects")); e != nil {
					return CommandResult{}, e
				}
			}
			if _, e = tx.Exec(`UPDATE project_repeat_state SET needs_decision=1 WHERE current_project_id IN ` + p.ids("projects")); e != nil {
				return CommandResult{}, e
			}
			if kind == "tasks" {
				rows, e := tx.Query(`SELECT DISTINCT project_id FROM tasks WHERE id IN ` + p.ids("tasks") + ` AND project_id IS NOT NULL`)
				if e != nil {
					return CommandResult{}, e
				}
				ids := []string{}
				for rows.Next() {
					var x string
					if e = rows.Scan(&x); e != nil {
						rows.Close()
						return CommandResult{}, e
					}
					ids = append(ids, x)
				}
				e = rows.Err()
				rows.Close()
				if e != nil {
					return CommandResult{}, e
				}
				for _, pid := range ids {
					keys, e := projectKeys(tx, pid)
					if e != nil && e != sql.ErrNoRows {
						return CommandResult{}, e
					}
					if e == nil {
						for key, tid := range keys {
							var deleted bool
							if e = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM tasks WHERE id=? AND id IN `+p.ids("tasks")+`)`, tid).Scan(&deleted); e != nil {
								return CommandResult{}, e
							}
							if deleted {
								delete(keys, key)
							}
						}
						raw, e := json.Marshal(keys)
						if e != nil {
							return CommandResult{}, e
						}
						if _, e = tx.Exec(`UPDATE project_instances SET task_keys_json=? WHERE project_id=?`, string(raw), pid); e != nil {
							return CommandResult{}, e
						}
					}
					if _, e = tx.Exec(`UPDATE projects SET revision=revision+1,updated_at=? WHERE id=?`, now, pid); e != nil {
						return CommandResult{}, e
					}
					if e = event(tx, "project", pid, "tasks_deleted", in.RequestID, now, nil, p.DeletionPreview); e != nil {
						return CommandResult{}, e
					}
				}
			}
			for _, t := range []string{"project_repeat_state", "detached_occurrences", "history_events", "attention_state", "entity_template_links", "project_closed_tasks", "project_instances", "sessions", "occurrence_incidents", "occurrence_schedule", "series_runtime", "tasks", "projects", "occurrences", "series_pauses", "series_version_scopes", "series_versions", "removed_cycles", "series_appearances", "series_assignments", "series", "template_versions", "project_templates"} {
				if (t == "series" || t == "project_repeat_state") && kind != "series" {
					continue
				}
				if len(p.rows[t]) == 0 {
					continue
				}
				if _, e = tx.Exec("DELETE FROM " + t + " WHERE rowid IN (" + p.selected(t) + ")"); e != nil {
					return CommandResult{}, e
				}
			}
			if p.occurrence != nil {
				v, e := getSeries(tx, p.occurrence.SeriesID)
				if e != nil {
					return CommandResult{}, e
				}
				if v.SourceType == "task" {
					if _, e = s.ensureUpcomingTx(ctx, tx, &v, in.RequestID, now); e != nil {
						return CommandResult{}, e
					}
				}
			}
			for sid, start := range currentStarts {
				if e = s.selectNextProject(tx, sid, start); e != nil {
					return CommandResult{}, e
				}
			}
			return CommandResult{Deleted: &p.DeletionPreview}, nil
		})
		return e
	}()
	return result, e
}
func (s *Service) Unarchive(ctx context.Context, kind, id string, in DeletionInput) (CommandResult, error) {
	var result CommandResult
	now := stamp(s.now())
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var e error
		result, e = commandOnce(tx, in.RequestID, kind+"/"+id, "unarchive", now, in, func() (CommandResult, error) {
			m, _, e := deletionTarget(tx, kind, id)
			if e != nil {
				return CommandResult{}, e
			}
			if e = checkRevision(in.ExpectedRevision, m); e != nil {
				return CommandResult{}, e
			}
			if kind == "series" {
				before, err := getSeries(tx, id)
				if err != nil {
					return CommandResult{}, err
				}
				if before.State != "archived" {
					return CommandResult{}, invalid("Повторение не находится в архиве либо отключено")
				}
				next := before
				next.State = "active"
				// Missing completion anchors require an explicit decision; never invent one.
				if next.AnchorMode == "completion" && next.AnchorDate == nil && next.WaitingOccurrenceID == nil {
					next.State = "needs_anchor"
				}
				if err = s.refreshPauseState(tx, &next); err != nil {
					return CommandResult{}, err
				}
				if err = saveSeriesRuntime(tx, next, now); err != nil {
					return CommandResult{}, err
				}
				next, err = getSeries(tx, id)
				if err != nil {
					return CommandResult{}, err
				}
				if err = event(tx, "series", id, "unarchived", in.RequestID, now, before, next); err != nil {
					return CommandResult{}, err
				}
				return CommandResult{Series: &next}, nil
			}

			if kind == "projects" {
				before, e := getProject(tx, id)
				if e != nil {
					return CommandResult{}, e
				}
				if before.ArchivedAt == nil {
					return CommandResult{}, invalid("Проект уже вне архива")
				}
				if _, e = tx.Exec("UPDATE projects SET archived_at=NULL,revision=revision+1,updated_at=? WHERE id=?", now, id); e != nil {
					return CommandResult{}, e
				}
				v, e := getProject(tx, id)
				if e == nil {
					e = event(tx, "project", id, "unarchived", in.RequestID, now, before, v)
				}
				return CommandResult{Project: &v}, e
			}
			if kind != "templates" && kind != "task-templates" {
				return CommandResult{}, invalid("Этот объект не поддерживает возврат из архива")
			}
			k := "project"
			if kind == "task-templates" {
				k = "task"
			}
			before, e := getTemplateKind(tx, id, "", k)
			if e != nil {
				return CommandResult{}, e
			}
			if !before.Archived {
				return CommandResult{}, invalid("Шаблон уже вне архива")
			}
			if _, e = tx.Exec("UPDATE project_templates SET archived=0,revision=revision+1,updated_at=? WHERE id=?", now, id); e != nil {
				return CommandResult{}, e
			}
			v, e := getTemplateKind(tx, id, "", k)
			if e == nil {
				e = event(tx, "template", id, "unarchived", in.RequestID, now, before, v)
			}
			return CommandResult{Template: &v}, e
		})
		return e
	})
	return result, e
}
func lifecycleRoute(s *Service, w http.ResponseWriter, r *http.Request, kind, id, action string) (int, any, error) {
	if !fields("tasks projects occurrences series templates task-templates")[kind] {
		return 0, nil, notFound("Маршрут", kind)
	}
	if action == "delete-preview" {
		if r.Method != "GET" {
			return 0, nil, methodError(w, "GET")
		}
		v, e := s.PreviewDeletion(r.Context(), kind, id)
		return 200, v, e
	}
	if r.Method != "POST" {
		return 0, nil, methodError(w, "POST")
	}
	var in DeletionInput
	allowed := "expected_revision request_id"
	if action == "delete" {
		allowed += " preview_token confirm"
	}
	if e := strictPlannerInput(w, r, &in, allowed); e != nil {
		return 0, nil, e
	}
	if action == "unarchive" {
		v, e := s.Unarchive(r.Context(), kind, id, in)
		return 200, v, e
	}
	v, e := s.DeleteForever(r.Context(), kind, id, in)
	return 200, v, e
}

// Used creation keys remain reserved even when the series row is physically gone.
func checkRetiredSeriesRequest(tx *sql.Tx, key string) error {
	var n int
	if e := tx.QueryRow("SELECT count(*) FROM retired_series_requests WHERE request_key=?", key).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return conflict("creation_target_deleted", "Повторение по этому запросу удалено навсегда. Для нового повторения начните новое создание.", nil)
	}
	return nil
}

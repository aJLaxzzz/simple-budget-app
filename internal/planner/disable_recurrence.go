package planner

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
)

type DisableInput struct {
	ExpectedRevision int64  `json:"expected_revision"`
	KeepID           string `json:"keep_id"`
	Future           string `json:"future"`
	PreviewToken     string `json:"preview_token,omitempty"`
	RequestID        string `json:"request_id,omitempty"`
	Confirm          bool   `json:"confirm"`
}
type DisableRow struct {
	ID           string  `json:"id"`
	OccurrenceID string  `json:"occurrence_id"`
	Title        string  `json:"title"`
	Date         *string `json:"date"`
	Decision     string  `json:"decision"`
	Reason       string  `json:"reason,omitempty"`
	Revision     int64   `json:"revision"`
}
type DisablePreview struct {
	Series       SeriesDTO    `json:"series"`
	Rows         []DisableRow `json:"rows"`
	PreviewToken string       `json:"preview_token"`
}

func (s *Service) disablePreviewTx(tx *sql.Tx, id string, in DisableInput) (DisablePreview, error) {
	p := DisablePreview{Rows: []DisableRow{}}
	v, e := getSeries(tx, id)
	if e != nil {
		return p, e
	}
	p.Series = v
	if e = checkRevision(in.ExpectedRevision, v.RecordMeta); e != nil {
		return p, e
	}
	if v.State == "disabled" {
		return p, invalid("Повторение уже отключено")
	}
	if in.Future != "delete" && in.Future != "keep" {
		return p, invalid("Выберите: удалить будущие или оставить самостоятельными")
	}
	var source *ProjectDTO
	if v.SourceType == "project" {
		x, err := getProject(tx, in.KeepID)
		if err != nil {
			return p, err
		}
		if x.RepeatSeriesID == nil || *x.RepeatSeriesID != id {
			return p, invalid("Сохраняемый проект должен быть исходным проектом этого повторения")
		}
		source = &x
		p.Rows = append(p.Rows, DisableRow{ID: x.ID, Title: x.Title, Date: x.StartDate, Decision: "keep_source", Revision: x.Revision})
	}
	occurrences, more, e := seriesOccurrences(tx, id, 1001, 0)
	if e != nil {
		return p, e
	}
	if more {
		return p, invalid("Слишком много выполнений для одной операции (максимум 1000). Обработайте историю отдельно")
	}
	found := source != nil
	for _, o := range occurrences {
		row := DisableRow{OccurrenceID: o.ID}
		status := ""
		protected := o.ManualOverride
		if v.SourceType == "task" {
			t := o.Task
			row.ID = t.ID
			row.Title = t.Title
			row.Date = t.PlannedDate
			row.Revision = t.Revision
			status = t.Status
			var related int
			if e = tx.QueryRow("SELECT (SELECT count(*) FROM sessions WHERE task_id=?)+(SELECT count(*) FROM tasks WHERE parent_task_id=?)", t.ID, t.ID).Scan(&related); e != nil {
				return p, e
			}
			protected = protected || related > 0 || t.Revision > 1
		} else if o.Project != nil {
			x := o.Project
			row.ID = x.ID
			row.Title = x.Title
			row.Date = x.StartDate
			row.Revision = x.Revision
			status = x.Status
			protected = protected || x.Revision > 1 || x.ArchivedAt != nil
			for _, t := range x.Tasks {
				if t.Status != "planned" || t.Revision > 1 || t.SeriesID != nil {
					protected = true
				}
			}
			var n int
			if e = tx.QueryRow("SELECT count(*) FROM sessions WHERE task_id IN(SELECT id FROM tasks WHERE project_id=?)", x.ID).Scan(&n); e != nil {
				return p, e
			}
			protected = protected || n > 0
		} else {
			continue
		}
		switch {
		case row.ID == in.KeepID:
			found = true
			row.Decision = "detach"
		case !active(status):
			row.Decision = "history"
			row.Reason = "Закрытая история сохраняется"
		case protected || status == "in_progress" || row.Date == nil || *row.Date < s.today():
			row.Decision = "detach"
			row.Reason = "Работа начата, дата прошла или есть пользовательские изменения: сохраняется самостоятельным"
		case in.Future == "keep":
			row.Decision = "detach"
		default:
			row.Decision = "delete"
		}
		if row.Decision == "delete" {
			plan, err := deletionPlanTx(tx, "occurrences", o.ID)
			if err != nil {
				return p, err
			}
			if len(plan.Blockers) > 0 {
				row.Decision = "detach"
				row.Reason = "Есть зависимости: сохраняется самостоятельным"
			}
		}
		p.Rows = append(p.Rows, row)
	}
	if !found {
		return p, invalid("Выберите выполнение, которое останется обычной задачей")
	}
	// Include complete dependent snapshots (including sessions/history) in the token.
	snapshot := deletionPlan{rows: map[string][]int64{}, snapshot: map[string][][]any{}}
	for _, table := range []string{"tasks", "projects", "sessions", "occurrences", "entity_template_links", "history_events"} { // bounded by series rows below, no unrelated user state
		where := "1=0"
		switch table {
		case "occurrences":
			where = "series_id=?"
		case "tasks":
			where = "occurrence_id IN(SELECT id FROM occurrences WHERE series_id=?) OR project_id IN(SELECT id FROM projects WHERE occurrence_id IN(SELECT id FROM occurrences WHERE series_id=?))"
		case "projects":
			where = "occurrence_id IN(SELECT id FROM occurrences WHERE series_id=?)"
		case "sessions":
			where = "task_id IN(SELECT id FROM tasks WHERE occurrence_id IN(SELECT id FROM occurrences WHERE series_id=?))"
		case "entity_template_links":
			where = "series_id=?"
		case "history_events":
			where = "series_id=?"
		}
		args := []any{id}
		if table == "tasks" {
			args = append(args, id)
		}
		if e = snapshot.collect(tx, table, where, args...); e != nil {
			return p, e
		}
	}
	p.PreviewToken, e = digest(struct {
		Rows     []DisableRow
		Series   SeriesDTO
		Future   string
		Snapshot any
	}{p.Rows, v, in.Future, snapshot.snapshot})
	return p, e
}
func detachOccurrenceTx(tx *sql.Tx, o TaskOccurrenceDTO, op, now string) error {
	var value any = o.Task
	kind, table, id := "task", "tasks", o.Task.ID
	if o.SourceType == "project" {
		value = o.Project
		kind, table, id = "project", "projects", o.Project.ID
	}
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO detached_occurrences(occurrence_id,entity_id,snapshot_json,detached_at) VALUES(?,?,?,?)", o.ID, id, string(raw), now); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE "+table+" SET occurrence_id=NULL,revision=revision+1,updated_at=? WHERE id=?", now, id); e != nil {
		return e
	}
	if o.SourceType == "task" {
		_, e = tx.Exec("UPDATE occurrences SET task_link=NULL,pending_completion=0,dynamic=0 WHERE id=?", o.ID)
	} else {
		_, e = tx.Exec("UPDATE occurrences SET project_link=NULL,project_state=NULL WHERE id=?", o.ID)
	}
	if e != nil {
		return e
	}
	if e = resolveIncidents(tx, o.ID, "acknowledged", op, now); e != nil {
		return e
	}
	return event(tx, kind, id, "repeat_disabled", op, now, nil, map[string]any{"series_id": o.SeriesID, "occurrence_id": o.ID, "preserved_id": id})
}
func (s *Service) disableTx(ctx context.Context, tx *sql.Tx, id string, in DisableInput) (CommandResult, error) {
	now := stamp(s.now())
	return commandOnce(tx, in.RequestID, "series/"+id, "disable", now, in, func() (CommandResult, error) {
		p, e := s.disablePreviewTx(tx, id, in)
		if e != nil {
			return CommandResult{}, e
		}
		if !in.Confirm || in.PreviewToken != p.PreviewToken {
			return CommandResult{}, conflict("preview_changed", "Просмотрите состав и подтвердите отключение повторения", p)
		}
		if _, e = tx.Exec("PRAGMA defer_foreign_keys=ON"); e != nil {
			return CommandResult{}, e
		}
		if _, e = tx.Exec("UPDATE series SET disabled_at=?,revision=revision+1,updated_at=? WHERE id=?", now, now, id); e != nil {
			return CommandResult{}, e
		}
		if _, e = tx.Exec("UPDATE series_runtime SET waiting_occurrence_id=NULL,anchor_date=NULL WHERE series_id=?", id); e != nil {
			return CommandResult{}, e
		}
		if _, e = tx.Exec(`DELETE FROM project_repeat_state WHERE series_id=?`, id); e != nil {
			return CommandResult{}, e
		}
		for _, row := range p.Rows {
			if row.Decision == "keep_source" {
				if _, e = tx.Exec("UPDATE entity_template_links SET series_id=NULL WHERE project_id=? AND series_id=?", row.ID, id); e != nil {
					return CommandResult{}, e
				}
				if _, e = tx.Exec("UPDATE projects SET revision=revision+1,updated_at=? WHERE id=?", now, row.ID); e != nil {
					return CommandResult{}, e
				}
				if e = event(tx, "project", row.ID, "repeat_disabled", in.RequestID, now, nil, map[string]any{"series_id": id}); e != nil {
					return CommandResult{}, e
				}
			}
			if row.Decision == "detach" {
				o, err := getOccurrence(tx, row.OccurrenceID)
				if err != nil {
					return CommandResult{}, err
				}
				if e = detachOccurrenceTx(tx, o, in.RequestID, now); e != nil {
					return CommandResult{}, e
				}
			}
			if row.Decision == "delete" {
				plan, err := deletionPlanTx(tx, "occurrences", row.OccurrenceID)
				if err != nil {
					return CommandResult{}, err
				}
				_, e = s.deleteForeverTx(ctx, tx, "occurrences", row.OccurrenceID, DeletionInput{ExpectedRevision: plan.Revision, PreviewToken: plan.PreviewToken, Confirm: true, RequestID: in.RequestID + ":" + row.OccurrenceID})
				if e != nil {
					return CommandResult{}, e
				}
			}
		}
		if e = event(tx, "series", id, "disabled", in.RequestID, now, nil, map[string]any{"rows": p.Rows}); e != nil {
			return CommandResult{}, e
		}
		v, e := getSeries(tx, id)
		return CommandResult{Series: &v}, e
	})
}
func disableRoute(s *Service, w http.ResponseWriter, r *http.Request, id, action string) (int, any, error) {
	if r.Method != "POST" {
		return 0, nil, methodError(w, "POST")
	}
	var in DisableInput
	if e := strictPlannerInput(w, r, &in, "expected_revision keep_id future preview_token request_id confirm"); e != nil {
		return 0, nil, e
	}
	var out any
	e := s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
		var e error
		if action == "disable-preview" {
			out, e = s.disablePreviewTx(tx, id, in)
		} else {
			out, e = s.disableTx(r.Context(), tx, id, in)
		}
		return e
	})
	return 200, out, e
}

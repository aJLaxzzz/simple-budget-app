package planner

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

type ProjectSourceAction struct {
	Action string `json:"action"`
	OccurrenceCommand
}

func (in ProjectSourceAction) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(in.OccurrenceCommand)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	fields["action"], err = json.Marshal(in.Action)
	if err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

func projectSourceActionRoute(s *Service, w http.ResponseWriter, r *http.Request, id string) (int, any, error) {
	if r.Method != "POST" {
		return 0, nil, methodError(w, "POST")
	}
	var in ProjectSourceAction
	if e := strictPlannerInput(w, r, &in, "action expected_revision expected_series_revision request_id related_revisions tasks_decision sessions_decision session_revisions completed_date reason"); e != nil {
		return 0, nil, e
	}
	var out CommandResult
	e := s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
		var e error
		out, e = commandOnce(tx, in.RequestID, "source-project/"+id, in.Action, stamp(s.now()), in, func() (CommandResult, error) {
			p, e := projectWithTree(tx, id)
			if e != nil {
				return CommandResult{}, e
			}
			if e = checkRevision(in.ExpectedRevision, p.RecordMeta); e != nil {
				return CommandResult{}, e
			}
			var sid string
			if e = tx.QueryRow(`SELECT series_id FROM project_repeat_state WHERE source_project_id=? OR series_id IN (SELECT series_id FROM entity_template_links WHERE project_id=? AND series_id IS NOT NULL)`, id, id).Scan(&sid); e != nil {
				return CommandResult{}, invalid("Это не исходное выполнение повторяющегося проекта")
			}
			v, e := getSeries(tx, sid)
			if e != nil {
				return CommandResult{}, e
			}
			if e = checkRevision(in.ExpectedSeriesRevision, v.RecordMeta); e != nil {
				return CommandResult{}, e
			}
			if p.ArchivedAt != nil {
				return CommandResult{}, invalid("Сначала верните проект из архива")
			}
			if in.Action != "restore" {
				if !active(p.Status) {
					return CommandResult{}, invalid("Выполнение уже закрыто")
				}
				if e = checkProjectPending(tx, &id); e != nil {
					return CommandResult{}, e
				}
			} else if active(p.Status) {
				return CommandResult{}, invalid("Проект уже открыт")
			}
			now := stamp(s.now())
			before := p
			switch in.Action {
			case "complete", "cancel", "skip":
				if in.Action == "complete" {
					if in.CompletedDate == nil {
						return CommandResult{}, invalid("Укажите фактическую дату")
					}
					if e = managementDate(*in.CompletedDate); e != nil {
						return CommandResult{}, e
					}
					if *in.CompletedDate > s.today() {
						return CommandResult{}, invalid("Фактическая дата не может быть в будущем")
					}
				}
				if e = s.closeProjectTasks(r.Context(), tx, "", p, in.Action, in.OccurrenceCommand, in.RequestID, now, false); e != nil {
					return CommandResult{}, e
				}
				p.Status = map[string]string{"complete": "completed", "cancel": "cancelled", "skip": "skipped"}[in.Action]
				p.ClosedAt = &now
			case "restore":
				p.Status = "planned"
				p.ClosedAt = nil
			default:
				return CommandResult{}, invalid("Неизвестное действие")
			}
			p.UpdatedAt = now
			if e = saveProject(tx, p); e != nil {
				return CommandResult{}, e
			}
			if _, e = tx.Exec(`UPDATE series SET revision=revision+1,updated_at=? WHERE id=?`, now, sid); e != nil {
				return CommandResult{}, e
			}
			p, e = projectWithTree(tx, id)
			if e != nil {
				return CommandResult{}, e
			}
			if e = event(tx, "project", id, "source_"+in.Action, in.RequestID, now, before, map[string]any{"project": p, "actual_date": in.CompletedDate, "reason": in.Reason}); e != nil {
				return CommandResult{}, e
			}
			if e = s.advanceProjectRepeat(tx, id); e != nil {
				return CommandResult{}, e
			}
			p, e = projectWithTree(tx, id)
			return CommandResult{Project: &p}, e
		})
		return e
	})
	return 200, out, e
}

package planner

import (
	"context"
	"database/sql"
	"net/http"
)

type AttentionSettings struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Revision     int64   `json:"revision"`
	SnoozedUntil *string `json:"snoozed_until"`
}
type SnoozeInput struct {
	Type             string `json:"type"`
	ExpectedRevision int64  `json:"expected_revision"`
	Until            string `json:"snoozed_until"`
	RequestID        string `json:"request_id"`
}

func attentionSettingsTx(tx *sql.Tx, kind, id, today string) (AttentionSettings, error) {
	v := AttentionSettings{ID: id}
	switch kind {
	case "task":
		task, e := getTask(tx, id)
		if e != nil {
			return v, e
		}
		v.Title = task.Title
		overdue := task.PlannedDate != nil && *task.PlannedDate < today || task.DueDate != nil && *task.DueDate < today
		if !active(task.Status) || !overdue {
			return v, invalid("Отложить можно напоминание об открытой просроченной задаче")
		}
	case "project":
		p, e := getProject(tx, id)
		if e != nil {
			return v, e
		}
		v.Title = p.Title
		if !active(p.Status) || p.ArchivedAt != nil || p.DueDate == nil || *p.DueDate >= today {
			return v, invalid("Отложить можно напоминание об открытом просроченном проекте")
		}
	case "occurrence":
		o, e := getOccurrence(tx, id)
		if e != nil {
			return v, e
		}
		v.Title = o.Task.Title
		if o.Project != nil {
			v.Title = o.Project.Title
		}
		if v.Title == "" {
			v.Title = "Пропущенное выполнение"
		}
		var unresolved int
		if e = tx.QueryRow(`SELECT count(*) FROM occurrence_incidents WHERE occurrence_id=? AND disposition='needs_decision'`, id).Scan(&unresolved); e != nil {
			return v, e
		}
		if unresolved == 0 {
			return v, invalid("Инцидент уже обработан")
		}
	default:
		return v, invalid("type: task, project или occurrence")
	}
	e := tx.QueryRow(`SELECT revision,snoozed_until FROM attention_state WHERE `+kind+`_id=?`, id).Scan(&v.Revision, &v.SnoozedUntil)
	if e == sql.ErrNoRows {
		e = nil
	}
	return v, e
}
func (s *Service) SnoozeAttention(ctx context.Context, id string, in SnoozeInput) (CommandResult, error) {
	if e := managementDate(in.Until); e != nil {
		return CommandResult{}, e
	}

	now := stamp(s.now())
	op, e := operation()
	if e != nil {
		return CommandResult{}, e
	}
	var result CommandResult
	e = s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var e error
		result, e = commandOnce(tx, in.RequestID, "attention/"+in.Type+"/"+id, "snooze", now, in, func() (CommandResult, error) {
			if in.Until <= s.today() {
				return CommandResult{}, invalid("Выберите будущую дату напоминания")
			}
			before, e := attentionSettingsTx(tx, in.Type, id, s.today())
			if e != nil {
				return CommandResult{}, e
			}
			if before.Revision != in.ExpectedRevision {
				return CommandResult{}, conflict("revision_conflict", "Настройки напоминания изменились. Просмотрите актуальную дату.", before)
			}
			// Type is validated against three constants above before being used as a column.
			_, e = tx.Exec(`INSERT INTO attention_state(id,`+in.Type+`_id,snoozed_until,created_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(`+in.Type+`_id) DO UPDATE SET snoozed_until=excluded.snoozed_until,revision=revision+1,updated_at=excluded.updated_at`, op, id, in.Until, now, now)
			if e != nil {
				return CommandResult{}, e
			}
			after, e := attentionSettingsTx(tx, in.Type, id, s.today())
			if e != nil {
				return CommandResult{}, e
			}
			if e = event(tx, in.Type, id, "attention_snoozed", op, now, before, after); e != nil {
				return CommandResult{}, e
			}
			return CommandResult{Attention: &after}, nil
		})
		return e
	})
	return result, e
}
func attentionSettingsRoute(s *Service, w http.ResponseWriter, r *http.Request, id string) (int, any, error) {
	if r.Method == "GET" {
		var result AttentionSettings
		e := s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
			var e error
			result, e = attentionSettingsTx(tx, r.URL.Query().Get("type"), id, s.today())
			return e
		})
		return 200, result, e
	}
	if r.Method != "POST" {
		return 0, nil, methodError(w, "GET, POST")
	}
	var in SnoozeInput
	if e := strictPlannerInput(w, r, &in, "type expected_revision snoozed_until request_id"); e != nil {
		return 0, nil, e
	}
	v, e := s.SnoozeAttention(r.Context(), id, in)
	return 200, v, e
}

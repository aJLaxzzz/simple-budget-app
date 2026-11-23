package planner

import (
	"context"
	"database/sql"
	"reflect"
	"time"
)

func validateSession(v SessionDTO) error {
	if e := validDate(&v.Date); e != nil {
		return e
	}
	if v.Timezone != "Europe/Moscow" {
		return invalid("Поддерживается только Europe/Moscow")
	}
	if (v.StartTime == nil) != (v.EndTime == nil) {
		return invalid("Укажите обе границы времени либо удалите обе")
	}
	if v.StartTime != nil {
		for _, p := range []*string{v.StartTime, v.EndTime} {
			parsed, e := time.Parse("15:04", *p)
			if e != nil || parsed.Format("15:04") != *p {
				return invalid("Время должно иметь формат HH:MM")
			}
		}
		if *v.EndTime <= *v.StartTime {
			return invalid("Окончание должно быть позже начала в пределах дня")
		}
	}
	return nil
}
func applySession(in SessionInput, v *SessionDTO) error {
	if e := applyString(in.Date, &v.Date, "date"); e != nil {
		return e
	}
	if e := applyString(in.Timezone, &v.Timezone, "timezone"); e != nil {
		return e
	}
	applyNullable(in.StartTime, &v.StartTime)
	applyNullable(in.EndTime, &v.EndTime)
	return validateSession(*v)
}
func sessionTaskOpen(tx *sql.Tx, id string) error {
	t, e := getTask(tx, id)
	if e != nil {
		return e
	}
	if !active(t.Status) {
		return invalid("Активная сессия требует открытой задачи; сначала восстановите задачу")
	}
	return nil
}
func (s *Service) CreateSession(ctx context.Context, in SessionInput) (SessionDTO, error) {
	v := SessionDTO{TaskID: in.TaskID, Timezone: "Europe/Moscow", Status: "planned"}
	if v.TaskID == "" {
		return v, invalid("Требуется task_id")
	}
	if e := applySession(in, &v); e != nil {
		return v, e
	}
	id, e := operation()
	if e != nil {
		return v, e
	}
	now := s.now().UTC().Format("2006-01-02T15:04:05.000000000Z")
	v.RecordMeta = RecordMeta{ID: id, Revision: 1, CreatedAt: now, UpdatedAt: now}
	e = s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		v, err = createOnce(tx, in.RequestID, "session", now, in, func() (SessionDTO, error) {
			if e := sessionTaskOpen(tx, v.TaskID); e != nil {
				return v, e
			}
			if e := insertSession(tx, v); e != nil {
				return v, e
			}
			return v, event(tx, "session", id, "created", id, now, nil, v)
		})
		return err
	})
	return v, e
}
func (s *Service) Session(ctx context.Context, id string) (SessionDTO, error) {
	var v SessionDTO
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error { var e error; v, e = getSession(tx, id); return e })
	return v, e
}
func (s *Service) Sessions(ctx context.Context, taskID, status string) ([]SessionDTO, error) {
	if status != "" && status != "planned" && status != "completed" && status != "cancelled" {
		return nil, invalid("Некорректный статус сессии")
	}
	var items []SessionDTO
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		if taskID != "" {
			if _, e := getTask(tx, taskID); e != nil {
				return e
			}
		}
		var e error
		items, e = listSessions(tx, "WHERE (?='' OR task_id=?) AND (?='' OR status=?)", taskID, taskID, status, status)
		return e
	})
	return items, e
}
func (s *Service) ChangeSession(ctx context.Context, id, action string, in SessionInput) (SessionDTO, error) {
	var result SessionDTO
	instant := s.now()
	now := instant.UTC().Format("2006-01-02T15:04:05.000000000Z")
	op, e := operation()
	if e != nil {
		return result, e
	}
	e = s.store.Transaction(ctx, func(tx *sql.Tx) error {
		before, e := getSession(tx, id)
		if e != nil {
			return e
		}
		if e = checkRevision(in.ExpectedRevision, before.RecordMeta); e != nil {
			return e
		}
		v := before
		if action == "restore" {
			if before.Status != "completed" && before.Status != "cancelled" {
				return invalid("Восстановить можно только завершённую или отменённую сессию")
			}
			v.Status = "planned"
			v.CompletedDate = nil
			v.CompletedAt = nil
		} else if !active(before.Status) {
			return invalid("Закрытую сессию сначала необходимо восстановить")
		}
		if action != "cancel" {
			if e = sessionTaskOpen(tx, v.TaskID); e != nil {
				return e
			}
		}
		if e = applySession(in, &v); e != nil {
			return e
		}
		if action != "complete" && in.CompletedDate != nil {
			return invalid("completed_date допустима только при завершении")
		}
		eventAction := "edited"
		switch action {
		case "complete":
			if in.CompletedDate == nil {
				return invalid("Укажите фактическую completed_date")
			}
			if e = validDate(in.CompletedDate); e != nil {
				return e
			}
			loc, e := time.LoadLocation("Europe/Moscow")
			if e != nil {
				return e
			}
			if *in.CompletedDate > instant.In(loc).Format("2006-01-02") {
				return invalid("Фактическая дата не может быть в будущем")
			}
			v.Status = "completed"
			v.CompletedDate = in.CompletedDate
			v.CompletedAt = &now
			eventAction = "completed"
		case "cancel":
			v.Status = "cancelled"
			eventAction = "cancelled"
		case "restore":
			eventAction = "restored"
		case "reschedule":
			if !in.Date.Set {
				return invalid("Для переноса требуется date")
			}
			eventAction = "rescheduled"
		case "":
			if v.Date != before.Date {
				eventAction = "rescheduled"
			}
		default:
			return invalid("Неизвестное действие")
		}
		if reflect.DeepEqual(v, before) {
			return invalid("Нет изменений")
		}
		v.UpdatedAt = now
		if e = saveSession(tx, v); e != nil {
			return e
		}
		v.Revision++
		if e = event(tx, "session", id, eventAction, op, now, before, v); e != nil {
			return e
		}
		result = v
		return nil
	})
	return result, e
}

// Close all active sessions, including overdue ones. Keeping past active sessions
// would violate the invariant for a closed task just as keeping future ones does.
func closeTaskSessions(tx *sql.Tx, taskIDs []string, decision string, revisions map[string]int64, op, now string) error {
	if decision != "" && decision != "cancel" {
		return invalid("sessions_decision должен быть cancel; статус skipped у сессий не предусмотрен")
	}
	sessions := []SessionDTO{}
	for _, id := range taskIDs {
		items, e := listSessions(tx, "WHERE task_id=? AND status IN ('planned','in_progress')", id)
		if e != nil {
			return e
		}
		sessions = append(sessions, items...)
	}
	if len(sessions) > 0 && decision != "cancel" {
		return &APIError{Status: 422, Code: "sessions_decision_required", Message: "Для закрытия задачи подтвердите отмену всех активных сессий: sessions_decision=cancel и session_revisions", Current: sessions}
	}
	for _, v := range sessions {
		if e := checkRevision(revisions[v.ID], v.RecordMeta); e != nil {
			return e
		}
	}
	for _, v := range sessions {
		before := v
		v.Status = "cancelled"
		v.UpdatedAt = now
		if e := saveSession(tx, v); e != nil {
			return e
		}
		v.Revision++
		if e := event(tx, "session", v.ID, "cancelled", op, now, before, v); e != nil {
			return e
		}
	}
	return nil
}

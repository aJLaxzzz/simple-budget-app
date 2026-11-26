package planner

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"time"
)

type Service struct {
	store *Store
	now   func() time.Time
}

func NewService(store *Store) *Service { return &Service{store: store, now: time.Now} }
func active(status string) bool        { return status == "planned" || status == "in_progress" }
func statusValid(status string) bool {
	return active(status) || status == "completed" || status == "cancelled"
}
func applyString(field Field[string], dst *string, name string) error {
	if !field.Set {
		return nil
	}
	if field.Value == nil {
		return invalid(name + " не может быть null")
	}
	*dst = *field.Value
	return nil
}
func applyNullable(field Field[string], dst **string) {
	if field.Set {
		*dst = field.Value
	}
}
func validDate(p *string) error {
	if p == nil {
		return nil
	}
	if _, e := parseDate(Date(*p)); e != nil {
		return invalid("Некорректная дата: ожидается YYYY-MM-DD")
	}
	return nil
}
func validateTitle(title string) error {
	if strings.TrimSpace(title) == "" || len(title) > 1000 {
		return invalid("Название обязательно и не должно превышать 1000 байт")
	}
	return nil
}
func validateProject(p ProjectDTO) error {
	if e := validateTitle(p.Title); e != nil {
		return e
	}
	if len(p.Description) > 50000 {
		return invalid("Описание слишком длинное")
	}
	if !statusValid(p.Status) {
		return invalid("Некорректный статус проекта")
	}
	if e := validDate(p.StartDate); e != nil {
		return e
	}
	return validDate(p.DueDate)
}
func validateTask(tx *sql.Tx, t TaskDTO) error {
	if e := validateTitle(t.Title); e != nil {
		return e
	}
	if len(t.Description) > 50000 {
		return invalid("Описание слишком длинное")
	}
	if !statusValid(t.Status) && t.Status != "skipped" {
		return invalid("Некорректный статус задачи")
	}
	if t.Priority != "low" && t.Priority != "normal" && t.Priority != "high" {
		return invalid("Некорректный приоритет")
	}
	for _, d := range []*string{t.PlannedDate, t.DueDate} {
		if e := validDate(d); e != nil {
			return e
		}
	}
	if t.ScheduledTime != nil {
		parsed, e := time.Parse("15:04", *t.ScheduledTime)
		if e != nil || parsed.Format("15:04") != *t.ScheduledTime || t.PlannedDate == nil {
			return invalid("Время должно иметь формат HH:MM и назначенную дату")
		}
	}
	if t.ProjectID != nil {
		p, e := getProject(tx, *t.ProjectID)
		if e != nil {
			return e
		}
		if p.ArchivedAt != nil {
			return invalid("Проект архивирован")
		}
		if p.OccurrenceID != nil && !active(p.Status) && active(t.Status) {
			return invalid("Сначала восстановите закрытый экземпляр проекта")
		}
	}
	if t.ParentTaskID != nil {
		if *t.ParentTaskID == t.ID {
			return invalid("Задача не может быть своим родителем")
		}
		p, e := getTask(tx, *t.ParentTaskID)
		if e != nil {
			return e
		}
		if p.ParentTaskID != nil || p.ProjectID == nil || t.ProjectID == nil || *p.ProjectID != *t.ProjectID {
			return invalid("Подзадача должна иметь корневого родителя в том же проекте")
		}
		var children int
		if e = taskChildCount(tx, t.ID, &children); e != nil {
			return e
		}
		if children > 0 {
			return invalid("Задачу с детьми нельзя превратить в подзадачу")
		}
	}
	return nil
}
func applyTask(in TaskInput, t *TaskDTO) error {
	applyNullable(in.ColorID, &t.ColorID)
	applyNullable(in.IconID, &t.IconID)
	for _, f := range []struct {
		field Field[string]
		dst   *string
		name  string
	}{{in.Title, &t.Title, "title"}, {in.Description, &t.Description, "description"}, {in.Priority, &t.Priority, "priority"}, {in.Status, &t.Status, "status"}} {
		if e := applyString(f.field, f.dst, f.name); e != nil {
			return e
		}
	}
	t.Title = strings.TrimSpace(t.Title)
	applyNullable(in.ProjectID, &t.ProjectID)
	applyNullable(in.ParentTaskID, &t.ParentTaskID)
	applyNullable(in.PlannedDate, &t.PlannedDate)
	applyNullable(in.ScheduledTime, &t.ScheduledTime)
	applyNullable(in.DueDate, &t.DueDate)
	return nil
}
func applyProject(in ProjectInput, p *ProjectDTO) error {
	applyNullable(in.ColorID, &p.ColorID)
	applyNullable(in.IconID, &p.IconID)
	for _, f := range []struct {
		field Field[string]
		dst   *string
		name  string
	}{{in.Title, &p.Title, "title"}, {in.Description, &p.Description, "description"}, {in.Status, &p.Status, "status"}} {
		if e := applyString(f.field, f.dst, f.name); e != nil {
			return e
		}
	}
	p.Title = strings.TrimSpace(p.Title)
	applyNullable(in.StartDate, &p.StartDate)
	applyNullable(in.DueDate, &p.DueDate)
	return validateProject(*p)
}
func operation() (string, error) { id, e := NewID(); return string(id), e }
func (s *Service) CreateProject(ctx context.Context, in ProjectInput) (ProjectDTO, error) {
	p := ProjectDTO{Title: "", Status: "planned"}
	if e := applyProject(in, &p); e != nil {
		return p, e
	}
	if !active(p.Status) {
		return p, invalid("Создайте проект в запланированном состоянии или в работе")
	}
	id, e := operation()
	if e != nil {
		return p, e
	}
	now := s.now().UTC().Format("2006-01-02T15:04:05.000000000Z")
	p.RecordMeta = RecordMeta{ID: id, Revision: 1, CreatedAt: now, UpdatedAt: now}
	e = s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		p, err = createOnce(tx, in.RequestID, "project", now, in, func() (ProjectDTO, error) {
			if e := insertProject(tx, p); e != nil {
				return p, e
			}
			var readErr error
			p, readErr = getProject(tx, id)
			if readErr != nil {
				return p, readErr
			}
			return p, event(tx, "project", id, "created", id, now, nil, p)
		})
		return err
	})
	return p, e
}
func (s *Service) Project(ctx context.Context, id string) (ProjectDTO, error) {
	var p ProjectDTO
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var e error
		p, e = getProject(tx, id)
		if e != nil {
			return e
		}
		return decorateProject(tx, &p, true)
	})
	return p, e
}
func (s *Service) Projects(ctx context.Context, status string, archived bool) ([]ProjectDTO, error) {
	if status != "" && !statusValid(status) {
		return nil, invalid("Некорректный фильтр статуса")
	}
	items := []ProjectDTO{}
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var e error
		items, e = listProjects(tx, status, archived)
		if e != nil {
			return e
		}
		for i := range items {
			if e = decorateProject(tx, &items[i], false); e != nil {
				return e
			}
		}
		return nil
	})
	return items, e
}
func (s *Service) ChangeProject(ctx context.Context, id, action string, in ProjectInput) (ProjectDTO, error) {
	var result ProjectDTO
	now := s.now().UTC().Format("2006-01-02T15:04:05.000000000Z")
	op, e := operation()
	if e != nil {
		return result, e
	}
	e = s.store.Transaction(ctx, func(tx *sql.Tx) error {
		apply := func() error {
			before, e := getProject(tx, id)
			if e != nil {
				return e
			}
			if e = checkRevision(in.ExpectedRevision, before.RecordMeta); e != nil {
				return e
			}
			if before.ArchivedAt != nil {
				return invalid("Архивированный проект доступен только для чтения")
			}
			p := before
			if action == "complete" || action == "cancel" {
				if e = checkProjectPending(tx, &id); e != nil {
					return e
				}
			}
			if e = applyProject(in, &p); e != nil {
				return e
			}
			switch action {
			case "complete":
				p.Status = "completed"
			case "cancel":
				p.Status = "cancelled"
			case "archive":
				p.ArchivedAt = &now
			case "":
			default:
				return invalid("Неизвестное действие")
			}
			if (p.Status != before.Status && !active(p.Status)) || p.ArchivedAt != nil {
				var linked int
				if e = tx.QueryRow(`SELECT count(*) FROM series s JOIN series_versions v ON v.series_id=s.id WHERE s.source_type='task' AND s.state!='archived' AND s.disabled_at IS NULL AND v.version=(SELECT max(v2.version) FROM series_versions v2 WHERE v2.series_id=s.id) AND (json_extract(v.source_snapshot_json,'$.project_id')=? OR EXISTS(SELECT 1 FROM tasks t JOIN occurrences o ON o.id=t.occurrence_id WHERE t.project_id=? AND o.series_id=s.id))`, id, id).Scan(&linked); e != nil {
					return e
				}
				if linked > 0 {
					return conflict("nested_series_conflict", "Проект содержит независимые регулярные задачи. Сначала отдельно остановите их серии или перенесите их источник и выполнения; закрытие проекта не отменяет серии автоматически.", before)
				}
			}
			if p.Status == "completed" && before.Status != "completed" {
				if e = requireProjectComplete(tx, id); e != nil {
					return e
				}
			}
			if before.ProjectMode == "sequential" && before.OccurrenceID == nil && p.Status != before.Status && !active(p.Status) {
				var open int
				if e = tx.QueryRow(`SELECT count(*) FROM tasks WHERE project_id=? AND status IN('planned','in_progress')`, id).Scan(&open); e != nil {
					return e
				}
				var sessions int
				if e = tx.QueryRow(`SELECT count(*) FROM sessions WHERE task_id IN(SELECT id FROM tasks WHERE project_id=?) AND status='in_progress'`, id).Scan(&sessions); e != nil {
					return e
				}
				if open > 0 || sessions > 0 {
					return conflict("project_open_work", "Сначала завершите открытые задачи и активные периоды работы текущего проекта.", before)
				}
			}
			if before.OccurrenceID != nil && p.Status != before.Status && !active(p.Status) {
				return conflict("project_occurrence_command_required", "Закройте экземпляр через /occurrences/{id}, явно обработав открытые задачи и сессии", before)
			}
			if p.Status != before.Status && !active(before.Status) {
				return invalid("Недопустимый переход статуса проекта")
			}
			if p.Status != before.Status && !active(p.Status) {
				p.ClosedAt = &now
			}
			if p.Status == before.Status && reflect.DeepEqual(p, before) {
				return invalid("Нет изменений")
			}
			p.UpdatedAt = now
			if e = saveProject(tx, p); e != nil {
				return e
			}
			p.Revision++
			eventAction := "edited"
			if p.ArchivedAt != nil {
				eventAction = "archived"
			} else if p.Status != before.Status {
				eventAction = statusAction(p.Status)
			}
			if e = event(tx, "project", id, eventAction, op, now, before, p); e != nil {
				return e
			}
			if e = trackProjectException(tx, before, p, op, now); e != nil {
				return e
			}
			if e = s.advanceProjectRepeat(tx, id); e != nil {
				return e
			}
			result = p
			return decorateProject(tx, &result, true)
		}
		if in.RequestID == "" {
			return apply()
		}
		out, err := commandOnce(tx, in.RequestID, "project/"+id, action, now, in, func() (CommandResult, error) { err := apply(); return CommandResult{Project: &result}, err })
		if out.Project != nil {
			result = *out.Project
		}
		return err
	})
	return result, e
}
func statusAction(status string) string {
	switch status {
	case "completed":
		return "completed"
	case "cancelled":
		return "cancelled"
	case "skipped":
		return "skipped"
	}
	return "status_changed"
}
func (s *Service) CreateTask(ctx context.Context, in TaskInput) (TaskDTO, error) {
	t := TaskDTO{Status: "planned", Priority: "normal"}
	if e := applyTask(in, &t); e != nil {
		return t, e
	}
	if !active(t.Status) || in.CompletedDate != nil || in.ChildrenDecision != "" {
		return t, invalid("Создайте задачу в запланированном состоянии или в работе")
	}
	id, e := operation()
	if e != nil {
		return t, e
	}
	now := s.now().UTC().Format("2006-01-02T15:04:05.000000000Z")
	t.RecordMeta = RecordMeta{ID: id, Revision: 1, CreatedAt: now, UpdatedAt: now}
	e = s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		t, err = createOnce(tx, in.RequestID, "task", now, in, func() (TaskDTO, error) {
			if e := validateTask(tx, t); e != nil {
				return t, e
			}
			if e := insertTask(tx, t); e != nil {
				return t, e
			}
			var readErr error
			t, readErr = getTask(tx, id)
			if readErr != nil {
				return t, readErr
			}
			return t, event(tx, "task", id, "created", id, now, nil, t)
		})
		return err
	})
	return t, e
}
func (s *Service) Task(ctx context.Context, id string) (TaskDTO, error) {
	var t TaskDTO
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var e error
		t, e = getTask(tx, id)
		if e != nil {
			return e
		}
		return decorateTask(tx, &t)
	})
	return t, e
}
func (s *Service) Tasks(ctx context.Context, status string, project *string) ([]TaskDTO, error) {
	if status != "" && !statusValid(status) && status != "skipped" && status != "active" {
		return nil, invalid("Некорректный фильтр статуса")
	}
	var items []TaskDTO
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		where := "WHERE (?='' OR status=?)"
		args := []any{status, status}
		if status == "active" {
			where = "WHERE status IN ('planned','in_progress')"
			args = nil
		}
		if project != nil {
			if *project == "" {
				where += " AND project_id IS NULL"
			} else {
				where += " AND project_id=?"
				args = append(args, *project)
			}
		}
		var e error
		items, e = listTasks(tx, where, args...)
		if e != nil {
			return e
		}
		for i := range items {
			if e = decorateTask(tx, &items[i]); e != nil {
				return e
			}
		}
		return nil
	})
	return items, e
}
func (s *Service) ChangeTask(ctx context.Context, id, action string, in TaskInput) (TaskDTO, error) {
	var result TaskDTO
	instant := s.now()
	now := instant.UTC().Format("2006-01-02T15:04:05.000000000Z")
	op, e := operation()
	if e != nil {
		return result, e
	}
	e = s.store.Transaction(ctx, func(tx *sql.Tx) error {
		if in.RequestID == "" {
			return s.changeTaskTx(ctx, tx, id, action, in, op, now, instant, &result, taskChangeOptions{})
		}
		cmd, e := commandOnce(tx, in.RequestID, "task/"+id, action, now, in, func() (CommandResult, error) {
			e := s.changeTaskTx(ctx, tx, id, action, in, op, now, instant, &result, taskChangeOptions{})
			return CommandResult{Task: &result}, e
		})
		if e == nil && cmd.Task != nil {
			result = *cmd.Task
		}
		return e
	})
	return result, e
}

type taskChangeOptions struct {
	InstanceOnly    bool
	DynamicDecision string
	ProjectSkip     bool
	AllowRestore    bool
	HistoricalOnly  bool
	Reason          string
	PauseID         *string
}

func (s *Service) changeTaskTx(ctx context.Context, tx *sql.Tx, id, action string, in TaskInput, op, now string, instant time.Time, result *TaskDTO, options taskChangeOptions) error {
	before, e := getTask(tx, id)
	if e != nil {
		return e
	}
	if action == "complete" || (in.Status.Set && in.Status.Value != nil && *in.Status.Value == "in_progress") {
		if e = checkProjectPending(tx, before.ProjectID); e != nil {
			return e
		}
	}
	if e = checkRevision(in.ExpectedRevision, before.RecordMeta); e != nil {
		return e
	}
	t := before
	if in.Status.Set && in.Status.Value != nil && *in.Status.Value == "skipped" && action != "skip" {
		return invalid("Используйте команду пропуска экземпляра")
	}
	if e = applyTask(in, &t); e != nil {
		return e
	}
	switch action {
	case "complete":
		t.Status = "completed"
	case "skip":
		if before.OccurrenceID == nil && !options.ProjectSkip {
			return invalid("Пропуск доступен только для экземпляра серии")
		}
		t.Status = "skipped"
	case "cancel":
		t.Status = "cancelled"
	case "restore":
		if before.OccurrenceID != nil && !options.AllowRestore {
			return invalid("Для восстановления цикла используйте команду экземпляра с проверкой состояния серии")
		}
		if before.Status != "completed" && before.Status != "cancelled" && before.Status != "skipped" {
			return invalid("Восстановить можно только завершённую, отменённую или пропущенную задачу")
		}
		t.Status = "planned"
	case "reschedule":
		if !in.PlannedDate.Set {
			return invalid("Для переноса требуется planned_date")
		}
	case "":
	default:
		return invalid("Неизвестное действие")
	}
	changedStatus := t.Status != before.Status
	if changedStatus && !active(before.Status) && action != "restore" && !options.AllowRestore {
		return invalid("Сначала восстановите закрытую задачу")
	}
	if action != "" && action != "reschedule" && !changedStatus {
		return invalid("Недопустимый повтор действия")
	}
	if e = validateTask(tx, t); e != nil {
		return e
	}
	completing := changedStatus && t.Status == "completed"
	if completing {
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
			return invalid("Фактическая дата выполнения не может быть в будущем")
		}
		t.CompletedDate = in.CompletedDate
		t.CompletedAt = &now
	} else if in.CompletedDate != nil || in.ChildrenDecision != "" {
		return invalid("Дата выполнения и решение по детям допустимы только при завершении")
	}
	if action == "restore" {
		t.CompletedDate = nil
		t.CompletedAt = nil
	}
	children, e := listTasks(tx, "WHERE parent_task_id=?", id)
	if e != nil {
		return e
	}
	moving := !reflect.DeepEqual(t.ProjectID, before.ProjectID)
	if moving && t.ProjectID != nil {
		regular := t.SeriesID != nil
		for _, c := range children {
			regular = regular || c.SeriesID != nil
		}
		if regular {
			if e = independentTaskProject(tx, *t.ProjectID); e != nil {
				return e
			}
		}
	}
	if moving && len(children) > 0 && t.ProjectID == nil {
		return invalid("Дерево с подзадачами должно принадлежать проекту")
	}
	open := 0
	for _, c := range children {
		if active(c.Status) {
			open++
		}
	}
	if completing && open > 0 && in.ChildrenDecision != "complete" && in.ChildrenDecision != "keep_open" {
		return invalid("Выберите children_decision: complete или keep_open")
	}
	if completing && in.ChildrenDecision != "" && in.ChildrenDecision != "complete" && in.ChildrenDecision != "keep_open" {
		return invalid("Неизвестное решение по детям")
	}
	for _, c := range children {
		if moving || (completing && active(c.Status) && !c.Dynamic) {
			if e = checkRevision(in.RelatedRevisions[c.ID], c.RecordMeta); e != nil {
				return e
			}
		}
	}
	closing := changedStatus && (t.Status == "completed" || t.Status == "cancelled" || t.Status == "skipped")
	if !closing && (in.SessionsDecision != "" || len(in.SessionRevisions) > 0) {
		return invalid("Решение по сессиям допустимо только при закрытии задачи")
	}
	if closing {
		closingIDs := []string{id}
		if completing && in.ChildrenDecision == "complete" {
			for _, c := range children {
				if active(c.Status) && !c.Dynamic {
					closingIDs = append(closingIDs, c.ID)
				}
			}
		}
		if e = closeTaskSessions(tx, closingIDs, in.SessionsDecision, in.SessionRevisions, op, now); e != nil {
			return e
		}
	}
	if reflect.DeepEqual(t, before) {
		return invalid("Нет изменений")
	}
	t.UpdatedAt = now
	if e = saveTask(tx, t); e != nil {
		return e
	}
	t.Revision++
	eventAction := "edited"
	if action == "restore" {
		eventAction = "restored"
	} else if changedStatus {
		eventAction = statusAction(t.Status)
	} else if moving || !reflect.DeepEqual(t.ParentTaskID, before.ParentTaskID) {
		eventAction = "moved_project"
	} else if !reflect.DeepEqual(t.PlannedDate, before.PlannedDate) || !reflect.DeepEqual(t.ScheduledTime, before.ScheduledTime) {
		eventAction = "rescheduled"
	}
	actor := "owner"
	if options.PauseID != nil {
		actor = "system"
	}
	if e = eventAs(tx, "task", id, eventAction, actor, op, now, before, t); e != nil {
		return e
	}
	for _, c := range children {
		current, e := getTask(tx, c.ID)
		if e != nil {
			return e
		}
		if moving {
			// The existing DB trigger moved the child and incremented its revision.
			if _, e = stampTask(tx, c.ID, now); e != nil {
				return e
			}
			current.UpdatedAt = now
			if e = event(tx, "task", c.ID, "moved_project", op, now, c, current); e != nil {
				return e
			}
		}
		if completing && in.ChildrenDecision == "complete" && active(c.Status) && !c.Dynamic {
			old := current
			current.Status = "completed"
			current.CompletedDate = t.CompletedDate
			current.CompletedAt = &now
			current.UpdatedAt = now
			if e = saveTask(tx, current); e != nil {
				return e
			}
			current.Revision++
			if e = event(tx, "task", c.ID, "completed", op, now, old, current); e != nil {
				return e
			}
			if e = s.advanceTaskSeries(ctx, tx, old, current, op, now); e != nil {
				return e
			}
		}
	}
	if e = s.advanceTaskSeries(ctx, tx, before, t, op, now, options); e != nil {
		return e
	}
	*result, e = getTask(tx, t.ID)
	if e != nil {
		return e
	}
	return decorateTask(tx, result)

}
func (s *Service) History(ctx context.Context, kind, id string, limit, offset int) ([]HistoryDTO, error) {
	if kind != "task" && kind != "project" && kind != "session" && kind != "series" && kind != "occurrence" && kind != "template" {
		return nil, invalid("object_type должен быть task, project, session, series или occurrence")
	}
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, invalid("limit: 1..100, offset: >=0")
	}
	items := []HistoryDTO{}
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		if kind == "template" {
			if _, e := getTemplateKind(tx, id, "", ""); e != nil {
				return e
			}
		} else if kind == "occurrence" {
			if _, e := getOccurrence(tx, id); e != nil {
				return e
			}
		} else if kind == "series" {
			if _, e := getSeries(tx, id); e != nil {
				return e
			}
		} else if kind == "session" {
			if _, e := getSession(tx, id); e != nil {
				return e
			}
		} else if kind == "task" {
			if _, e := getTask(tx, id); e != nil {
				return e
			}
		} else {
			if _, e := getProject(tx, id); e != nil {
				return e
			}
		}
		var e error
		items, e = listHistory(tx, kind, id, limit, offset)
		return e
	})
	return items, e
}

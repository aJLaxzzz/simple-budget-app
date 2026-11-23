package planner

import (
	"context"
	"database/sql"
	"reflect"
)

func (s *Service) projectOccurrenceCommandTx(ctx context.Context, tx *sql.Tx, v SeriesDTO, o TaskOccurrenceDTO, action string, in OccurrenceCommand, op, now string, automatic bool) (CommandResult, error) {
	copied := map[string]int64{}
	for k, v := range in.RelatedRevisions {
		copied[k] = v
	}
	in.RelatedRevisions = copied
	fail := func(e error) (CommandResult, error) { return CommandResult{}, e }
	if e := checkRevision(in.ExpectedRevision, o.RecordMeta); e != nil {
		return fail(e)
	}
	if e := checkRevision(in.ExpectedSeriesRevision, v.RecordMeta); e != nil {
		return fail(e)
	}
	if o.Project != nil {
		if e := checkRevision(in.ExpectedProjectRevision, o.Project.RecordMeta); e != nil {
			return fail(e)
		}
	} else if in.ExpectedProjectRevision != 0 {
		return fail(invalid("Компактный пропуск ещё не имеет revision проекта"))
	}
	if in.ExpectedTaskRevision != 0 || in.BranchDecision != "" || in.ChildrenDecision != "" {
		return fail(invalid("Поля задачи/ветки неприменимы к проекту"))
	}
	if len(in.Reason) > 5000 {
		return fail(invalid("Причина слишком длинная"))
	}
	if in.PlannedDate != nil && action != "reschedule" {
		return fail(invalid("planned_date допустима только при переносе"))
	}
	if in.CompletedDate != nil && action != "complete" {
		return fail(invalid("completed_date допустима только при завершении"))
	}
	if action != "reschedule" && in.TasksDateDecision != "" {
		return fail(invalid("tasks_date_decision допустимо только при переносе"))
	}
	if in.CompletedDate != nil {
		if e := managementDate(*in.CompletedDate); e != nil {
			return fail(e)
		}
		if *in.CompletedDate > s.today() {
			return fail(invalid("Фактическая дата не может быть в будущем"))
		}
	}
	before := o
	if action == "review" {
		if o.ProjectStatus != "skipped" {
			return fail(invalid("Рассмотреть можно только пропуск"))
		}
		if in.TasksDecision != "" || in.SessionsDecision != "" || len(in.SessionRevisions) > 0 || len(in.RelatedRevisions) > 0 {
			return fail(invalid("Рассмотрение не изменяет задачи или сессии"))
		}
		if e := resolveIncidents(tx, o.ID, "acknowledged", op, now); e != nil {
			return fail(e)
		}
	} else {
		switch action {
		case "restore", "reschedule", "skip", "cancel", "complete":
		default:
			return fail(invalid("Неизвестная команда проекта"))
		}
		if action == "reschedule" {
			if in.PlannedDate == nil {
				return fail(invalid("Укажите planned_date — новую дату начала проекта"))
			}
			if e := managementDate(*in.PlannedDate); e != nil {
				return fail(e)
			}
			if in.TasksDateDecision != "keep" && in.TasksDateDecision != "shift" {
				return fail(invalid("Выберите tasks_date_decision: keep или shift"))
			}
		}
		closed := !active(o.ProjectStatus)
		if (action == "restore" && !closed) || (action == "skip" && closed) || (action == "cancel" && o.ProjectStatus == "completed") || (action == "complete" && o.ProjectStatus == "completed") || (action == "cancel" && o.ProjectStatus == "cancelled") {
			return fail(invalid("Недопустимый переход статуса проекта"))
		}
		restores := action == "restore" || (action == "reschedule" && closed) || (action == "complete" && closed)
		newlyMaterialized := o.Compact && (restores || action == "complete")
		if o.Compact && (restores || action == "complete") {
			date := o.AdjustedDate
			if o.ManualDate != nil {
				date = *o.ManualDate
			}
			if in.PlannedDate != nil {
				date = *in.PlannedDate
			}
			var e error
			o, e = s.restoreCompactProject(tx, v, o, date, op, now)
			if e != nil {
				return fail(e)
			}
		}
		if o.Project != nil {
			if newlyMaterialized {
				for _, t := range o.Project.Tasks {
					in.RelatedRevisions[t.ID] = t.Revision
				}
			}
			p := *o.Project
			old := p
			if p.ArchivedAt != nil {
				return fail(conflict("project_archived", "Сначала разрешите архивное состояние проекта", p))
			}
			if restores {
				p.Status = "planned"
				p.ClosedAt = nil
				p.UpdatedAt = now
				if e := saveProject(tx, p); e != nil {
					return fail(e)
				}
				p.Revision++
				restoreInput := in
				if action == "complete" {
					restoreInput.TasksDecision = "restore_closed"
				}
				if e := s.restoreProjectTasks(ctx, tx, o.ID, p, restoreInput, op, now); e != nil {
					return fail(e)
				}
			}
			if action == "reschedule" {
				if !newlyMaterialized {
					if e := s.moveProjectTasks(ctx, tx, p, *in.PlannedDate, in, op, now); e != nil {
						return fail(e)
					}
				}
				p.StartDate = in.PlannedDate
			}
			if action == "skip" || action == "cancel" || action == "complete" {
				if e := s.closeProjectTasks(ctx, tx, o.ID, p, action, in, op, now, automatic); e != nil {
					return fail(e)
				}
				p.Status = map[string]string{"skip": "skipped", "cancel": "cancelled", "complete": "completed"}[action]
				p.ClosedAt = &now
			}
			p.UpdatedAt = now
			if e := saveProject(tx, p); e != nil {
				return fail(e)
			}
			p.Revision++
			actor := "owner"
			if automatic {
				actor = "system"
			}
			if e := eventAs(tx, "project", p.ID, map[string]string{"skip": "skipped", "cancel": "cancelled", "complete": "completed", "restore": "restored", "reschedule": "rescheduled"}[action], actor, op, now, old, p); e != nil {
				return fail(e)
			}
			o.Project = &p
			o.ProjectStatus = p.Status
		}
		if action == "skip" || action == "cancel" {
			kind := map[string]string{"skip": "skipped", "cancel": "cancelled"}[action]
			source := "user"
			var pauseID *string
			if automatic {
				source = "pause"
				pauseID = &in.Reason
			}
			reason := in.Reason
			if automatic {
				if e := tx.QueryRow("SELECT reason FROM series_pauses WHERE id=?", *pauseID).Scan(&reason); e != nil {
					return fail(e)
				}
				if reason == "" {
					reason = "Пропуск проекта во время паузы"
				}
			}
			if reason == "" {
				reason = map[string]string{"skip": "Проект пропущен пользователем", "cancel": "Проект отменён пользователем"}[action]
			}
			var planned *string
			if o.Project != nil {
				planned = o.Project.StartDate
			}
			if e := registerOccurrenceIncident(tx, o, planned, kind, source, reason, pauseID, op, now); e != nil {
				return fail(e)
			}
			if action == "cancel" {
				if e := resolveIncidents(tx, o.ID, "cancelled", op, now); e != nil {
					return fail(e)
				}
			}
		} else {
			if _, e := tx.Exec("UPDATE occurrences SET outcome=NULL WHERE id=?", o.ID); e != nil {
				return fail(e)
			}
			resolution := map[string]string{"restore": "restored", "reschedule": "rescheduled", "complete": "completed"}[action]
			if e := resolveIncidents(tx, o.ID, resolution, op, now); e != nil {
				return fail(e)
			}
		}
		if action == "reschedule" || action == "restore" && !automatic {
			var date *string
			if action == "reschedule" {
				date = in.PlannedDate
			} else {
				date = before.ManualDate
			}
			if _, e := tx.Exec("UPDATE occurrences SET manual_override=1,manual_date=? WHERE id=?", date, o.ID); e != nil {
				return fail(e)
			}
		}
		if automatic && restores && o.Project != nil {
			keys, e := projectKeys(tx, o.Project.ID)
			if e != nil {
				return fail(e)
			}
			if e = projectBaseline(tx, o.Project.ID, keys); e != nil {
				return fail(e)
			}
		}
	}
	if _, e := tx.Exec("UPDATE occurrences SET revision=revision+1,updated_at=? WHERE id=?", now, o.ID); e != nil {
		return fail(e)
	}
	if _, e := tx.Exec("UPDATE series SET revision=revision+1,updated_at=? WHERE id=?", now, v.ID); e != nil {
		return fail(e)
	}
	after, e := getOccurrence(tx, o.ID)
	if e != nil {
		return fail(e)
	}
	actor := "owner"
	if automatic {
		actor = "system"
	}
	if e = eventAs(tx, "occurrence", o.ID, "project_"+action, actor, op, now, before, after); e != nil {
		return fail(e)
	}
	if e = eventAs(tx, "series", v.ID, "project_"+action, actor, op, now, nil, map[string]any{"occurrence_id": o.ID}); e != nil {
		return fail(e)
	}
	return CommandResult{Occurrence: &after}, nil
}
func (s *Service) closeProjectTasks(ctx context.Context, tx *sql.Tx, occ string, p ProjectDTO, action string, in OccurrenceCommand, op, now string, automatic bool) error {
	var nested string
	e := tx.QueryRow(`SELECT s.id FROM series s JOIN series_versions v ON v.series_id=s.id WHERE s.source_type='task' AND s.state!='archived' AND v.version=(SELECT max(version) FROM series_versions WHERE series_id=s.id) AND json_extract(v.source_snapshot_json,'$.project_id')=? LIMIT 1`, p.ID).Scan(&nested)
	if e == nil {
		return conflict("nested_series_conflict", "Сначала отдельно архивируйте серию задач, создающую выполнения в этом проекте, и перенесите её экземпляры из проекта", map[string]any{"series_id": nested, "project_id": p.ID})
	}
	if e != sql.ErrNoRows {
		return e
	}
	tasks, e := listTasks(tx, "WHERE project_id=?", p.ID)
	if e != nil {
		return e
	}
	want := map[string]string{"skip": "skip_open", "cancel": "cancel_open", "complete": "complete_open"}[action]
	for _, t := range tasks {
		if t.OccurrenceID != nil {
			return conflict("nested_series_conflict", "Задача имеет собственную серию; после отдельной обработки серии перенесите её экземпляры из проекта", t)
		}
		if !active(t.Status) {
			continue
		}
		if !automatic {
			if in.TasksDecision != want {
				return &APIError{Status: 422, Code: "tasks_decision_required", Message: "Подтвердите обработку открытых задач: tasks_decision=" + want, Current: tasks}
			}
			if e = checkRevision(in.RelatedRevisions[t.ID], t.RecordMeta); e != nil {
				return e
			}
		}
	}
	if in.TasksDecision != "" && in.TasksDecision != want {
		return invalid("Некорректное решение по задачам")
	}
	for _, t := range tasks {
		if !active(t.Status) {
			continue
		}
		input := TaskInput{Mutation: Mutation{ExpectedRevision: t.Revision, RelatedRevisions: in.RelatedRevisions}, SessionsDecision: in.SessionsDecision, SessionRevisions: in.SessionRevisions}
		if action == "complete" {
			input.CompletedDate = in.CompletedDate
			input.ChildrenDecision = "keep_open"
		}
		var result TaskDTO
		options := taskChangeOptions{ProjectSkip: true}
		if automatic {
			options.PauseID = &in.Reason
		}
		if e = s.changeTaskTx(ctx, tx, t.ID, action, input, op, now, s.now(), &result, options); e != nil {
			return e
		}
		if action != "complete" {
			_, e = tx.Exec(`INSERT INTO project_closed_tasks(occurrence_id,task_id,closed_revision,previous_status) VALUES(?,?,?,?) ON CONFLICT(occurrence_id,task_id) DO UPDATE SET closed_revision=excluded.closed_revision,previous_status=excluded.previous_status,restored_at=NULL`, occ, t.ID, result.Revision, t.Status)
			if e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Service) restoreProjectTasks(ctx context.Context, tx *sql.Tx, occ string, p ProjectDTO, in OccurrenceCommand, op, now string) error {
	rows, e := tx.Query("SELECT task_id,closed_revision,previous_status FROM project_closed_tasks WHERE occurrence_id=? AND restored_at IS NULL ORDER BY task_id", occ)
	if e != nil {
		return e
	}
	type saved struct {
		id       string
		revision int64
		status   string
	}
	items := []saved{}
	for rows.Next() {
		var x saved
		if e = rows.Scan(&x.id, &x.revision, &x.status); e != nil {
			rows.Close()
			return e
		}
		items = append(items, x)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if len(items) > 0 && in.TasksDecision != "restore_closed" {
		return invalid("Выберите tasks_decision=restore_closed для восстановления закрытых этой операцией задач")
	}
	for _, x := range items {
		t, e := getTask(tx, x.id)
		if e != nil {
			return e
		}
		if t.ProjectID == nil || *t.ProjectID != p.ID || t.Revision != x.revision || (t.Status != "skipped" && t.Status != "cancelled") {
			return conflict("project_restore_conflict", "Закрытая задача была изменена; автоматическое восстановление небезопасно", t)
		}
		if e = checkRevision(in.RelatedRevisions[t.ID], t.RecordMeta); e != nil {
			return e
		}
		var changed TaskDTO
		if e = s.changeTaskTx(ctx, tx, t.ID, "restore", TaskInput{Mutation: Mutation{ExpectedRevision: t.Revision}}, op, now, s.now(), &changed, taskChangeOptions{AllowRestore: true}); e != nil {
			return e
		}
		if x.status == "in_progress" {
			status := x.status
			if e = s.changeTaskTx(ctx, tx, t.ID, "", TaskInput{Mutation: Mutation{ExpectedRevision: changed.Revision}, Status: Field[string]{Set: true, Value: &status}}, op, now, s.now(), &changed, taskChangeOptions{}); e != nil {
				return e
			}
		}
		in.RelatedRevisions[t.ID] = changed.Revision
		if _, e = tx.Exec("UPDATE project_closed_tasks SET restored_at=? WHERE occurrence_id=? AND task_id=?", now, occ, t.ID); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) moveProjectTasks(ctx context.Context, tx *sql.Tx, p ProjectDTO, date string, in OccurrenceCommand, op, now string) error {
	if in.TasksDateDecision == "keep" {
		return nil
	}
	if p.StartDate == nil {
		return invalid("Нельзя сдвинуть задачи проекта без прежней даты начала")
	}
	old, e := parseDate(Date(*p.StartDate))
	if e != nil {
		return e
	}
	next, e := parseDate(Date(date))
	if e != nil {
		return e
	}
	ordinal := func(year, day int) int { y := year - 1; return 365*y + y/4 - y/100 + y/400 + day }
	delta := ordinal(next.Year(), next.YearDay()) - ordinal(old.Year(), old.YearDay())
	tasks, e := listTasks(tx, "WHERE project_id=?", p.ID)
	if e != nil {
		return e
	}
	for _, t := range tasks {
		if !active(t.Status) || (t.PlannedDate == nil && t.DueDate == nil) {
			continue
		}
		if e = checkRevision(in.RelatedRevisions[t.ID], t.RecordMeta); e != nil {
			return e
		}
		old := t
		if t.PlannedDate != nil {
			t.PlannedDate, e = offsetDate(*t.PlannedDate, &delta)
			if e != nil {
				return e
			}
		}
		if t.DueDate != nil {
			t.DueDate, e = offsetDate(*t.DueDate, &delta)
			if e != nil {
				return e
			}
		}
		t.UpdatedAt = now
		if e = saveTask(tx, t); e != nil {
			return e
		}
		t.Revision++
		if e = event(tx, "task", t.ID, "project_rescheduled", op, now, old, t); e != nil {
			return e
		}
		if e = s.advanceTaskSeries(ctx, tx, old, t, op, now); e != nil {
			return e
		}
	}
	return nil
}
func trackProjectException(tx *sql.Tx, before, after ProjectDTO, op, now string) error {
	if before.OccurrenceID == nil {
		return nil
	}
	if !reflect.DeepEqual(before.StartDate, after.StartDate) {
		if _, e := tx.Exec("UPDATE occurrences SET manual_date=?,manual_override=1,revision=revision+1,updated_at=? WHERE id=?", after.StartDate, now, *before.OccurrenceID); e != nil {
			return e
		}
		return event(tx, "occurrence", *before.OccurrenceID, "project_rescheduled", op, now, map[string]any{"date": before.StartDate}, map[string]any{"date": after.StartDate, "tasks_date_decision": "keep"})
	}
	return nil
}

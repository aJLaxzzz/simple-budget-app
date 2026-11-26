package planner

import (
	"budget/internal/planner/recurrence"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func commandOnce(tx *sql.Tx, key, target, action, now string, input any, run func() (CommandResult, error)) (CommandResult, error) {
	var result CommandResult
	if strings.TrimSpace(key) == "" || len(key) > 128 {
		return result, invalid("Требуется request_id длиной 1–128 байт")
	}
	hash, e := digest(struct {
		Target, Action string
		Input          any
	}{target, action, input})
	if e != nil {
		return result, e
	}
	var oldHash, response string
	e = tx.QueryRow("SELECT request_hash,response_json FROM planner_commands WHERE request_id=?", key).Scan(&oldHash, &response)
	if e == nil {
		if oldHash != hash {
			return result, conflict("idempotency_conflict", "request_id уже использован другой командой", nil)
		}
		e = json.Unmarshal([]byte(response), &result)
		return result, e
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return result, e
	}
	result, e = run()
	if e != nil {
		return result, e
	}
	raw, e := json.Marshal(result)
	if e != nil {
		return result, e
	}
	_, e = tx.Exec("INSERT INTO planner_commands(request_id,target,action,request_hash,response_json,created_at) VALUES(?,?,?,?,?,?)", key, target, action, hash, string(raw), now)
	return result, e
}
func (s *Service) ApplySeriesCommand(ctx context.Context, id string, in SeriesCommand) (CommandResult, error) {
	var result CommandResult
	now := stamp(s.now())
	op, e := operation()
	if e != nil {
		return result, e
	}
	e = s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var e error
		result, e = commandOnce(tx, in.RequestID, "series/"+id, in.Action, now, in, func() (CommandResult, error) {
			v, e := getSeries(tx, id)
			if e != nil {
				return CommandResult{}, e
			}
			if v.SourceType == "project" {
				return s.applyProjectSeriesTx(ctx, tx, v, in, op, now)
			}
			p, e := s.previewSeriesTx(ctx, tx, v, in)
			if e != nil {
				return CommandResult{}, e
			}
			if in.PreviewToken == "" || in.PreviewToken != p.PreviewToken {
				return CommandResult{}, conflict("preview_changed", "Получите и просмотрите актуальный предпросмотр", p)
			}
			if len(p.CalendarErrors) > 0 {
				return CommandResult{}, p.CalendarErrors[0]
			}
			before := v
			switch in.Action {
			case "change_rule":
				version, e := insertVersion(tx, v, *p.ProposedRule, now)
				if e != nil {
					return CommandResult{}, e
				}
				for _, row := range p.Rows {
					if row.OccurrenceID == "" || row.Candidate == nil {
						continue
					}
					task, e := getTask(tx, row.TaskID)
					if e != nil {
						return CommandResult{}, e
					}
					old := task
					task.PlannedDate = row.AfterDate
					task.UpdatedAt = now
					if e = saveTask(tx, task); e != nil {
						return CommandResult{}, e
					}
					task.Revision++
					task.RuleVersionID = &version
					payload, e := json.Marshal(map[string]any{"candidate": row.Candidate, "calendars": row.Calendars})
					if e != nil {
						return CommandResult{}, e
					}
					_, e = tx.Exec(`INSERT INTO occurrence_schedule(occurrence_id,series_id,rule_version_id,base_date,adjusted_date,calculation_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(occurrence_id) DO UPDATE SET rule_version_id=excluded.rule_version_id,base_date=excluded.base_date,adjusted_date=excluded.adjusted_date,calculation_json=excluded.calculation_json,revision=occurrence_schedule.revision+1,updated_at=excluded.updated_at`, row.OccurrenceID, id, version, row.Candidate.BaseDate, row.Candidate.AdjustedDate, string(payload), now, now)
					if e != nil {
						return CommandResult{}, e
					}
					if _, e = tx.Exec(`UPDATE occurrences SET revision=revision+1,updated_at=? WHERE id=?`, now, row.OccurrenceID); e != nil {
						return CommandResult{}, e
					}
					if e = event(tx, "task", task.ID, "recurrence_rule_changed", op, now, old, task); e != nil {
						return CommandResult{}, e
					}
					if e = event(tx, "occurrence", row.OccurrenceID, "rule_changed", op, now, map[string]any{"rule_version_id": *old.RuleVersionID, "date": old.PlannedDate}, map[string]any{"rule_version_id": version, "base_date": row.Candidate.BaseDate, "date": task.PlannedDate}); e != nil {
						return CommandResult{}, e
					}
					if row.Disposition == "change_and_skip" {
						pause := pauseOn(p.Pauses, *task.PlannedDate)
						if e = s.skipForPause(ctx, tx, task, *pause, op, now); e != nil {
							return CommandResult{}, e
						}
					}
				}
			case "pause":
				pauseID, e := operation()
				if e != nil {
					return CommandResult{}, e
				}
				pause := PauseDTO{RecordMeta: RecordMeta{ID: pauseID, Revision: 1, CreatedAt: now, UpdatedAt: now}, SeriesID: id, StartDate: in.StartDate, EndDate: &in.EndDate, Reason: in.Reason}
				if _, e = tx.Exec(`INSERT INTO series_pauses(id,series_id,start_date,end_date,reason,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, pauseID, id, in.StartDate, in.EndDate, in.Reason, now, now); e != nil {
					return CommandResult{}, e
				}
				for _, row := range p.Rows {
					if row.Disposition == "skip_pause" {
						task, e := getTask(tx, row.TaskID)
						if e != nil {
							return CommandResult{}, e
						}
						if e = s.skipForPause(ctx, tx, task, pause, op, now); e != nil {
							return CommandResult{}, e
						}
					}
				}
				if e = event(tx, "series", id, "pause_created", op, now, nil, pause); e != nil {
					return CommandResult{}, e
				}
			case "resume":
				if _, e = tx.Exec("UPDATE series_pauses SET resumed_on=?,revision=revision+1,updated_at=? WHERE id=? AND series_id=?", s.today(), now, in.PauseID, id); e != nil {
					return CommandResult{}, e
				}
				for _, row := range p.Rows {
					if row.Disposition == "restore_after_resume" {
						task, e := getTask(tx, row.TaskID)
						if e != nil {
							return CommandResult{}, e
						}
						var restored TaskDTO
						if e = s.changeTaskTx(ctx, tx, task.ID, "restore", TaskInput{Mutation: Mutation{ExpectedRevision: task.Revision}}, op, now, s.now(), &restored, taskChangeOptions{AllowRestore: true}); e != nil {
							return CommandResult{}, e
						}
					}
				}
			case "archive":
			case "reanchor":
			}
			// Hooks may have changed the runtime while closing affected tasks.
			v, e = getSeries(tx, id)
			if e != nil {
				return CommandResult{}, e
			}
			switch in.Action {
			case "archive":
				v.State = "archived"
			case "reanchor":
				v.State = "active"
				v.AnchorDate = &in.AnchorDate
				v.AnchorCycle = v.NextCycle - 1
				if e = tx.QueryRow("SELECT COALESCE(min(o.cycle_index)-1,?) FROM occurrences o JOIN tasks t ON t.occurrence_id=o.id WHERE o.series_id=? AND o.dynamic=1 AND t.status IN ('planned','in_progress')", v.AnchorCycle, v.ID).Scan(&v.AnchorCycle); e != nil {
					return CommandResult{}, e
				}
				v.AnchorKind = recurrence.ExplicitAnchor
				v.WaitingOccurrenceID = nil
			}
			if e = s.refreshPauseState(tx, &v); e != nil {
				return CommandResult{}, e
			}
			if e = saveSeriesRuntime(tx, v, now); e != nil {
				return CommandResult{}, e
			}
			v.Revision++
			if e = event(tx, "series", id, in.Action, op, now, before, map[string]any{"series": v, "command": in, "preview": p}); e != nil {
				return CommandResult{}, e
			}
			if v.AnchorMode == "completion" && in.Action == "reanchor" {
				_, _, e = s.syncCompletionChain(ctx, tx, &v, op, now, true, "keep_manual")
			} else {
				_, _, e = s.materializeSeriesTx(ctx, tx, &v, op, now)
			}
			if e != nil {
				return CommandResult{}, e
			}
			v, e = getSeries(tx, id)
			if e != nil {
				return CommandResult{}, e
			}
			if e = decorateSeries(tx, &v); e != nil {
				return CommandResult{}, e
			}
			return CommandResult{Series: &v}, nil
		})
		return e
	})
	return result, recurrenceAPIError(e)
}
func (s *Service) refreshPauseState(tx *sql.Tx, v *SeriesDTO) error {
	if v.State == "archived" || v.State == "disabled" || v.State == "needs_anchor" {
		return nil
	}
	pauses, e := loadPauses(tx, v.ID)
	if e != nil {
		return e
	}
	v.State = "active"
	if pauseOn(pauses, s.today()) != nil {
		v.State = "paused"
	}
	return nil
}
func (s *Service) skipForPause(ctx context.Context, tx *sql.Tx, task TaskDTO, p PauseDTO, op, now string) error {
	reason := p.Reason
	if reason == "" {
		reason = "Пропуск во время паузы серии"
	}
	var result TaskDTO
	return s.changeTaskTx(ctx, tx, task.ID, "skip", TaskInput{Mutation: Mutation{ExpectedRevision: task.Revision}}, op, now, s.now(), &result, taskChangeOptions{Reason: reason, PauseID: &p.ID})
}
func branchConflict(tx *sql.Tx, v SeriesDTO, o TaskOccurrenceDTO) error {
	affected, more, e := seriesOccurrences(tx, v.ID, 100, 0)
	if e != nil {
		return e
	}
	return conflict("completion_branch_conflict", "В серии уже есть продолжение или новая опора. Старый цикл нельзя сделать второй активной веткой; для исторического завершения явно выберите historical_only", map[string]any{"series": v, "occurrence": o.ID, "affected_occurrences": affected, "has_more": more})
}
func (s *Service) OccurrenceCommand(ctx context.Context, id, action string, in OccurrenceCommand) (CommandResult, error) {
	var result CommandResult
	now := stamp(s.now())
	op, e := operation()
	if e != nil {
		return result, e
	}
	e = s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var e error
		result, e = commandOnce(tx, in.RequestID, "occurrence/"+id, action, now, in, func() (CommandResult, error) {
			o, e := getOccurrence(tx, id)
			if e != nil {
				return CommandResult{}, e
			}
			v, e := getSeries(tx, o.SeriesID)
			if e != nil {
				return CommandResult{}, e
			}
			var detached int
			if e = tx.QueryRow("SELECT count(*) FROM detached_occurrences WHERE occurrence_id=?", id).Scan(&detached); e != nil {
				return CommandResult{}, e
			}
			if detached > 0 {
				return CommandResult{}, conflict("detached_occurrence", "Это выполнение стало самостоятельным. Откройте обычную задачу или проект.", nil)
			}
			if in.ScheduledTime.Set && (action != "reschedule" || v.SourceType == "project") {
				return CommandResult{}, invalid("scheduled_time допустимо только при переносе выполнения задачи")
			}
			if in.InstanceOnly && (action != "reschedule" || v.SourceType != "task" || !active(o.Task.Status)) {
				return CommandResult{}, invalid("instance_only допустимо только для переноса открытого выполнения задачи")
			}
			if v.SourceType == "project" {
				return s.projectOccurrenceCommandTx(ctx, tx, v, o, action, in, op, now, false)
			}
			if in.ExpectedProjectRevision != 0 || in.TasksDecision != "" || in.TasksDateDecision != "" {
				return CommandResult{}, invalid("Поля проекта неприменимы к циклу задачи")
			}
			for _, r := range []struct {
				expected int64
				actual   RecordMeta
			}{{in.ExpectedRevision, o.RecordMeta}, {in.ExpectedTaskRevision, o.Task.RecordMeta}, {in.ExpectedSeriesRevision, v.RecordMeta}} {
				if e = checkRevision(r.expected, r.actual); e != nil {
					return CommandResult{}, e
				}
			}
			if action == "undo-complete" {
				return s.undoCompletionTx(ctx, tx, o, v, in, op, now)
			}
			beforeCompletion := v
			canUndo := action == "complete" && v.AnchorMode == "completion" && v.WaitingOccurrenceID != nil && *v.WaitingOccurrenceID == o.ID && in.ChildrenDecision == "" && in.SessionsDecision == "" && in.DynamicDecision == ""
			if len(in.Reason) > 5000 {
				return CommandResult{}, invalid("Причина слишком длинная")
			}
			if in.PlannedDate != nil && action != "reschedule" {
				return CommandResult{}, invalid("planned_date допустима только при переносе")
			}
			if in.CompletedDate != nil {
				if e := managementDate(*in.CompletedDate); e != nil {
					return CommandResult{}, e
				}
			}
			if in.CompletedDate != nil && action != "complete" {
				return CommandResult{}, invalid("completed_date допустима только при завершении")
			}
			if in.BranchDecision != "" && in.BranchDecision != "historical_only" && in.BranchDecision != "replace_anchor" {
				return CommandResult{}, invalid("Неизвестное решение о продолжении серии")
			}
			if in.DynamicDecision != "" && in.DynamicDecision != "keep_manual" {
				return CommandResult{}, invalid("Неизвестное решение по динамическим датам")
			}
			options := taskChangeOptions{InstanceOnly: in.InstanceOnly, AllowRestore: true, Reason: in.Reason, DynamicDecision: in.DynamicDecision}
			if in.BranchDecision == "historical_only" {
				if action != "complete" || (active(o.Task.Status) && !o.Task.Dynamic) || v.AnchorMode != "completion" {
					return CommandResult{}, invalid("historical_only допустимо для завершения закрытого цикла от выполнения")
				}
				options.HistoricalOnly = true
			}
			restores := action == "restore" || action == "reschedule" && !active(o.Task.Status) || action == "complete" && !active(o.Task.Status)
			// A dependent future row can rejoin the same chain without becoming
			// a new anchor. A historical row behind the current cycle cannot.
			futureRestore := false
			if o.Task.Dynamic && restores {
				boundary := v.AnchorCycle
				if v.WaitingOccurrenceID != nil {
					current, err := getOccurrence(tx, *v.WaitingOccurrenceID)
					if err != nil {
						return CommandResult{}, err
					}
					boundary = current.CycleIndex
				}
				futureRestore = o.CycleIndex > boundary
			}
			if v.AnchorMode == "completion" && restores && !options.HistoricalOnly && !futureRestore {
				following := v.WaitingOccurrenceID != nil && *v.WaitingOccurrenceID != o.ID
				replaced := v.AnchorKind == recurrence.ExplicitAnchor && v.AnchorDate != nil && v.WaitingOccurrenceID == nil
				if following || replaced && in.BranchDecision != "replace_anchor" {
					return CommandResult{}, branchConflict(tx, v, o)
				}
			}
			if in.BranchDecision == "replace_anchor" && (v.AnchorMode != "completion" || v.AnchorKind != recurrence.ExplicitAnchor || v.WaitingOccurrenceID != nil || !restores) {
				return CommandResult{}, invalid("Заменить можно только новую опору без созданного продолжения")
			}
			switch action {
			case "review":
				if o.Task.Status != "skipped" {
					return CommandResult{}, invalid("Рассмотреть можно пропущенное выполнение")
				}
				if in.SessionsDecision != "" || len(in.SessionRevisions) > 0 {
					return CommandResult{}, invalid("Рассмотрение не изменяет сессии")
				}
				if e = resolveIncidents(tx, id, "acknowledged", op, now); e != nil {
					return CommandResult{}, e
				}
				if _, e = tx.Exec("UPDATE occurrences SET revision=revision+1,updated_at=? WHERE id=?", now, id); e != nil {
					return CommandResult{}, e
				}
			case "reschedule", "restore", "complete", "cancel", "skip":
				if action == "skip" && !active(o.Task.Status) {
					return CommandResult{}, invalid("Пропустить можно только активное выполнение")
				}
				if action == "cancel" && o.Task.Status == "completed" {
					return CommandResult{}, invalid("Сначала явно восстановите завершённый цикл")
				}
				if action == "reschedule" {
					if in.PlannedDate == nil {
						return CommandResult{}, invalid("Укажите planned_date")
					}
					if e = managementDate(*in.PlannedDate); e != nil {
						return CommandResult{}, e
					}
				}
				mutation := TaskInput{Mutation: Mutation{ExpectedRevision: o.Task.Revision, RelatedRevisions: in.RelatedRevisions}, CompletedDate: in.CompletedDate, ChildrenDecision: in.ChildrenDecision, SessionsDecision: in.SessionsDecision, SessionRevisions: in.SessionRevisions}
				taskAction := action
				if action == "reschedule" {
					mutation.PlannedDate = Field[string]{Set: true, Value: in.PlannedDate}
					mutation.ScheduledTime = in.ScheduledTime
					if !active(o.Task.Status) {
						taskAction = "restore"
					}
				}
				var changed TaskDTO
				if e = s.changeTaskTx(ctx, tx, o.Task.ID, taskAction, mutation, op, now, s.now(), &changed, options); e != nil {
					return CommandResult{}, e
				}
				if action == "restore" || action == "reschedule" {
					if _, e = tx.Exec("UPDATE occurrences SET manual_override=1,revision=revision+1,updated_at=? WHERE id=?", now, id); e != nil {
						return CommandResult{}, e
					}
				}
				if action != "skip" {
					resolution := map[string]string{"restore": "restored", "reschedule": "rescheduled", "complete": "completed", "cancel": "cancelled"}[action]
					if e = resolveIncidents(tx, id, resolution, op, now); e != nil {
						return CommandResult{}, e
					}
				}
			default:
				return CommandResult{}, invalid("Неизвестное действие экземпляра")
			}
			if e = event(tx, "series", v.ID, "occurrence_"+action, op, now, nil, map[string]any{"occurrence_id": id, "request": in}); e != nil {
				return CommandResult{}, e
			}
			freshSeries, err := getSeries(tx, v.ID)
			if err != nil {
				return CommandResult{}, err
			}
			if _, err = s.ensureUpcomingTx(ctx, tx, &freshSeries, op, now); err != nil {
				return CommandResult{}, err
			}
			// Record-level revisions include incident-only decisions as well.
			if _, e = tx.Exec("UPDATE series SET revision=revision+1,updated_at=? WHERE id=?", now, v.ID); e != nil {
				return CommandResult{}, e
			}
			o, e = getOccurrence(tx, id)
			if e != nil {
				return CommandResult{}, e
			}
			out := CommandResult{Occurrence: &o}
			if canUndo {
				signature, err := completionSignature(tx, v.ID)
				if err != nil {
					return CommandResult{}, err
				}
				out.Undo = &CompletionUndo{Signature: signature, Before: beforeCompletion}
			}
			return out, nil
		})
		return e
	})
	return result, recurrenceAPIError(e)
}
func (s *Service) Incidents(ctx context.Context, series, kind, disposition string, limit, offset int) ([]IncidentDTO, error) {
	if kind != "" && kind != "skipped" && kind != "cancelled" || disposition != "" && disposition != "needs_decision" && disposition != "processed" || limit < 1 || limit > 100 || offset < 0 {
		return nil, invalid("Некорректный фильтр или пагинация пропусков")
	}
	var items []IncidentDTO
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var e error
		items, e = incidentList(tx, series, kind, disposition, limit, offset)
		return e
	})
	return items, e
}

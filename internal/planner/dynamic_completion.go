package planner

import (
	"budget/internal/planner/recurrence"
	"context"
	"database/sql"
	"encoding/json"
)

// One current occurrence owns the chain. Later rows are real, dependent tasks;
// their immutable original provenance is retained in occurrences, while recalcs
// use the existing versioned occurrence_schedule projection.
func (s *Service) syncCompletionChain(ctx context.Context, tx *sql.Tx, v *SeriesDTO, op, now string, recalc bool, decision string, expand ...bool) (int, bool, error) {
	oldState := v.State
	if e := s.refreshPauseState(tx, v); e != nil {
		return 0, false, e
	}
	if oldState != v.State {
		if e := saveSeriesRuntime(tx, *v, now); e != nil {
			return 0, false, e
		}
		v.Revision++
	}
	if (v.State != "active" && v.State != "paused") || v.SourceType != "task" {
		return 0, false, nil
	}
	if v.WaitingOccurrenceID == nil && v.AnchorDate == nil {
		return 0, false, nil
	}
	tasks, e := listTasks(tx, "WHERE status IN ('planned','in_progress') AND occurrence_id IN (SELECT id FROM occurrences WHERE series_id=?)", v.ID)
	if e != nil {
		return 0, false, e
	}
	sortChain(tasks)
	var anchor string
	start := 0
	if v.WaitingOccurrenceID != nil {
		current, e := getOccurrence(tx, *v.WaitingOccurrenceID)
		if e != nil {
			return 0, false, e
		}
		if current.Task.PlannedDate == nil {
			return 0, false, invalid("Текущему выполнению нужна плановая дата")
		}
		anchor = *current.Task.PlannedDate
		index := current.CycleIndex
		for start < len(tasks) && *tasks[start].CycleIndex <= index {
			start++
		}
	} else {
		anchor = *v.AnchorDate
		recalc = true
	}
	created := 0
	for i := start; i < len(tasks); i++ {
		t := tasks[i]
		selected, e := versionForCycle(tx, *v, *t.CycleIndex)
		if e != nil {
			return 0, false, e
		}
		selected.AnchorDate = &anchor
		selected.AnchorCycle = *t.CycleIndex - 1
		selected.AnchorKind = recurrence.CompletedAnchor
		c, calendars, e := calculateStoredCycle(ctx, tx, selected, *t.CycleIndex, nil)
		if e != nil {
			return 0, false, e
		}
		desired := string(c.AdjustedDate)
		if recalc && v.EndDate != nil && desired > *v.EndDate {
			return 0, false, conflict("dynamic_end_conflict", "Пересчёт будущего выполнения выходит за дату окончания повторения. Сначала отмените или удалите зависимое выполнение за этой границей.", map[string]any{"task": t, "proposed_date": desired, "end_date": v.EndDate})
		}
		if recalc && (t.PlannedDate == nil || *t.PlannedDate != desired) {
			o, e := getOccurrence(tx, *t.OccurrenceID)
			if e != nil {
				return 0, false, e
			}
			var work int
			if e = tx.QueryRow("SELECT count(*) FROM sessions WHERE task_id=? AND status IN ('completed','in_progress')", t.ID).Scan(&work); e != nil {
				return 0, false, e
			}
			if o.ManualOverride || t.Status == "in_progress" || work > 0 {
				if decision != "keep_manual" {
					return 0, false, conflict("dynamic_manual_conflict", "У будущего выполнения есть ручной перенос или начатая работа. Подтвердите сохранение его даты; остальные даты будут рассчитаны последовательно.", map[string]any{"task": t, "proposed_date": desired})
				}
			} else {
				raw, _ := json.Marshal(map[string]any{"candidate": c, "calendars": calendars, "origin": "dynamic_recalculation"})
				_, e = tx.Exec(`INSERT INTO occurrence_schedule(occurrence_id,series_id,rule_version_id,base_date,adjusted_date,calculation_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(occurrence_id) DO UPDATE SET rule_version_id=excluded.rule_version_id,base_date=excluded.base_date,adjusted_date=excluded.adjusted_date,calculation_json=excluded.calculation_json,revision=occurrence_schedule.revision+1,updated_at=excluded.updated_at`, *t.OccurrenceID, v.ID, selected.RuleVersionID, c.BaseDate, c.AdjustedDate, string(raw), now, now)
				if e != nil {
					return 0, false, e
				}
				before := t
				t.PlannedDate = &desired
				t.UpdatedAt = now
				if e = saveTask(tx, t); e != nil {
					return 0, false, e
				}
				t.Revision++
				if _, e = tx.Exec("UPDATE occurrences SET revision=revision+1,updated_at=? WHERE id=?", now, *t.OccurrenceID); e != nil {
					return 0, false, e
				}
				if e = eventAs(tx, "task", t.ID, "dynamic_recalculated", "system", op, now, before, t); e != nil {
					return 0, false, e
				}
			}
		}
		if v.WaitingOccurrenceID == nil {
			if _, e = tx.Exec("UPDATE occurrences SET dynamic=0,pending_completion=1,revision=revision+1,updated_at=? WHERE id=?", now, *t.OccurrenceID); e != nil {
				return 0, false, e
			}
			beforeRole := t
			t.Dynamic = false
			t.UpdatedAt = now
			if _, e = tx.Exec("UPDATE tasks SET revision=revision+1,updated_at=? WHERE id=?", now, t.ID); e != nil {
				return 0, false, e
			}
			t.Revision++
			if e = eventAs(tx, "task", t.ID, "became_current", "system", op, now, beforeRole, t); e != nil {
				return 0, false, e
			}
			v.WaitingOccurrenceID = t.OccurrenceID
			if e = saveSeriesRuntime(tx, *v, now); e != nil {
				return 0, false, e
			}
			v.Revision++
			if e = eventAs(tx, "occurrence", *t.OccurrenceID, "became_current", "system", op, now, nil, map[string]any{"dynamic": false}); e != nil {
				return 0, false, e
			}
			pauses, e := loadPauses(tx, v.ID)
			if e != nil {
				return 0, false, e
			}
			if t.PlannedDate != nil {
				if pause := pauseOn(pauses, *t.PlannedDate); pause != nil {
					if e = s.skipForPause(ctx, tx, t, *pause, op, now); e != nil {
						return 0, false, e
					}
					fresh, e := getSeries(tx, v.ID)
					if e != nil {
						return 0, false, e
					}
					*v = fresh
					return created, false, nil
				}
			}
		}
		if t.PlannedDate != nil {
			anchor = *t.PlannedDate
		}
	}
	// Three tasks by default; a calendar request may safely extend the same chain.
	extend := v.HorizonDate > s.horizon() || (len(expand) > 0 && expand[0])
	count := len(tasks)
	for attempt := 0; attempt < materializeCyclesPerBatch; attempt++ {
		if v.State != "active" {
			break
		}
		if e = ctx.Err(); e != nil {
			return 0, false, e
		}
		if count >= 3 && !extend {
			break
		}
		var removed int
		if e = tx.QueryRow("SELECT count(*) FROM removed_cycles WHERE series_id=? AND cycle_index=?", v.ID, v.NextCycle).Scan(&removed); e != nil {
			return 0, false, e
		}
		if removed > 0 {
			v.NextCycle++
			if e = saveSeriesRuntime(tx, *v, now); e != nil {
				return 0, false, e
			}
			v.Revision++
			continue
		}
		selected, e := versionForCycle(tx, *v, v.NextCycle)
		if e != nil {
			return 0, false, e
		}
		selected.AnchorDate = &anchor
		selected.AnchorCycle = v.NextCycle - 1
		selected.AnchorKind = recurrence.CompletedAnchor
		c, calendars, e := calculateStoredCycle(ctx, tx, selected, v.NextCycle, nil)
		if e != nil {
			return 0, false, e
		}
		date := string(c.AdjustedDate)
		if v.EndDate != nil && date > *v.EndDate {
			break
		}
		if count >= 3 && date > v.HorizonDate {
			break
		}
		pauses, e := loadPauses(tx, v.ID)
		if e != nil {
			return 0, false, e
		}
		// Do not invent a continuation through a future pause. The current cycle
		// will require an explicit new anchor when that pause becomes applicable.
		if pauseOn(pauses, date) != nil {
			break
		}
		selected.WaitingOccurrenceID = v.WaitingOccurrenceID
		task, e := s.insertCycle(tx, &selected, c, calendars, nil, op, now)
		if e != nil {
			return 0, false, e
		}
		if v.WaitingOccurrenceID == nil {
			v.WaitingOccurrenceID = task.OccurrenceID
		}
		anchor = date
		v.NextCycle++
		created++
		count++
		if e = saveSeriesRuntime(tx, *v, now); e != nil {
			return 0, false, e
		}
		v.Revision++
	}
	return created, created >= materializeCyclesPerBatch && extend, nil
}

func sortChain(tasks []TaskDTO) { // Small bounded chain; shared task ordering remains unchanged.
	for i := 1; i < len(tasks); i++ {
		for j := i; j > 0 && *tasks[j].CycleIndex < *tasks[j-1].CycleIndex; j-- {
			tasks[j], tasks[j-1] = tasks[j-1], tasks[j]
		}
	}
}

func (s *Service) advanceCompletionChain(ctx context.Context, tx *sql.Tx, v *SeriesDTO, before, task TaskDTO, op, now string, options taskChangeOptions) error {
	if options.HistoricalOnly {
		_, _, e := s.syncCompletionChain(ctx, tx, v, op, now, false, options.DynamicDecision)
		return e
	}
	current := v.WaitingOccurrenceID != nil && *v.WaitingOccurrenceID == *task.OccurrenceID
	if task.Dynamic {
		if task.Status == "completed" && !options.HistoricalOnly {
			return conflict("dynamic_early_completion", "Это предварительно запланированное выполнение. Явно подтвердите завершение вне очереди: текущая опора останется прежней.", task)
		}
		_, _, e := s.syncCompletionChain(ctx, tx, v, op, now, false, options.DynamicDecision)
		return e
	}
	if active(task.Status) && active(before.Status) {
		return nil
	}
	old := *v
	if active(task.Status) {
		if v.WaitingOccurrenceID != nil && !current {
			return conflict("completion_branch_conflict", "У повторения уже есть текущее выполнение", v)
		}
		v.WaitingOccurrenceID = task.OccurrenceID
		v.State = "active"
	} else {
		if !current {
			return conflict("completion_branch_conflict", "Это выполнение больше не определяет продолжение", v)
		}
		v.WaitingOccurrenceID = nil
		if task.Status == "completed" {
			v.AnchorDate = task.CompletedDate
			v.AnchorCycle = *task.CycleIndex
			v.AnchorKind = recurrence.CompletedAnchor
		} else {
			v.AnchorDate = nil
			if v.State != "archived" {
				v.State = "needs_anchor"
			}
		}
	}
	if e := saveSeriesRuntime(tx, *v, now); e != nil {
		return e
	}
	v.Revision++
	if e := event(tx, "series", v.ID, "anchor_updated", op, now, old, *v); e != nil {
		return e
	}
	_, _, e := s.syncCompletionChain(ctx, tx, v, op, now, true, options.DynamicDecision)
	return e
}

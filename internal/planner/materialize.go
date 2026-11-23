package planner

import (
	"budget/internal/planner/recurrence"
	"context"
	"database/sql"
	"errors"
	"log"
	"reflect"
	"sort"
	"time"
)

func sortCalendars(items []recurrence.CalendarInfo) {
	sort.Slice(items, func(i, j int) bool { return items[i].Year < items[j].Year })
}
func (s *Service) materializeSeriesTx(ctx context.Context, tx *sql.Tx, v *SeriesDTO, op, now string) (int, bool, error) {
	oldState := v.State
	if e := s.refreshPauseState(tx, v); e != nil {
		return 0, false, e
	}
	if oldState != v.State {
		if e := saveSeriesRuntime(tx, *v, now); e != nil {
			return 0, false, e
		}
		v.Revision++
		if e := eventAs(tx, "series", v.ID, "pause_state_changed", "system", op, now, map[string]any{"state": oldState}, map[string]any{"state": v.State}); e != nil {
			return 0, false, e
		}
	}
	if (v.State != "active" && v.State != "paused") || v.WaitingOccurrenceID != nil {
		return 0, false, nil
	}
	if v.AnchorMode == "completion" && v.AnchorDate == nil {
		return 0, false, nil
	}
	pauses, e := loadPauses(tx, v.ID)
	if e != nil {
		return 0, false, e
	}
	created := 0
	calendar := &recurrenceCalendarSnapshot{tx: tx, years: map[int]productionYearSnapshot{}}
	cycleLimit := materializeCyclesPerBatch
	if v.SourceType == "project" {
		cycleLimit = 5
	} // At most 2500 template tasks per transaction.
	for attempt := 0; attempt < cycleLimit; attempt++ {
		if e := ctx.Err(); e != nil {
			return 0, false, e
		}
		var exists int
		if e := tx.QueryRow("SELECT count(*) FROM occurrences WHERE series_id=? AND cycle_index=?", v.ID, v.NextCycle).Scan(&exists); e != nil {
			return 0, false, e
		}
		if exists > 0 {
			return 0, false, invalid("Курсор серии не согласован с существующим циклом; данные не изменены")
		}
		selected, e := versionForCycle(tx, *v, v.NextCycle)
		if e != nil {
			return 0, false, e
		}
		rule, anchor := selected.Rule.Rule, selected.Rule.Anchor
		if rule.Mode == recurrence.Completion {
			anchor = recurrence.Anchor{Date: recurrence.Date(*v.AnchorDate), CycleIndex: v.AnchorCycle, Kind: v.AnchorKind}
		}
		base, e := recurrence.BaseDate(rule, anchor, v.NextCycle)
		if e != nil {
			return 0, false, recurrenceAPIError(e)
		}
		horizon := v.HorizonDate
		if v.EndDate != nil && *v.EndDate < horizon {
			horizon = *v.EndDate
		}
		end, _ := parseDate(Date(horizon))
		if rule.Adjustment == recurrence.Previous || rule.Adjustment == recurrence.Nearest {
			end = end.AddDate(0, 0, recurrence.MaxAdjustmentDays)
		}
		if string(base) > end.Format("2006-01-02") {
			return created, false, nil
		}
		c, calendars, e := calculateStoredCycle(ctx, tx, selected, v.NextCycle, calendar)
		if e != nil {
			return 0, false, e
		}
		// Allowed-day adjustment is monotonic for a fixed rule/calendar. Keep the
		// first out-of-horizon cycle pending; later cycles cannot precede it.
		if string(c.AdjustedDate) > horizon {
			return created, false, nil
		}
		var task TaskDTO
		if selected.SourceType == "project" {
			_, e = s.insertProjectCycle(ctx, tx, &selected, c, calendars, pauseOn(pauses, string(c.AdjustedDate)), op, now)
		} else {
			task, e = s.insertCycle(tx, &selected, c, calendars, nil, op, now)
		}
		if e != nil {
			return 0, false, e
		}
		v.NextCycle++
		created++
		if rule.Mode == recurrence.Completion {
			v.WaitingOccurrenceID = task.OccurrenceID
		}
		if e = saveSeriesRuntime(tx, *v, now); e != nil {
			return 0, false, e
		}
		v.Revision++
		if pause := pauseOn(pauses, string(c.AdjustedDate)); pause != nil && selected.SourceType == "task" {
			if e = s.skipForPause(ctx, tx, task, *pause, op, now); e != nil {
				return 0, false, e
			}
			if rule.Mode == recurrence.Completion {
				fresh, e := getSeries(tx, v.ID)
				if e != nil {
					return 0, false, e
				}
				*v = fresh
				return created, false, nil
			}
		}
		if v.WaitingOccurrenceID != nil {
			return created, false, nil
		}
	}
	return created, true, nil
}
func (s *Service) Materialize(ctx context.Context, in MaterializeInput) (MaterializeResult, error) {
	result := MaterializeResult{Through: s.horizon(), Results: []SeriesMaterialization{}}
	if in.SeriesID != "" && in.AfterSeriesID != "" {
		return result, invalid("series_id и after_series_id нельзя совмещать")
	}
	if in.Through != "" {
		to, e := parseDate(Date(in.Through))
		loc, _ := time.LoadLocation("Europe/Moscow")
		if e != nil || to.Year() < 1 {
			return result, invalid("through: требуется корректная дата YYYY-MM-DD")
		}
		if to.After(s.now().In(loc).AddDate(2, 0, 0)) {
			return result, &APIError{Status: 422, Code: "materialization_range_limit", Message: "Создание повторений ограничено двумя годами от сегодня. За этой границей доступны уже созданные задачи."}
		}
		if in.Through > result.Through {
			result.Through = in.Through
		}
	}
	ids := []string{in.SeriesID}
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		if in.SeriesID != "" {
			_, e := getSeries(tx, in.SeriesID)
			return e
		}
		var e error
		ids, e = seriesIDs(tx, in.AfterSeriesID, materializeSeriesPerBatch+1)
		if len(ids) > materializeSeriesPerBatch {
			ids = ids[:materializeSeriesPerBatch]
			result.NextAfterSeriesID = ids[len(ids)-1]
		}
		return e
	})
	if e != nil {
		return result, e
	}
	for _, id := range ids {
		if e := ctx.Err(); e != nil {
			return result, e
		}
		one := SeriesMaterialization{SeriesID: id}
		op, e := operation()
		if e != nil {
			return result, e
		}
		now := stamp(s.now())
		e = s.store.Transaction(ctx, func(tx *sql.Tx) error {
			v, e := getSeries(tx, id)
			if e != nil {
				return e
			}
			if result.Through > v.HorizonDate {
				v.HorizonDate = result.Through
				if e = saveSeriesRuntime(tx, v, now); e != nil {
					return e
				}
				v.Revision++
			}
			one.Created, one.Pending, e = s.materializeSeriesTx(ctx, tx, &v, op, now)
			return e
		})
		if e != nil {
			one.Created = 0
			one.Pending = false
			var api *APIError
			if errors.As(recurrenceAPIError(e), &api) {
				one.Error = api
			} else {
				log.Printf("planner materialize series %s: %v", id, e)
				one.Error = &APIError{Status: 500, Code: "materialization_failed", Message: "Не удалось материализовать серию; текущая порция полностью отменена"}
			}
		}
		result.Pending = result.Pending || one.Pending
		result.Results = append(result.Results, one)
	}
	return result, nil
}

// RunMaterializer runs immediately and on a timer, independent of browser tabs.
// Each pass is bounded and releases the store between series. Fast follow-ups
// drain catch-up pages; failed series are retried on the normal interval.
func (s *Service) RunMaterializer(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	after := ""
	pending := false
	for {
		result, e := s.Materialize(ctx, MaterializeInput{AfterSeriesID: after})
		if ctx.Err() != nil {
			return
		}
		if e != nil {
			log.Printf("planner materializer: %v", e)
		}
		for _, v := range result.Results {
			if v.Error != nil {
				log.Printf("planner series %s: %s", v.SeriesID, v.Error.Message)
			}
		}
		pending = pending || result.Pending
		after = result.NextAfterSeriesID
		delay := interval
		if after != "" {
			delay = time.Second
		} else if pending {
			delay = time.Second
			pending = false
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Called inside ChangeTask's transaction, after its status/history was saved.
func (s *Service) advanceTaskSeries(ctx context.Context, tx *sql.Tx, before, task TaskDTO, op, now string, option ...taskChangeOptions) error {
	if task.OccurrenceID == nil {
		return nil
	}
	options := taskChangeOptions{}
	if len(option) > 0 {
		options = option[0]
	}
	if !reflect.DeepEqual(task.PlannedDate, before.PlannedDate) {
		if _, e := tx.Exec("UPDATE occurrences SET manual_date=?,manual_override=1,revision=revision+1,updated_at=? WHERE id=?", task.PlannedDate, now, *task.OccurrenceID); e != nil {
			return e
		}
		if e := event(tx, "occurrence", *task.OccurrenceID, "rescheduled", op, now, map[string]any{"date": before.PlannedDate}, map[string]any{"date": task.PlannedDate, "cycle_index": task.CycleIndex}); e != nil {
			return e
		}
	}
	if before.Status == task.Status {
		return nil
	}
	actor := "owner"
	if options.PauseID != nil {
		actor = "system"
	}
	if e := eventAs(tx, "occurrence", *task.OccurrenceID, "status_changed", actor, op, now, map[string]any{"status": before.Status, "completed_date": before.CompletedDate}, map[string]any{"status": task.Status, "completed_date": task.CompletedDate}); e != nil {
		return e
	}
	if task.Status == "skipped" || task.Status == "cancelled" {
		reason := options.Reason
		if reason == "" {
			reason = map[string]string{"skipped": "Пропущено пользователем", "cancelled": "Отменено пользователем"}[task.Status]
		}
		source := "user"
		if options.PauseID != nil {
			source = "pause"
		}
		if e := registerIncident(tx, task, task.Status, source, reason, options.PauseID, op, now); e != nil {
			return e
		}
		if task.Status == "cancelled" {
			if e := resolveIncidents(tx, *task.OccurrenceID, "cancelled", op, now); e != nil {
				return e
			}
		}
	} else {
		if _, e := tx.Exec("UPDATE occurrences SET outcome=NULL,revision=revision+1,updated_at=? WHERE id=?", now, *task.OccurrenceID); e != nil {
			return e
		}
		if !active(before.Status) || task.Status == "completed" {
			resolution := "restored"
			if task.Status == "completed" {
				resolution = "completed"
			}
			if e := resolveIncidents(tx, *task.OccurrenceID, resolution, op, now); e != nil {
				return e
			}
		}
	}
	if task.SeriesID == nil {
		return invalid("У экземпляра отсутствует серия")
	}
	v, e := getSeries(tx, *task.SeriesID)
	if e != nil {
		return e
	}
	if v.AnchorMode != "completion" || options.HistoricalOnly {
		return nil
	}
	if active(task.Status) && active(before.Status) {
		return nil
	}
	old := v
	if active(task.Status) {
		if task.CycleIndex == nil || *task.CycleIndex+1 != v.NextCycle || v.WaitingOccurrenceID != nil && *v.WaitingOccurrenceID != *task.OccurrenceID {
			return conflict("completion_branch_conflict", "В серии уже существует продолжение", v)
		}
		v.WaitingOccurrenceID = task.OccurrenceID
		v.AnchorDate = nil
		v.AnchorKind = recurrence.CompletedAnchor
		if v.State != "archived" {
			v.State = "active"
		}
	} else {
		if task.CycleIndex == nil || *task.CycleIndex+1 != v.NextCycle || v.WaitingOccurrenceID != nil && *v.WaitingOccurrenceID != *task.OccurrenceID {
			return conflict("completion_branch_conflict", "Закрываемый цикл не является текущим", v)
		}
		v.WaitingOccurrenceID = nil
		if task.Status == "completed" {
			v.AnchorDate = task.CompletedDate
			v.AnchorCycle = *task.CycleIndex
			v.AnchorKind = recurrence.CompletedAnchor
			if v.State != "archived" {
				v.State = "active"
			}
		} else {
			if v.State != "archived" {
				v.State = "needs_anchor"
			}
			v.AnchorDate = nil
			v.AnchorKind = recurrence.CompletedAnchor
		}
	}
	if s.horizon() > v.HorizonDate {
		v.HorizonDate = s.horizon()
	}
	if e = saveSeriesRuntime(tx, v, now); e != nil {
		return e
	}
	v.Revision++
	if e = eventAs(tx, "series", v.ID, "anchor_updated", actor, op, now, old, v); e != nil {
		return e
	}
	_, _, e = s.materializeSeriesTx(ctx, tx, &v, op, now)
	return e
}

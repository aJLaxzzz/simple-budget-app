package planner

import (
	"budget/internal/planner/recurrence"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // Moscow horizon also works on hosts without system zoneinfo.
)

func (s *Service) horizon() string {
	loc, _ := time.LoadLocation("Europe/Moscow")
	return s.now().In(loc).AddDate(0, 0, 59).Format("2006-01-02")
}
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }
func taskFromTemplate(t TaskTemplate, date string) TaskDTO {
	return TaskDTO{Appearance: t.Appearance, Title: strings.TrimSpace(t.Title), Description: t.Description, Priority: t.Priority, ProjectID: t.ProjectID, ParentTaskID: t.ParentTaskID, ScheduledTime: t.ScheduledTime, PlannedDate: &date, Status: "planned"}
}
func templateFromTask(t TaskDTO) TaskTemplate {
	return TaskTemplate{Appearance: t.Appearance, Title: t.Title, Description: t.Description, Priority: t.Priority, ProjectID: t.ProjectID, ParentTaskID: t.ParentTaskID, ScheduledTime: t.ScheduledTime}
}

func (s *Service) CreateSeries(ctx context.Context, in CreateSeriesInput) (SeriesDTO, error) {
	var result SeriesDTO
	if in.EndDate != nil {
		if e := validDate(in.EndDate); e != nil {
			return result, e
		}
		if *in.EndDate < in.FirstDate {
			return result, invalid("Окончание серии не может предшествовать первому выполнению")
		}
	}
	if in.SourceType == "project" {
		return s.createProjectSeries(ctx, in)
	}
	if in.SourceType != "" && in.SourceType != "task" || in.TemplateID != "" || in.TemplateVersionID != "" {
		return result, invalid("Некорректный тип/источник серии")
	}
	if strings.TrimSpace(in.RequestID) == "" || len(in.RequestID) > 128 {
		return result, invalid("Требуется request_id длиной 1–128 байт для безопасного повтора создания")
	}
	if (in.Task == nil) == (in.ExistingTaskID == "") {
		return result, invalid("Укажите либо task, либо existing_task_id")
	}
	if in.ExistingTaskID == "" && in.ExpectedRevision != 0 {
		return result, invalid("expected_revision относится к существующей задаче")
	}
	if e := validDate(&in.FirstDate); e != nil {
		return result, e
	}
	if t, e := parseDate(Date(in.FirstDate)); e != nil || t.Year() < 1 {
		return result, invalid("Некорректная first_date")
	}
	rule, e := recurrence.NormalizeRule(in.Rule)
	if e != nil {
		return result, recurrenceAPIError(e)
	}
	raw, e := json.Marshal(in)
	if e != nil {
		return result, e
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	id, e := operation()
	if e != nil {
		return result, e
	}
	version, e := operation()
	if e != nil {
		return result, e
	}
	now := stamp(s.now())
	e = s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var previous, previousHash string
		e := tx.QueryRow("SELECT id,request_hash FROM series WHERE request_key=?", in.RequestID).Scan(&previous, &previousHash)
		if e == nil {
			if hash != previousHash {
				return &APIError{Status: 409, Code: "idempotency_conflict", Message: "request_id уже использован с другим содержимым"}
			}
			result, e = getSeries(tx, previous)
			if e != nil {
				return e
			}
			result.Occurrences, result.HasMore, e = seriesOccurrences(tx, previous, 100, 0)
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		var source *TaskDTO
		var snapshot TaskTemplate
		if in.Task != nil {
			snapshot = *in.Task
			if snapshot.Priority == "" {
				snapshot.Priority = "normal"
			}
		} else {
			t, e := getTask(tx, in.ExistingTaskID)
			if e != nil {
				return e
			}
			if e = checkRevision(in.ExpectedRevision, t.RecordMeta); e != nil {
				return e
			}
			if t.OccurrenceID != nil {
				return invalid("Задача уже принадлежит серии")
			}
			if t.Status == "cancelled" || t.Status == "skipped" {
				return invalid("Перед созданием серии восстановите исходную отменённую/пропущенную задачу")
			}
			if t.PlannedDate != nil && *t.PlannedDate != in.FirstDate {
				return invalid("first_date должна совпадать с плановой датой исходной задачи")
			}
			var children int
			if e = taskChildCount(tx, t.ID, &children); e != nil {
				return e
			}
			if children > 0 {
				return invalid("Повторяется отдельная задача; копирование дерева задач пока не поддерживается")
			}
			if t.Status == "completed" && (t.CompletedDate == nil || t.CompletedAt == nil) {
				return invalid("У завершённой задачи отсутствует подтверждённая фактическая дата")
			}
			source = &t
			snapshot = templateFromTask(t)
		}
		snapshot.Title = strings.TrimSpace(snapshot.Title)
		if e := validateTask(tx, taskFromTemplate(snapshot, in.FirstDate)); e != nil {
			return e
		}
		record := StoredTaskRule{Rule: rule, FirstDate: recurrence.Date(in.FirstDate), Anchor: recurrence.Anchor{Date: recurrence.Date(in.FirstDate), Kind: recurrence.ScheduleAnchor}, FirstCycle: 0}
		effectiveFrom := in.FirstDate
		// An early actual completion can place cycle one before cycle zero's
		// planned date. Version one owns all completion cycles from index zero.
		if rule.Mode == recurrence.Completion {
			effectiveFrom = "0001-01-01"
		}
		ruleJSON, _ := json.Marshal(record)
		snapshotJSON, _ := json.Marshal(snapshot)
		_, e = tx.Exec(`INSERT INTO series(id,source_type,anchor_mode,request_key,request_hash,created_at,updated_at) VALUES(?,'task',?,?,?,?,?)`, id, rule.Mode, in.RequestID, hash, now, now)
		if e != nil {
			return e
		}
		_, e = tx.Exec(`INSERT INTO series_versions(id,series_id,version,effective_from,rule_json,source_snapshot_json,created_at) VALUES(?,?,1,?,?,?,?)`, version, id, effectiveFrom, string(ruleJSON), string(snapshotJSON), now)
		if e != nil {
			return e
		}
		current := SeriesDTO{EndDate: in.EndDate, AnchorKind: recurrence.CompletedAnchor, RecordMeta: RecordMeta{ID: id, Revision: 1, CreatedAt: now, UpdatedAt: now}, SourceType: "task", AnchorMode: string(rule.Mode), State: "active", RuleVersionID: version, Rule: record, Template: snapshot, NextCycle: 1, HorizonDate: s.horizon()}
		candidate, calendars, e := calculateStoredCycle(ctx, tx, current, 0, nil)
		if e != nil {
			return e
		}
		if current.EndDate != nil && string(candidate.AdjustedDate) > *current.EndDate {
			return invalid("Первое скорректированное выполнение позже окончания серии")
		}
		task, e := s.insertCycle(tx, &current, candidate, calendars, source, id, now)
		if e != nil {
			return e
		}
		if rule.Mode == recurrence.Completion {
			if task.Status == "completed" {
				current.AnchorDate = task.CompletedDate
				current.AnchorCycle = 0
				current.WaitingOccurrenceID = nil
			} else {
				current.WaitingOccurrenceID = task.OccurrenceID
			}
		}
		_, e = tx.Exec(`INSERT INTO series_runtime(series_id,next_cycle,anchor_date,anchor_cycle,waiting_occurrence_id,horizon_date,end_date) VALUES(?,?,?,?,?,?,?)`, id, current.NextCycle, current.AnchorDate, current.AnchorCycle, current.WaitingOccurrenceID, current.HorizonDate, current.EndDate)
		if e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO series_version_scopes(version_id,series_id,from_cycle) VALUES(?,?,0)", version, id); e != nil {
			return e
		}
		if e = event(tx, "series", id, "created", id, now, nil, current); e != nil {
			return e
		}
		if _, _, e = s.materializeSeriesTx(ctx, tx, &current, id, now); e != nil {
			return e
		}
		result, e = getSeries(tx, id)
		if e != nil {
			return e
		}
		result.Occurrences, result.HasMore, e = seriesOccurrences(tx, id, 100, 0)
		return e
	})
	return result, recurrenceAPIError(e)
}

// Cycle zero represents the explicitly requested first execution. Subsequent
// cycles use the unchanged engine contract (completion requires an actual anchor).
func calculateStoredCycle(ctx context.Context, tx *sql.Tx, s SeriesDTO, index int64, calendar *recurrenceCalendarSnapshot) (recurrence.Candidate, []recurrence.CalendarInfo, error) {
	rule, anchor := s.Rule.Rule, s.Rule.Anchor
	if index == 0 {
		rule.Mode = recurrence.Schedule
		anchor = recurrence.Anchor{Date: s.Rule.FirstDate, Kind: recurrence.ScheduleAnchor}
	} else if rule.Mode == recurrence.Completion {
		if s.AnchorDate == nil {
			return recurrence.Candidate{}, nil, invalid("Нет подтверждённой опоры выполнения")
		}
		anchor = recurrence.Anchor{Date: recurrence.Date(*s.AnchorDate), CycleIndex: s.AnchorCycle, Kind: s.AnchorKind}
	}
	if calendar == nil {
		calendar = &recurrenceCalendarSnapshot{tx: tx, years: map[int]productionYearSnapshot{}}
	}
	// Preview's 100-year UI limit does not limit offline catch-up. Calculation is
	// the same CalculateCycle; the adapter supplies a stable calendar snapshot.
	candidate, e := recurrence.CalculateCycle(ctx, rule, anchor, index, calendar)
	if e != nil {
		return candidate, nil, recurrenceAPIError(e)
	}
	infos := []recurrence.CalendarInfo{}
	for _, year := range calendar.years {
		infos = append(infos, year.info)
	}
	sortCalendars(infos)
	return candidate, infos, nil
}

func (s *Service) insertCycle(tx *sql.Tx, series *SeriesDTO, c recurrence.Candidate, calendars []recurrence.CalendarInfo, source *TaskDTO, op, now string) (TaskDTO, error) {
	id, e := operation()
	if e != nil {
		return TaskDTO{}, e
	}
	task := taskFromTemplate(series.Template, string(c.AdjustedDate))
	var before any
	if source != nil {
		task = *source
		before = *source
		if task.PlannedDate == nil && active(task.Status) {
			date := string(c.AdjustedDate)
			task.PlannedDate = &date
		}
	} else {
		tid, e := operation()
		if e != nil {
			return task, e
		}
		task.RecordMeta = RecordMeta{ID: tid, Revision: 1, CreatedAt: now, UpdatedAt: now}
	}
	pending := series.AnchorMode == string(recurrence.Completion) && active(task.Status)
	var manual *string
	if task.PlannedDate != nil && *task.PlannedDate != string(c.AdjustedDate) {
		manual = task.PlannedDate
	}
	provenance, e := json.Marshal(map[string]any{"candidate": c, "calendars": calendars, "origin": "materialized", "source_task_id": seriesSourceID(source)})
	if e != nil {
		return task, e
	}
	_, e = tx.Exec(`INSERT INTO occurrences(id,series_id,rule_version_id,cycle_index,base_date,adjusted_date,manual_date,task_link,calculation_json,pending_completion,manual_override,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, series.ID, series.RuleVersionID, c.CycleIndex, c.BaseDate, c.AdjustedDate, manual, id, string(provenance), pending, manual != nil, now, now)
	if e != nil {
		return task, e
	}
	if source == nil {
		if e = validateTask(tx, task); e != nil {
			return task, e
		}
		e = insertTask(tx, task)
	} else {
		task.UpdatedAt = now
		e = saveTask(tx, task)
		task.Revision++
	}
	if e != nil {
		return task, e
	}
	_, e = tx.Exec(`UPDATE tasks SET occurrence_id=? WHERE id=?`, id, task.ID)
	if e != nil {
		return task, e
	}
	task.OccurrenceID = &id
	task.SeriesID = &series.ID
	task.RuleVersionID = &series.RuleVersionID
	index := c.CycleIndex
	task.CycleIndex = &index
	action, actor := "created", "system"
	if source != nil {
		action, actor = "recurrence_linked", "owner"
	}
	if e = eventAs(tx, "task", task.ID, action, actor, op, now, before, task); e != nil {
		return task, e
	}
	if e = eventAs(tx, "series", series.ID, "materialized", actor, op, now, nil, map[string]any{"occurrence_id": id, "task_id": task.ID, "cycle_index": index, "rule_version_id": series.RuleVersionID, "calculation": json.RawMessage(provenance)}); e != nil {
		return task, e
	}
	return task, nil
}
func seriesSourceID(t *TaskDTO) any {
	if t == nil {
		return nil
	}
	return t.ID
}
func (s *Service) Series(ctx context.Context, id string, limit, offset int) (SeriesDTO, error) {
	var result SeriesDTO
	if limit < 1 || limit > 100 || offset < 0 {
		return result, invalid("limit: 1..100, offset >=0")
	}
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var e error
		result, e = getSeries(tx, id)
		if e != nil {
			return e
		}
		result.Occurrences, result.HasMore, e = seriesOccurrences(tx, id, limit, offset)
		if e != nil {
			return e
		}
		if result.Upcoming, e = upcomingOccurrences(tx, id, s.today()); e != nil {
			return e
		}
		if e = seriesSummary(tx, &result); e != nil {
			return e
		}
		return decorateSeries(tx, &result)
	})
	return result, e
}
func (s *Service) SeriesList(ctx context.Context, after string) ([]SeriesDTO, string, error) {
	items := []SeriesDTO{}
	next := ""
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		ids, e := seriesIDs(tx, after, materializeSeriesPerBatch+1)
		if e != nil {
			return e
		}
		if len(ids) > materializeSeriesPerBatch {
			ids = ids[:materializeSeriesPerBatch]
			next = ids[len(ids)-1]
		}
		for _, id := range ids {
			v, e := getSeries(tx, id)
			if e != nil {
				return e
			}
			if e = seriesSummary(tx, &v); e != nil {
				return e
			}
			items = append(items, v)
		}
		return nil
	})
	return items, next, e
}

package planner

import (
	"budget/internal/planner/recurrence"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (s *Service) today() string {
	loc, _ := time.LoadLocation("Europe/Moscow")
	return s.now().In(loc).Format("2006-01-02")
}
func digest(v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}
func conflict(code, message string, current any) error {
	return &APIError{Status: 409, Code: code, Message: message, Current: current}
}
func managementDate(date string) error {
	value, e := parseDate(Date(date))
	if e != nil || value.Year() < 1 {
		return invalid("Требуется корректная календарная дата YYYY-MM-DD, год 0001–9999")
	}
	return nil
}
func validateSeriesCommand(v SeriesDTO, in SeriesCommand) error {
	if e := checkRevision(in.ExpectedRevision, v.RecordMeta); e != nil {
		return e
	}
	if v.State == "archived" || v.State == "disabled" {
		return invalid("Серия архивирована")
	}
	if len(in.Reason) > 5000 {
		return invalid("Причина слишком длинная")
	}
	if in.TemplateVersionID != "" || in.TasksDateDecision != "" || in.RemovedTasksDecision != "" {
		return invalid("Поля проекта неприменимы к серии задач")
	}
	if in.Action != "change_rule" && (in.Rule != nil || in.FromCycle != nil || in.FirstDate != "") {
		return invalid("Поля правила допустимы только для change_rule")
	}
	if in.Action != "pause" && (in.StartDate != "" || in.EndDate != "") {
		return invalid("Границы периода допустимы только для pause")
	}
	if in.Action != "resume" && in.PauseID != "" {
		return invalid("pause_id допустим только для resume")
	}
	if in.Action != "reanchor" && in.AnchorDate != "" {
		return invalid("anchor_date допустима только для reanchor")
	}
	switch in.Action {
	case "change_rule":
		if in.Rule == nil || in.FromCycle == nil || *in.FromCycle < 0 || *in.FromCycle > v.NextCycle {
			return invalid("Нужны rule и from_cycle от 0 до next_cycle")
		}
		if e := recurrence.ValidateRule(*in.Rule); e != nil {
			return recurrenceAPIError(e)
		}
		if string(in.Rule.Mode) != v.AnchorMode {
			return invalid("Смена режима отсчёта требует отдельной серии")
		}
		if in.Rule.Mode == recurrence.Schedule {
			if e := managementDate(in.FirstDate); e != nil {
				return e
			}
		} else if in.FirstDate != "" {
			return invalid("В режиме от выполнения опора меняется только отдельной командой reanchor")
		}
	case "pause":
		if e := managementDate(in.StartDate); e != nil {
			return e
		}
		if e := managementDate(in.EndDate); e != nil {
			return e
		}
		if in.EndDate < in.StartDate {
			return invalid("Конец паузы раньше начала")
		}
	case "resume":
		if in.PauseID == "" {
			return invalid("Укажите pause_id")
		}
	case "archive":
	case "reanchor":
		if v.AnchorMode != "completion" || v.State != "needs_anchor" || v.WaitingOccurrenceID != nil {
			return conflict("anchor_conflict", "Новая опора допустима только после пропуска/отмены без ожидающего выполнения", v)
		}
		if e := managementDate(in.AnchorDate); e != nil {
			return e
		}
	default:
		return invalid("Неизвестная команда серии")
	}
	return nil
}
func (s *Service) previewSeriesTx(ctx context.Context, tx *sql.Tx, v SeriesDTO, in SeriesCommand) (SeriesChangePreview, error) {
	if v.SourceType == "project" {
		return s.previewProjectSeriesTx(ctx, tx, v, in)
	}
	p := SeriesChangePreview{SeriesID: v.ID, Revision: v.Revision, Action: in.Action, CurrentRule: v.Rule, Rows: []SeriesChangeRow{}, Conflicts: []string{}, CalendarErrors: []*APIError{}}
	if e := validateSeriesCommand(v, in); e != nil {
		return p, e
	}
	var e error
	p.Pauses, e = loadPauses(tx, v.ID)
	if e != nil {
		return p, e
	}
	if in.Action == "pause" {
		for _, pause := range p.Pauses {
			end := in.EndDate
			if pause.EndDate != nil && *pause.EndDate < end {
				end = *pause.EndDate
			}
			start := in.StartDate
			if pause.StartDate > start {
				start = pause.StartDate
			}
			if start <= end && (pause.ResumedOn == nil || start < *pause.ResumedOn) {
				return p, conflict("pause_overlap", "Периоды пауз пересекаются", pause)
			}
		}
	}
	if in.Action == "resume" {
		found := false
		for _, pause := range p.Pauses {
			if pause.ID == in.PauseID {
				found = true
				if pause.ResumedOn != nil {
					return p, invalid("Пауза уже завершена явно")
				}
			}
		}
		if !found {
			return p, notFound("Пауза", in.PauseID)
		}
	}
	proposed := v
	if in.Action == "change_rule" {
		normalized, _ := recurrence.NormalizeRule(*in.Rule)
		r := v.Rule
		r.Rule = normalized
		r.FirstCycle = *in.FromCycle
		if normalized.Mode == recurrence.Schedule {
			r.FirstDate = recurrence.Date(in.FirstDate)
			r.Anchor = recurrence.Anchor{Date: r.FirstDate, CycleIndex: r.FirstCycle, Kind: recurrence.ScheduleAnchor}
		}
		proposed.Rule = r
		p.ProposedRule = &r
	}
	occurrences, outside, e := managementOccurrences(tx, v, in, s.today())
	if e != nil {
		return p, e
	}
	p.UnchangedOutsideScope = outside
	calendar := &recurrenceCalendarSnapshot{tx: tx, years: map[int]productionYearSnapshot{}}
	var previousDate *string
	for _, o := range occurrences {
		row := SeriesChangeRow{OccurrenceID: o.ID, TaskID: o.Task.ID, CycleIndex: o.CycleIndex, OccurrenceRevision: o.Revision, TaskRevision: o.Task.Revision, BeforeDate: o.Task.PlannedDate, AfterDate: o.Task.PlannedDate, BaseDate: o.BaseDate, AdjustedDate: o.AdjustedDate, Disposition: "unchanged", Manual: o.ManualOverride || o.ManualDate != nil}
		row.Sessions, e = listSessions(tx, "WHERE task_id=?", o.Task.ID)
		if e != nil {
			return p, e
		}
		started := o.Task.Status == "in_progress"
		activeSessions := false
		for _, session := range row.Sessions {
			if session.Status == "completed" || session.Status == "in_progress" {
				started = true
			}
			if active(session.Status) {
				activeSessions = true
			}
		}
		var children int
		if e = taskChildCount(tx, o.Task.ID, &children); e != nil {
			return p, e
		}
		if (in.Action == "change_rule" && o.CycleIndex >= *in.FromCycle) || (in.Action == "reanchor" && o.Task.Dynamic && active(o.Task.Status)) {
			switch {
			case !active(o.Task.Status):
				row.Disposition = "protected_closed"
			case started:
				row.Disposition = "protected_started"
			case row.Manual:
				row.Disposition = "manual_exception"
			case !o.Task.Dynamic && o.Task.PlannedDate != nil && *o.Task.PlannedDate < s.today():
				row.Disposition = "protected_past"
			case v.AnchorMode == "completion" && !o.Task.Dynamic && (v.WaitingOccurrenceID == nil || *v.WaitingOccurrenceID != o.ID):
				row.Disposition = "protected_history"
			default:
				candidateSeries := proposed
				if o.Task.Dynamic {
					anchor := previousDate
					if anchor == nil && in.Action == "reanchor" {
						anchor = &in.AnchorDate
					}
					if anchor == nil {
						if e = tx.QueryRow("SELECT t.planned_date FROM tasks t JOIN occurrences prev ON prev.id=t.occurrence_id WHERE prev.series_id=? AND prev.cycle_index<? AND t.status IN ('planned','in_progress') ORDER BY prev.cycle_index DESC LIMIT 1", v.ID, o.CycleIndex).Scan(&anchor); e != nil {
							return p, e
						}
					}
					candidateSeries.AnchorDate = anchor
					candidateSeries.AnchorCycle = o.CycleIndex - 1
					candidateSeries.AnchorKind = recurrence.CompletedAnchor
				}
				c, calendars, err := calculateStoredCycle(ctx, tx, candidateSeries, o.CycleIndex, calendar)
				if err != nil {
					var api *APIError
					if !errors.As(recurrenceAPIError(err), &api) {
						return p, err
					}
					p.CalendarErrors = append(p.CalendarErrors, api)
					row.Disposition = "calculation_error"
				} else {
					date := string(c.AdjustedDate)
					row.Candidate = &c
					row.Calendars = calendars
					row.AfterDate = &date
					row.Disposition = "change_date"
					if row.BeforeDate != nil && *row.BeforeDate == date {
						row.Disposition = "same_date_new_version"
					}
					if pauseOn(p.Pauses, date) != nil {
						row.PauseConflict = true
						row.Reason = "Новая дата попадает в паузу"
						if v.AnchorMode == "completion" {
							row.Disposition = "change_date_pause_pending_preserved"
							row.Reason = "Дата меняется, ожидающее выполнение сохраняется; пропуск/отмена — отдельное явное действие"
						} else if activeSessions || children > 0 {
							row.Disposition = "protected_sessions_or_children"
							row.AfterDate = row.BeforeDate
							row.Candidate = nil
						} else {
							row.Disposition = "change_and_skip"
						}
					}
				}
			}
		}
		if in.Action == "pause" {
			adjustedIn := o.AdjustedDate >= in.StartDate && o.AdjustedDate <= in.EndDate
			manualIn := row.Manual && o.Task.PlannedDate != nil && *o.Task.PlannedDate >= in.StartDate && *o.Task.PlannedDate <= in.EndDate
			if adjustedIn || manualIn {
				switch {
				case !active(o.Task.Status):
					row.Disposition = "protected_closed"
				case started:
					row.Disposition = "protected_started"
				case row.Manual:
					row.Disposition = "manual_exception"
					row.PauseConflict = true
					row.Reason = "Ручное исключение сохранено"
				case v.AnchorMode == "completion":
					row.Disposition = "completion_pending_preserved"
					row.PauseConflict = true
					row.Reason = "Ожидающее выполнение сохранено; пропуск/отмена — отдельное явное действие"
				case activeSessions || children > 0:
					row.Disposition = "protected_sessions_or_children"
					row.PauseConflict = true
				default:
					row.Disposition = "skip_pause"
				}
			}
		}
		if in.Action == "resume" && o.Task.Status == "skipped" && !row.Manual && o.AdjustedDate >= s.today() {
			var n int
			if e = tx.QueryRow("SELECT count(*) FROM occurrence_incidents WHERE occurrence_id=? AND pause_id=? AND disposition='needs_decision'", o.ID, in.PauseID).Scan(&n); e != nil {
				return p, e
			}
			if n > 0 && v.AnchorMode == "schedule" {
				row.Disposition = "restore_after_resume"
			}
		}
		if row.PauseConflict {
			p.Conflicts = append(p.Conflicts, o.ID+": "+row.Disposition)
		}
		p.Rows = append(p.Rows, row)
		if active(o.Task.Status) {
			previousDate = row.AfterDate
		}
	}
	// Read-only sample of not-yet-created cycles. Completion has only one confirmed next cycle.
	if in.Action == "change_rule" || in.Action == "pause" || in.Action == "reanchor" {
		sample := proposed
		if in.Action == "reanchor" {
			sample.AnchorDate = &in.AnchorDate
			sample.AnchorCycle = sample.NextCycle - 1
			sample.AnchorKind = recurrence.ExplicitAnchor
			sample.WaitingOccurrenceID = nil
		}
		n := 10
		if sample.AnchorMode == "completion" {
			n = 1
			if in.Action == "reanchor" && len(occurrences) > 0 {
				n = 0
			}
			if sample.WaitingOccurrenceID != nil || sample.AnchorDate == nil {
				n = 0
			}
		}
		for i := 0; i < n; i++ {
			index := sample.NextCycle + int64(i)
			upper := v.HorizonDate
			if in.Action == "pause" && in.EndDate < upper {
				upper = in.EndDate
			}
			upperTime, _ := parseDate(Date(upper))
			baseRule, baseAnchor := sample.Rule.Rule, sample.Rule.Anchor
			if sample.AnchorMode == "completion" {
				baseAnchor = recurrence.Anchor{Date: recurrence.Date(*sample.AnchorDate), CycleIndex: sample.AnchorCycle, Kind: sample.AnchorKind}
			}
			base, baseErr := recurrence.BaseDate(baseRule, baseAnchor, index)
			lookahead := upperTime
			if baseRule.Adjustment == recurrence.Previous || baseRule.Adjustment == recurrence.Nearest {
				lookahead = lookahead.AddDate(0, 0, recurrence.MaxAdjustmentDays)
			}
			if baseErr == nil && string(base) > lookahead.Format("2006-01-02") {
				break
			}
			c, cal, err := calculateStoredCycle(ctx, tx, sample, index, calendar)
			if err != nil {
				var api *APIError
				if !errors.As(recurrenceAPIError(err), &api) {
					return p, err
				}
				p.CalendarErrors = append(p.CalendarErrors, api)
				break
			}
			date := string(c.AdjustedDate)
			if date > upper {
				break
			}
			row := SeriesChangeRow{CycleIndex: index, BaseDate: string(c.BaseDate), AdjustedDate: date, AfterDate: &date, Disposition: "not_materialized", Candidate: &c, Calendars: cal, Sessions: []SessionDTO{}}
			if in.Action == "pause" && date >= in.StartDate && date <= in.EndDate {
				row.Disposition = "future_pause_skip"
			}
			p.Rows = append(p.Rows, row)
		}
	}
	clean := in
	clean.RequestID = ""
	clean.PreviewToken = ""
	token, e := digest(struct {
		Input   SeriesCommand
		Preview SeriesChangePreview
		Today   string
	}{clean, p, s.today()})
	p.PreviewToken = token
	return p, e
}
func (s *Service) PreviewSeriesChange(ctx context.Context, id string, in SeriesCommand) (SeriesChangePreview, error) {
	var p SeriesChangePreview
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		v, e := getSeries(tx, id)
		if e != nil {
			return e
		}
		p, e = s.previewSeriesTx(ctx, tx, v, in)
		return e
	})
	return p, e
}

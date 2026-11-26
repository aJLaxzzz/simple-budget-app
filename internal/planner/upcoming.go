package planner

import (
	"budget/internal/planner/recurrence"
	"context"
	"database/sql"
	"errors"
)

type UpcomingResult struct {
	Created int `json:"created"`
	// Kept for wire compatibility; list clients receive real tasks, never placeholders.
	Forecast []recurrence.Candidate `json:"forecast"`
}

// Fill a small number of real cycles, independently of the calendar horizon.
// Iteration advances cycle indices, never days; deleted cycles and pauses remain authoritative.
func (s *Service) ensureUpcomingTx(ctx context.Context, tx *sql.Tx, v *SeriesDTO, op, now string) (int, error) {
	if v.SourceType != "task" || v.State != "active" {
		return 0, nil
	}
	if v.AnchorMode == "completion" {
		n, _, e := s.syncCompletionChain(ctx, tx, v, op, now, false, "")
		return n, e
	}
	limit, err := projectRepeatLimit(tx)
	if err != nil {
		return 0, err
	}
	var count int
	if e := tx.QueryRow(`SELECT count(*) FROM tasks t JOIN occurrences o ON o.id=t.occurrence_id WHERE o.series_id=? AND t.status IN ('planned','in_progress') AND t.planned_date>=? AND (t.due_date IS NULL OR t.due_date>=?)`, v.ID, s.today(), s.today()).Scan(&count); e != nil {
		return 0, e
	}
	pauses, e := loadPauses(tx, v.ID)
	if e != nil {
		return 0, e
	}
	created := 0
	for attempt := 0; count < limit.Limit && attempt < materializeCyclesPerBatch; attempt++ {
		if e = ctx.Err(); e != nil {
			return 0, e
		}
		var removed int
		if e = tx.QueryRow("SELECT count(*) FROM removed_cycles WHERE series_id=? AND cycle_index=?", v.ID, v.NextCycle).Scan(&removed); e != nil {
			return 0, e
		}
		if removed == 0 {
			selected, err := versionForCycle(tx, *v, v.NextCycle)
			if err != nil {
				return 0, err
			}
			if v.EndDate != nil {
				base, err := recurrence.BaseDate(selected.Rule.Rule, selected.Rule.Anchor, v.NextCycle)
				if err != nil {
					var re *recurrence.Error
					if errors.As(err, &re) && re.Code == "recurrence_date_range" {
						break
					}
					return 0, recurrenceAPIError(err)
				}
				end, _ := parseDate(Date(*v.EndDate))
				if selected.Rule.Rule.Adjustment == recurrence.Previous || selected.Rule.Rule.Adjustment == recurrence.Nearest {
					end = end.AddDate(0, 0, recurrence.MaxAdjustmentDays)
				}
				if string(base) > end.Format("2006-01-02") {
					break
				}
			}
			c, calendars, err := calculateStoredCycle(ctx, tx, selected, v.NextCycle, nil)
			if err != nil {
				var api *APIError
				if errors.As(err, &api) && api.Code == "recurrence_date_range" {
					break
				}
				return 0, err
			}
			date := string(c.AdjustedDate)
			if v.EndDate != nil && date > *v.EndDate {
				break
			}
			// A future paused cycle is not silently skipped to invent a new minimum.
			if pauseOn(pauses, date) != nil {
				break
			}
			if _, err = s.insertCycle(tx, &selected, c, calendars, nil, op, now); err != nil {
				return 0, err
			}
			created++
			if date >= s.today() {
				count++
			}
		}
		v.NextCycle++
		if e = saveSeriesRuntime(tx, *v, now); e != nil {
			return 0, e
		}
		v.Revision++
	}
	return created, nil
}
func (s *Service) EnsureUpcoming(ctx context.Context, id string) (UpcomingResult, error) {
	out := UpcomingResult{Forecast: []recurrence.Candidate{}}
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		v, e := getSeries(tx, id)
		if e != nil {
			return e
		}
		op, e := operation()
		if e != nil {
			return e
		}
		out.Created, e = s.ensureUpcomingTx(ctx, tx, &v, op, stamp(s.now()))
		return e
	})
	s.recordCalculationError(ctx, id, e)
	return out, e
}

// A single projection per source, not a new incident on every failed attempt.
func (s *Service) recordCalculationError(ctx context.Context, id string, err error) {
	var value *string
	if err != nil {
		var api *APIError
		if !errors.As(recurrenceAPIError(err), &api) {
			return
		}
		message := api.Message
		value = &message
	}
	_ = s.store.Transaction(ctx, func(tx *sql.Tx) error {
		// Rechecking an unchanged projection must not produce a write notification:
		// live clients materialize again when refreshing their calendar.
		_, e := tx.Exec("UPDATE series SET calculation_error=? WHERE id=? AND calculation_error IS NOT ?", value, id, value)
		return e
	})
}

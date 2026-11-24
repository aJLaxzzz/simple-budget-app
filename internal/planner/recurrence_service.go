package planner

import (
	"budget/internal/planner/recurrence"
	"context"
	"database/sql"
)

func (s *Service) PreviewRecurrence(ctx context.Context, in recurrence.PreviewRequest) (recurrence.PreviewResult, error) {
	if e := recurrence.ValidatePreview(in); e != nil {
		return recurrence.PreviewResult{}, recurrenceAPIError(e)
	}
	// Simple civil rules do not access SQLite at all.
	if (in.Rule.AllowedDays.Mode != recurrence.RussianWorkdays && in.Rule.AllowedDays.Mode != recurrence.RussianWeekdays) || in.Anchor.Kind == recurrence.UnconfirmedAnchor {
		result, e := recurrence.Preview(ctx, in, nil)
		return result, recurrenceAPIError(e)
	}
	var result recurrence.PreviewResult
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		calendar := &recurrenceCalendarSnapshot{tx: tx, years: map[int]productionYearSnapshot{}}
		var err error
		result, err = recurrence.Preview(ctx, in, calendar)
		return err
	})
	return result, recurrenceAPIError(e)
}

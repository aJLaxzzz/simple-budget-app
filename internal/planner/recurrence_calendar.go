package planner

import (
	"budget/internal/planner/recurrence"
	"context"
	"database/sql"
	"errors"
	"time"
)

type productionYearSnapshot struct {
	info recurrence.CalendarInfo
	days map[recurrence.Date]recurrence.ProductionDay
}

// Lazy year loading within a single transaction guarantees that a concurrent
// calendar import cannot mix revisions during one calculation. No fallback data.
type recurrenceCalendarSnapshot struct {
	tx    *sql.Tx
	years map[int]productionYearSnapshot
}

func (s *recurrenceCalendarSnapshot) Day(ctx context.Context, date recurrence.Date) (recurrence.ProductionDay, error) {
	t, e := parseDate(Date(date))
	if e != nil {
		return recurrence.ProductionDay{}, e
	}
	year, ok := s.years[t.Year()]
	if !ok {
		year = productionYearSnapshot{days: map[recurrence.Date]recurrence.ProductionDay{}}
		year.info.Year = t.Year()
		e = s.tx.QueryRowContext(ctx, "SELECT source,source_checksum,revision FROM production_calendar_years WHERE year=?", t.Year()).Scan(&year.info.Source, &year.info.Checksum, &year.info.Revision)
		if errors.Is(e, sql.ErrNoRows) {
			return recurrence.ProductionDay{}, &recurrence.MissingCalendarError{Year: t.Year()}
		}
		if e != nil {
			return recurrence.ProductionDay{}, e
		}
		rows, e := s.tx.QueryContext(ctx, "SELECT date,kind,reason FROM production_calendar_days WHERE year=? ORDER BY date", t.Year())
		if e != nil {
			return recurrence.ProductionDay{}, e
		}
		for rows.Next() {
			var d recurrence.Date
			day := recurrence.ProductionDay{Calendar: year.info}
			if e = rows.Scan(&d, &day.Kind, &day.Reason); e != nil {
				rows.Close()
				return recurrence.ProductionDay{}, e
			}
			year.days[d] = day
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return recurrence.ProductionDay{}, e
		}
		for day := time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.UTC); day.Year() == t.Year(); day = day.AddDate(0, 0, 1) {
			if _, ok := year.days[recurrence.Date(day.Format("2006-01-02"))]; !ok {
				return recurrence.ProductionDay{}, &recurrence.MissingCalendarError{Year: t.Year()}
			}
		}
		s.years[t.Year()] = year
	}
	day, ok := year.days[date]
	if !ok {
		return recurrence.ProductionDay{}, &recurrence.MissingCalendarError{Year: t.Year()}
	}
	return day, nil
}

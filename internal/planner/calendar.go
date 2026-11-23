package planner

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type DayMode string

const (
	Weekdays                  DayMode = "weekdays"
	RussianProductionCalendar DayMode = "ru_production"
)

var ErrCalendarUnavailable = errors.New("official Russian production calendar is unavailable")

type CalendarDay struct {
	Date         Date
	Kind, Reason string
}
type CalendarYear struct {
	Year   int
	Source string
	Days   []CalendarDay
}
type Calendar interface {
	IsWorkingDay(context.Context, Date, DayMode) (bool, error)
}

// ImportCalendarYear accepts a complete, externally verified year. It does not
// download or invent holidays. Source identifies the official source document.
func validateCalendarYear(year CalendarYear) error {
	if year.Year < 1900 || year.Year > 9999 || year.Source == "" {
		return errors.New("calendar year and official source are required")
	}
	start := time.Date(year.Year, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(1, 0, 0)
	expected := int(end.Sub(start).Hours() / 24)
	if len(year.Days) != expected {
		return errors.New("calendar must contain every date of the year")
	}
	seen := map[Date]bool{}
	for _, d := range year.Days {
		t, e := parseDate(d.Date)
		if e != nil || t.Year() != year.Year || seen[d.Date] || (d.Kind != "working" && d.Kind != "nonworking" && d.Kind != "shortened") || d.Reason == "" {
			return fmt.Errorf("invalid calendar day %q", d.Date)
		}
		seen[d.Date] = true
	}
	return nil
}
func (s *Store) ImportCalendarYear(ctx context.Context, year CalendarYear) error {
	if e := validateCalendarYear(year); e != nil {
		return e
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		return importCalendarYearTx(ctx, tx, year, time.Now().UTC().Format(time.RFC3339Nano))
	})
}
func importCalendarYearTx(ctx context.Context, tx *sql.Tx, year CalendarYear, now string) error {
	raw, _ := json.Marshal(year)
	checksum := fmt.Sprintf("%x", sha256.Sum256(raw))
	_, e := tx.ExecContext(ctx, `INSERT INTO production_calendar_years(year,source,source_checksum,imported_at) VALUES(?,?,?,?) ON CONFLICT(year) DO UPDATE SET source=excluded.source,source_checksum=excluded.source_checksum,imported_at=excluded.imported_at,revision=revision+1,updated_at=excluded.imported_at`, year.Year, year.Source, checksum, now)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM production_calendar_days WHERE year=?", year.Year); e != nil {
		return e
	}
	for _, d := range year.Days {
		if _, e = tx.ExecContext(ctx, "INSERT INTO production_calendar_days(date,year,kind,reason) VALUES(?,?,?,?)", d.Date, year.Year, d.Kind, d.Reason); e != nil {
			return e
		}
	}
	return nil
}
func parseDate(date Date) (time.Time, error) {
	t, e := time.Parse("2006-01-02", string(date))
	if e == nil && t.Format("2006-01-02") != string(date) {
		e = errors.New("invalid civil date")
	}
	return t, e
}
func (s *Store) IsWorkingDay(ctx context.Context, date Date, mode DayMode) (bool, error) {
	t, e := parseDate(date)
	if e != nil {
		return false, e
	}
	if mode == Weekdays {
		return t.Weekday() != time.Saturday && t.Weekday() != time.Sunday, nil
	}
	if mode != RussianProductionCalendar {
		return false, errors.New("unknown working-day mode")
	}
	var kind string
	e = s.Transaction(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT kind FROM production_calendar_days WHERE date=?", date).Scan(&kind)
	})
	if errors.Is(e, sql.ErrNoRows) {
		return false, fmt.Errorf("%w: %d", ErrCalendarUnavailable, t.Year())
	}
	return kind == "working" || kind == "shortened", e
}

package planner

import (
	"errors"
	"testing"
	"time"
)

func TestCalendarModesAndMissingYear(t *testing.T) {
	s := testStore(t)
	if working, e := s.IsWorkingDay(ctx, "2026-11-16", Weekdays); e != nil || !working {
		t.Fatal(working, e)
	}
	if working, e := s.IsWorkingDay(ctx, "2026-11-15", Weekdays); e != nil || working {
		t.Fatal(working, e)
	}
	if _, e := s.IsWorkingDay(ctx, "2026-11-16", RussianProductionCalendar); !errors.Is(e, ErrCalendarUnavailable) {
		t.Fatal(e)
	}
	// Deliberately synthetic fixture, not a real Russian holiday calendar.
	year := CalendarYear{Year: 2024, Source: "synthetic test fixture; not official data"}
	for d := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC); d.Year() == 2024; d = d.AddDate(0, 0, 1) {
		year.Days = append(year.Days, CalendarDay{Date: Date(d.Format("2006-01-02")), Kind: "nonworking", Reason: "synthetic fixture"})
	}
	year.Days[5].Kind = "working" // Saturday: explicit transferred workday
	if e := s.ImportCalendarYear(ctx, year); e != nil {
		t.Fatal(e)
	}
	if working, e := s.IsWorkingDay(ctx, "2024-01-06", RussianProductionCalendar); e != nil || !working {
		t.Fatal(working, e)
	}
	if working, e := s.IsWorkingDay(ctx, "2024-01-01", RussianProductionCalendar); e != nil || working {
		t.Fatal(working, e)
	}
	if working, e := s.IsWorkingDay(ctx, "2024-01-01", Weekdays); e != nil || !working {
		t.Fatal("modes mixed", working, e)
	}
	year.Days[5].Kind = "shortened"
	if e := s.ImportCalendarYear(ctx, year); e != nil {
		t.Fatal(e)
	}
	if count(t, s, "production_calendar_days") != 366 {
		t.Fatal("duplicate import")
	}
	year.Days = year.Days[:365]
	if e := s.ImportCalendarYear(ctx, year); e == nil {
		t.Fatal("partial year accepted")
	}
	if count(t, s, "production_calendar_days") != 366 {
		t.Fatal("partial import damaged prior calendar")
	}
	if _, e := s.IsWorkingDay(ctx, "2024-02-30", Weekdays); e == nil {
		t.Fatal("invalid date")
	}
}

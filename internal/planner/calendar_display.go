package planner

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// CalendarDisplayDay describes imported facts only; it never computes holidays.
// The original kind/reason remain available, including shortened working days.
type CalendarDisplayDay struct {
	Date      string `json:"date"`
	Kind      string `json:"kind"`
	WorkKind  string `json:"work_kind"`
	Label     string `json:"label"`
	Reason    string `json:"reason"`
	Shortened bool   `json:"shortened"`
}

func displayCalendarDay(date, kind, reason string) CalendarDisplayDay {
	d := CalendarDisplayDay{Date: date, WorkKind: kind, Reason: reason, Shortened: kind == "shortened"}
	t, _ := parseDate(Date(date))
	weekend := t.Weekday() == time.Saturday || t.Weekday() == time.Sunday
	text := strings.ToLower(reason)
	switch {
	case kind == "working" || kind == "shortened":
		d.Kind, d.Label = "working", "Рабочий день"
		if weekend {
			d.Kind, d.Label = "working_weekend", "Рабочая суббота"
			if t.Weekday() == time.Sunday {
				d.Label = "Рабочее воскресенье"
			}
		}
	case strings.Contains(text, "нерабочий праздничный день"):
		d.Kind, d.Label = "holiday", "Государственный праздник"
	case strings.Contains(text, "перенос") || strings.Contains(text, "совпадение"):
		d.Kind, d.Label = "transferred_day_off", "Выходной по переносу"
	case weekend:
		d.Kind, d.Label = "weekend", "Выходной"
	default:
		d.Kind, d.Label = "nonworking", "Нерабочий день"
	}
	if d.Shortened {
		d.Label += " · сокращённый"
	}
	return d
}

func calendarDisplay(ctx context.Context, tx *sql.Tx, start, end time.Time) ([]CalendarDisplayDay, []int, error) {
	days, missing := []CalendarDisplayDay{}, []int{}
	confirmed := map[int]bool{}
	for year := start.Year(); year <= end.Year(); year++ {
		summary, err := calendarSummaryTx(tx, year)
		if err != nil {
			return nil, nil, err
		}
		confirmed[year] = summary.Complete && summary.PublishedAt != "" && summary.Version != ""
		if !confirmed[year] {
			missing = append(missing, year)
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT date,year,kind,reason FROM production_calendar_days WHERE date>=? AND date<=? ORDER BY date`, start.Format("2006-01-02"), end.Format("2006-01-02"))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var date, kind, reason string
		var year int
		if err = rows.Scan(&date, &year, &kind, &reason); err != nil {
			return nil, nil, err
		}
		if confirmed[year] {
			days = append(days, displayCalendarDay(date, kind, reason))
		}
	}
	return days, missing, rows.Err()
}

package recurrence

import (
	"context"
	"fmt"
)

type Adjusted struct {
	Date               Date
	ShiftDays          int
	ReasonCode, Reason string
}

func permitted(ctx context.Context, date Date, days AllowedDays, calendar ProductionCalendar) (bool, string, string, error) {
	if e := ctx.Err(); e != nil {
		return false, "", "", e
	}
	value, e := parse(date)
	if e != nil {
		return false, "", "", e
	}
	weekday := (int(value.Weekday())+6)%7 + 1
	switch days.Mode {
	case AnyDay:
		return true, "allowed", "Любой календарный день", nil
	case Weekdays:
		return weekday <= 5, "weekday_required", "Базовая дата не является обычным будним днём (пн–пт)", nil
	case Weekends:
		return weekday >= 6, "weekend_required", "Базовая дата не является субботой или воскресеньем", nil
	case SelectedDays:
		for _, day := range days.Weekdays {
			if day == weekday {
				return true, "allowed", "Разрешённый день недели", nil
			}
		}
		return false, "selected_weekday_required", "Базовый день недели не входит в выбранный список", nil
	case RussianWorkdays, RussianWeekdays:
		if calendar == nil {
			return false, "", "", &MissingCalendarError{Year: value.Year()}
		}
		day, e := calendar.Day(ctx, date)
		if e != nil {
			return false, "", "", e
		}
		if day.Calendar.Year != value.Year() || day.Calendar.Source == "" || day.Calendar.Checksum == "" || day.Calendar.Revision <= 0 || (day.Kind != "working" && day.Kind != "shortened" && day.Kind != "nonworking") {
			return false, "", "", failure("invalid_production_calendar", "Производственный календарь содержит неполные сведения")
		}
		return day.Kind != "nonworking" && (days.Mode != RussianWeekdays || weekday <= 5), "ru_nonworking", "Нерабочий день по производственному календарю РФ: " + day.Reason, nil
	}
	return false, "", "", failure("invalid_recurrence_rule", "Неизвестное правило допустимых дней")
}

func AdjustDate(ctx context.Context, base Date, days AllowedDays, policy Adjustment, calendar ProductionCalendar) (Adjusted, error) {
	// Reuse the same static validation for direct callers and preview.
	_, e := NormalizeRule(Rule{SchemaVersion: 1, Mode: Schedule, Unit: Days, Interval: 1, AllowedDays: days, Adjustment: policy})
	if e != nil {
		return Adjusted{}, e
	}
	allowed, code, reason, e := permitted(ctx, base, days, calendar)
	if e != nil {
		return Adjusted{}, e
	}
	if allowed {
		return Adjusted{Date: base, ReasonCode: "allowed", Reason: "Базовая дата разрешена; перенос не требуется"}, nil
	}
	for distance := 1; distance <= MaxAdjustmentDays; distance++ {
		offsets := []int{distance}
		if policy == Previous {
			offsets = []int{-distance}
		} else if policy == Nearest {
			offsets = []int{distance, -distance}
		}
		// Next is deliberately inspected first: an exact tie always goes forward.
		for _, offset := range offsets {
			candidate, e := addDays(base, offset)
			if e != nil {
				return Adjusted{}, e
			}
			ok, _, _, e := permitted(ctx, candidate, days, calendar)
			if e != nil {
				return Adjusted{}, e
			}
			if ok {
				return Adjusted{Date: candidate, ShiftDays: offset, ReasonCode: code, Reason: fmt.Sprintf("%s; перенос на %s (%+d дн.)", reason, candidate, offset)}, nil
			}
		}
	}
	return Adjusted{}, failure("recurrence_adjustment_limit", "Не найден допустимый день в пределах 366 дней от базовой даты")
}

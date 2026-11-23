package recurrence

import (
	"context"
	"sort"
)

// CalculateCycle is the shared primitive for preview and the future materializer.
// It returns a candidate, not a stored occurrence; conditional forecasts are not
// generated. Existing manual dates and closed objects belong to persistence.
func CalculateCycle(ctx context.Context, rule Rule, anchor Anchor, cycle int64, calendar ProductionCalendar) (Candidate, error) {
	if e := ctx.Err(); e != nil {
		return Candidate{}, e
	}
	normalized, e := NormalizeRule(rule)
	if e != nil {
		return Candidate{}, e
	}
	base, e := BaseDate(normalized, anchor, cycle)
	if e != nil {
		return Candidate{}, e
	}
	adjusted, e := AdjustDate(ctx, base, normalized.AllowedDays, normalized.Adjustment, calendar)
	if e != nil {
		return Candidate{}, e
	}
	return Candidate{CycleIndex: cycle, AnchorDate: anchor.Date, BaseDate: base, AdjustedDate: adjusted.Date, AllowedDays: normalized.AllowedDays, Adjustment: normalized.Adjustment, ShiftDays: adjusted.ShiftDays, ReasonCode: adjusted.ReasonCode, Reason: adjusted.Reason, Conditional: false}, nil
}

type trackedCalendar struct {
	source ProductionCalendar
	used   map[int]CalendarInfo
}

func (c *trackedCalendar) Day(ctx context.Context, date Date) (ProductionDay, error) {
	value, _ := parse(date)
	if c.source == nil {
		return ProductionDay{}, &MissingCalendarError{Year: value.Year()}
	}
	day, e := c.source.Day(ctx, date)
	if e != nil {
		return ProductionDay{}, e
	}
	if prior, ok := c.used[day.Calendar.Year]; ok && prior != day.Calendar {
		return ProductionDay{}, failure("production_calendar_changed", "Производственный календарь изменился во время расчёта; нужен согласованный снимок")
	}
	c.used[day.Calendar.Year] = day.Calendar
	return day, nil
}
func Preview(ctx context.Context, in PreviewRequest, calendar ProductionCalendar) (PreviewResult, error) {
	if e := ValidatePreview(in); e != nil {
		return PreviewResult{}, e
	}
	if e := ctx.Err(); e != nil {
		return PreviewResult{}, e
	}
	rule, _ := NormalizeRule(in.Rule)
	result := PreviewResult{Rule: rule, Anchor: in.Anchor, RequestedCount: in.Count, Items: []Candidate{}, Status: "ready", Calendars: []CalendarInfo{}}
	if rule.Mode == Completion && in.Anchor.Kind == UnconfirmedAnchor {
		result.Status = "needs_anchor"
		result.Reason = "Следующий цикл требует фактического завершения или явно подтверждённой новой опоры"
		return result, nil
	}
	start, _ := startCycle(in)
	count := in.Count
	if rule.Mode == Completion && count > 1 {
		count = 1
		result.Status = "awaiting_completion"
		result.Reason = "Рассчитан только ближайший цикл. Для дальнейших дат нужно его фактическое завершение; условный прогноз не строится"
	}
	origin, _ := parse(in.Anchor.Date)
	endYear := origin.Year() + MaxPreviewYears
	if endYear > 9999 {
		endYear = 9999
	}
	maxDate, _ := civil(clamped(endYear, origin.Month(), origin.Day()))
	tracker := &trackedCalendar{source: calendar, used: map[int]CalendarInfo{}}
	for offset := 0; offset < count; offset++ {
		index := start + int64(offset)
		// Check horizon before touching potentially unavailable calendar years.
		base, e := BaseDate(rule, in.Anchor, index)
		if e != nil {
			return PreviewResult{}, e
		}
		if base > maxDate {
			return PreviewResult{}, failure("recurrence_limit", "Предпросмотр ограничен 100 календарными годами от опоры")
		}
		candidate, e := CalculateCycle(ctx, rule, in.Anchor, index, tracker)
		if e != nil {
			return PreviewResult{}, e
		}
		if candidate.AdjustedDate > maxDate {
			return PreviewResult{}, failure("recurrence_limit", "Скорректированная дата выходит за горизонт предпросмотра (100 лет)")
		}
		result.Items = append(result.Items, candidate)
	}
	for _, info := range tracker.used {
		result.Calendars = append(result.Calendars, info)
	}
	sort.Slice(result.Calendars, func(i, j int) bool { return result.Calendars[i].Year < result.Calendars[j].Year })
	return result, nil
}

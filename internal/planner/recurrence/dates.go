package recurrence

import "time"

func parse(date Date) (time.Time, error) {
	value, e := time.Parse("2006-01-02", string(date))
	if e != nil || value.Year() < 1 || value.Year() > 9999 || value.Format("2006-01-02") != string(date) {
		return time.Time{}, failure("invalid_recurrence_date", "Ожидается календарная дата YYYY-MM-DD, год 0001–9999")
	}
	return value, nil
}
func civil(t time.Time) (Date, error) {
	if t.Year() < 1 || t.Year() > 9999 {
		return "", failure("recurrence_date_range", "Дата выходит за годы 0001–9999")
	}
	return Date(t.Format("2006-01-02")), nil
}
func addDays(date Date, n int) (Date, error) {
	t, e := parse(date)
	if e != nil {
		return "", e
	}
	return civil(t.AddDate(0, 0, n))
}
func clamped(year int, month time.Month, day int) time.Time {
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > last {
		day = last
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// BaseDate always computes from the supplied immutable origin. It never consumes
// a previously adjusted/manual date. With completion, the caller must supply a
// new confirmed actual date for each next cycle.
func BaseDate(rule Rule, anchor Anchor, cycle int64) (Date, error) {
	var err error
	rule, err = NormalizeRule(rule)
	if err != nil {
		return "", err
	}
	if err = validateAnchor(rule, anchor); err != nil {
		return "", err
	}
	if rule.Mode == Completion && anchor.Kind == UnconfirmedAnchor {
		return "", failure("recurrence_needs_anchor", "Нет подтверждённого выполнения или новой опоры")
	}
	if cycle < anchor.CycleIndex {
		return "", failure("invalid_recurrence_cycle", "Цикл предшествует опоре")
	}
	if rule.Mode == Completion && cycle-anchor.CycleIndex != 1 {
		return "", failure("recurrence_needs_anchor", "Для следующего цикла требуется фактическое завершение предыдущего")
	}
	origin, _ := parse(anchor.Date)
	steps := cycle - anchor.CycleIndex
	// Bound multiplication BEFORE it occurs, including extreme int64 inputs.
	maxUnits := int64(3652058)
	switch rule.Unit {
	case Weeks:
		maxUnits /= 7
	case Months:
		maxUnits = 119987
	case Years:
		maxUnits = 9998
	}
	if steps > maxUnits/rule.Interval {
		return "", failure("recurrence_date_range", "Интервал выходит за поддерживаемый диапазон дат")
	}
	units := steps * rule.Interval
	var result time.Time
	switch rule.Unit {
	case Days:
		result = origin.AddDate(0, 0, int(units))
	case Weeks:
		result = origin.AddDate(0, 0, int(units*7))
	case Months:
		monthIndex := int64(origin.Year()-1)*12 + int64(origin.Month()-1) + units
		if monthIndex > 119987 {
			return "", failure("recurrence_date_range", "Месяц выходит за год 9999")
		}
		result = clamped(int(monthIndex/12)+1, time.Month(monthIndex%12)+1, origin.Day())
	case Years:
		year := int64(origin.Year()) + units
		if year > 9999 {
			return "", failure("recurrence_date_range", "Год выходит за 9999")
		}
		result = clamped(int(year), origin.Month(), origin.Day())
	}
	return civil(result)
}

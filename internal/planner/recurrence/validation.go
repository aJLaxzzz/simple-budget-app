package recurrence

import (
	"math"
	"sort"
)

func NormalizeRule(rule Rule) (Rule, error) {
	bad := func(message string) (Rule, error) { return Rule{}, failure("invalid_recurrence_rule", message) }
	if rule.SchemaVersion != 1 {
		return bad("Поддерживается schema_version=1")
	}
	if rule.Timezone == "" {
		rule.Timezone = Timezone
	}
	if rule.Timezone != Timezone {
		return bad("Часовой пояс повторений — Europe/Moscow")
	}
	if rule.Mode != Schedule && rule.Mode != Completion {
		return bad("Режим должен быть schedule или completion")
	}
	if rule.Unit != Days && rule.Unit != Weeks && rule.Unit != Months && rule.Unit != Years {
		return bad("Единица интервала: days, weeks, months или years")
	}
	if rule.Interval <= 0 {
		return bad("Интервал должен быть положительным целым числом")
	}
	if rule.Adjustment != Next && rule.Adjustment != Previous && rule.Adjustment != Nearest && rule.Adjustment != NoAdjustment {
		return bad("Корректировка: next, previous, nearest или none")
	}
	switch rule.AllowedDays.Mode {
	case AnyDay, Weekdays, Weekends, RussianWorkdays:
		if len(rule.AllowedDays.Weekdays) > 0 {
			return bad("Список weekdays разрешён только для режима selected")
		}
	case SelectedDays:
		if len(rule.AllowedDays.Weekdays) == 0 || len(rule.AllowedDays.Weekdays) > 7 {
			return bad("Выберите от 1 до 7 дней недели")
		}
		days := append([]int(nil), rule.AllowedDays.Weekdays...)
		sort.Ints(days)
		for i, day := range days {
			if day < 1 || day > 7 || (i > 0 && days[i-1] == day) {
				return bad("Дни недели должны быть уникальными числами 1–7 (понедельник–воскресенье)")
			}
		}
		rule.AllowedDays.Weekdays = days
	default:
		return bad("Неизвестное правило допустимых дней")
	}
	if rule.Adjustment == NoAdjustment && rule.AllowedDays.Mode != AnyDay {
		return bad("При ограничении допустимых дней выберите направление переноса; none допустим только для any")
	}
	return rule, nil
}
func ValidateRule(rule Rule) error { _, err := NormalizeRule(rule); return err }
func validateAnchor(rule Rule, anchor Anchor) error {
	if anchor.CycleIndex < 0 {
		return failure("invalid_recurrence_anchor", "Номер опорного цикла не может быть отрицательным")
	}
	if rule.Mode == Schedule {
		if anchor.Kind != ScheduleAnchor {
			return failure("invalid_recurrence_anchor", "Для расписания нужна опора kind=schedule")
		}
	} else {
		switch anchor.Kind {
		case CompletedAnchor, ExplicitAnchor:
		case UnconfirmedAnchor:
			if anchor.Date != "" {
				return failure("invalid_recurrence_anchor", "Неподтверждённая опора не должна содержать дату: перенос не является завершением")
			}
			return nil
		default:
			return failure("invalid_recurrence_anchor", "Для отсчёта от выполнения нужна опора completion, reanchor или unconfirmed")
		}
	}
	_, err := parse(anchor.Date)
	if err != nil {
		return failure("invalid_recurrence_anchor", "Опорная дата должна иметь формат YYYY-MM-DD и год 0001–9999")
	}
	return nil
}
func ValidatePreview(in PreviewRequest) error {
	rule, err := NormalizeRule(in.Rule)
	if err != nil {
		return err
	}
	if err = validateAnchor(rule, in.Anchor); err != nil {
		return err
	}
	if in.Count < 1 || in.Count > MaxPreviewCount {
		return failure("recurrence_limit", "Количество выполнений должно быть от 1 до 50")
	}
	start, err := startCycle(in)
	if err != nil {
		return err
	}
	if start < in.Anchor.CycleIndex || start > math.MaxInt64-int64(in.Count-1) {
		return failure("recurrence_limit", "Недопустимый диапазон номеров циклов")
	}
	if rule.Mode == Completion && (in.Anchor.CycleIndex == math.MaxInt64 || start != in.Anchor.CycleIndex+1) {
		return failure("invalid_recurrence_anchor", "От выполнения можно рассчитать только цикл, следующий за опорным")
	}
	return nil
}
func startCycle(in PreviewRequest) (int64, error) {
	if in.StartCycle != nil {
		return *in.StartCycle, nil
	}
	if in.Anchor.CycleIndex == math.MaxInt64 {
		return 0, failure("recurrence_limit", "Номер следующего цикла выходит за допустимый диапазон")
	}
	return in.Anchor.CycleIndex + 1, nil
}

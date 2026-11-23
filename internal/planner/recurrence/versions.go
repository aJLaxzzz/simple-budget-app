package recurrence

import "sort"

// VersionWindow describes the scope of NEW calculations. Stored occurrences keep
// their original version and history even if a later rule supersedes its tail.
// The origin (Anchor) and typed Rule can be stored together in rule_json.
// FirstCycle uses the series' global counter, never a number derived from a date.
type VersionWindow struct {
	SeriesID       string `json:"series_id"`
	RuleVersionID  string `json:"rule_version_id"`
	EffectiveFrom  Date   `json:"effective_from"`
	EffectiveUntil *Date  `json:"effective_until,omitempty"` // exclusive, base date
	FirstCycle     int64  `json:"first_cycle"`
	UntilCycle     *int64 `json:"until_cycle,omitempty"` // exclusive
}

// ResolveVersionWindows derives an open tail's end from the next immutable
// version, without updating the earlier row. Explicit overlapping scopes are
// rejected. Gaps are allowed; this function never invents cycles to fill them.
func ResolveVersionWindows(input []VersionWindow) ([]VersionWindow, error) {
	result := append([]VersionWindow(nil), input...)
	seen := map[string]bool{}
	for i := range result {
		v := &result[i]
		if v.SeriesID == "" || v.RuleVersionID == "" || seen[v.RuleVersionID] || (i > 0 && v.SeriesID != result[0].SeriesID) || v.FirstCycle < 0 {
			return nil, failure("invalid_recurrence_versions", "Нужны уникальные версии одной серии с неотрицательными номерами циклов")
		}
		seen[v.RuleVersionID] = true
		if _, e := parse(v.EffectiveFrom); e != nil {
			return nil, e
		}
		if v.EffectiveUntil != nil {
			end := *v.EffectiveUntil
			v.EffectiveUntil = &end
			if _, e := parse(end); e != nil {
				return nil, e
			}
			if end <= v.EffectiveFrom {
				return nil, failure("invalid_recurrence_versions", "Конец области версии должен быть позже начала")
			}
		}
		if v.UntilCycle != nil {
			end := *v.UntilCycle
			v.UntilCycle = &end
			if end <= v.FirstCycle {
				return nil, failure("invalid_recurrence_versions", "Конец диапазона циклов должен быть больше начала")
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].EffectiveFrom < result[j].EffectiveFrom })
	for i := 0; i+1 < len(result); i++ {
		current, next := &result[i], result[i+1]
		if current.EffectiveFrom == next.EffectiveFrom || current.FirstCycle >= next.FirstCycle {
			return nil, failure("invalid_recurrence_versions", "Начальные даты и номера циклов версий должны строго возрастать")
		}
		if current.EffectiveUntil != nil && *current.EffectiveUntil > next.EffectiveFrom {
			return nil, failure("invalid_recurrence_versions", "Области базовых дат версий пересекаются")
		}
		if current.UntilCycle != nil && *current.UntilCycle > next.FirstCycle {
			return nil, failure("invalid_recurrence_versions", "Диапазоны циклов версий пересекаются")
		}
		if current.EffectiveUntil == nil {
			end := next.EffectiveFrom
			current.EffectiveUntil = &end
		}
		if current.UntilCycle == nil {
			end := next.FirstCycle
			current.UntilCycle = &end
		}
	}
	return result, nil
}
func (v VersionWindow) Contains(candidate Candidate) bool {
	return candidate.CycleIndex >= v.FirstCycle && (v.UntilCycle == nil || candidate.CycleIndex < *v.UntilCycle) && candidate.BaseDate >= v.EffectiveFrom && (v.EffectiveUntil == nil || candidate.BaseDate < *v.EffectiveUntil)
}

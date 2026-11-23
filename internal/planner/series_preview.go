package planner

import (
	"budget/internal/planner/recurrence"
	"database/sql"
	"net/http"
)

type SeriesCreationPreview struct {
	Rule           recurrence.Rule `json:"rule"`
	FirstDate      string          `json:"first_date"`
	ExistingTaskID string          `json:"existing_task_id,omitempty"`
	Count          int             `json:"count"`
}

// Uses the materializer's cycle-zero contract and the same calculator. It never
// invents completion anchors or creates objects during preview.
func seriesCreationPreviewRoute(s *Service, w http.ResponseWriter, r *http.Request) (int, any, error) {
	if r.Method != "POST" {
		return 0, nil, methodError(w, "POST")
	}
	var in SeriesCreationPreview
	if e := strictPlannerInput(w, r, &in, "rule first_date existing_task_id count"); e != nil {
		return 0, nil, e
	}
	if in.Count < 1 || in.Count > 10 {
		return 0, nil, invalid("Предпросмотр: от 1 до 10 выполнений")
	}
	if e := managementDate(in.FirstDate); e != nil {
		return 0, nil, e
	}
	rule, e := recurrence.NormalizeRule(in.Rule)
	if e != nil {
		return 0, nil, recurrenceAPIError(e)
	}
	result := recurrence.PreviewResult{Rule: rule, RequestedCount: in.Count, Items: []recurrence.Candidate{}, Status: "ready"}
	e = s.store.Transaction(r.Context(), func(tx *sql.Tx) error {
		v := SeriesDTO{Rule: StoredTaskRule{Rule: rule, FirstDate: recurrence.Date(in.FirstDate), Anchor: recurrence.Anchor{Date: recurrence.Date(in.FirstDate), Kind: recurrence.ScheduleAnchor}}, AnchorKind: recurrence.CompletedAnchor}
		if in.ExistingTaskID != "" {
			t, e := getTask(tx, in.ExistingTaskID)
			if e != nil {
				return e
			}
			if t.Status == "completed" {
				v.AnchorDate = t.CompletedDate
			}
		}
		calendar := &recurrenceCalendarSnapshot{tx: tx, years: map[int]productionYearSnapshot{}}
		first, _, e := calculateStoredCycle(r.Context(), tx, v, 0, calendar)
		if e != nil {
			return e
		}
		result.Items = append(result.Items, first)
		if rule.Mode == recurrence.Completion && v.AnchorDate == nil {
			result.Status = "awaiting_completion"
			result.Reason = "Первое выполнение запланировано. Дата следующего появится после подтверждённого фактического завершения."
			return nil
		}
		if in.Count > 1 {
			anchor := v.Rule.Anchor
			if rule.Mode == recurrence.Completion {
				anchor = recurrence.Anchor{Date: recurrence.Date(*v.AnchorDate), Kind: recurrence.CompletedAnchor}
			}
			more, e := recurrence.Preview(r.Context(), recurrence.PreviewRequest{Rule: rule, Anchor: anchor, Count: in.Count - 1}, calendar)
			if e != nil {
				return e
			}
			result.Items = append(result.Items, more.Items...)
			result.Status = more.Status
			result.Reason = more.Reason
		}
		for _, year := range calendar.years {
			result.Calendars = append(result.Calendars, year.info)
		}
		sortCalendars(result.Calendars)
		return nil
	})
	return 200, result, recurrenceAPIError(e)
}

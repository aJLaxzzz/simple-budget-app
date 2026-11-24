package planner

import (
	"net/http"
	"strconv"
	"strings"
)

func seriesManagementRoute(s *Service, w http.ResponseWriter, r *http.Request, id, action string) (int, any, error) {
	if r.Method != "POST" {
		return 0, nil, methodError(w, "POST")
	}
	if action != "preview" && action != "change-rule" && action != "change-template" && action != "pause" && action != "resume" && action != "archive" && action != "reanchor" {
		return 0, nil, notFound("Маршрут", action)
	}
	var in SeriesCommand
	if e := strictPlannerInput(w, r, &in, "request_id expected_revision preview_token action rule from_cycle first_date start_date end_date pause_id anchor_date reason template_version_id tasks_date_decision removed_tasks_decision"); e != nil {
		return 0, nil, e
	}
	if action == "preview" {
		p, e := s.PreviewSeriesChange(r.Context(), id, in)
		return 200, p, e
	}
	normalized := strings.ReplaceAll(action, "-", "_")
	if in.Action != "" && in.Action != normalized {
		return 0, nil, badJSON("action не соответствует маршруту")
	}
	in.Action = normalized
	result, e := s.ApplySeriesCommand(r.Context(), id, in)
	return 200, result, e
}
func occurrenceManagementRoute(s *Service, w http.ResponseWriter, r *http.Request, id, action string) (int, any, error) {
	if r.Method != "POST" {
		return 0, nil, methodError(w, "POST")
	}
	switch action {
	case "reschedule", "restore", "complete", "cancel", "skip", "review", "undo-complete":
	default:
		return 0, nil, notFound("Маршрут", action)
	}
	var in OccurrenceCommand
	if e := strictPlannerInput(w, r, &in, "completion_request_id request_id expected_revision expected_task_revision expected_series_revision planned_date scheduled_time completed_date reason dynamic_decision branch_decision sessions_decision session_revisions children_decision related_revisions expected_project_revision tasks_decision tasks_date_decision"); e != nil {
		return 0, nil, e
	}
	result, e := s.OccurrenceCommand(r.Context(), id, action, in)
	return 200, result, e
}
func incidentsRoute(s *Service, w http.ResponseWriter, r *http.Request) (int, any, error) {
	if r.Method != "GET" {
		return 0, nil, methodError(w, "GET")
	}
	q := r.URL.Query()
	limit, offset := 50, 0
	for key, dst := range map[string]*int{"limit": &limit, "offset": &offset} {
		if q.Get(key) != "" {
			n, e := strconv.Atoi(q.Get(key))
			if e != nil {
				return 0, nil, invalid("Некорректная пагинация")
			}
			*dst = n
		}
	}
	items, e := s.Incidents(r.Context(), q.Get("series_id"), q.Get("kind"), q.Get("disposition"), limit, offset)
	return 200, map[string]any{"items": items, "limit": limit, "offset": offset}, e
}

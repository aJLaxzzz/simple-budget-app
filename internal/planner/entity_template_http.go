package planner

import (
	"net/http"
	"strconv"
)

func taskTemplatesRoute(s *Service, w http.ResponseWriter, r *http.Request, id, action string) (int, any, error) {
	if r.Method == "GET" && action == "" {
		if id != "" {
			v, e := s.TaskTemplate(r.Context(), id)
			return 200, v, e
		}
		offset := 0
		if raw := r.URL.Query().Get("offset"); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 0 {
				return 0, nil, invalid("Некорректная пагинация")
			}
			offset = n
		}
		items, e := s.TaskTemplates(r.Context(), r.URL.Query().Get("archived") == "true", offset)
		return 200, map[string]any{"items": items, "offset": offset}, e
	}
	if r.Method != "POST" {
		return 0, nil, methodError(w, "GET, POST")
	}
	if id == "" && action == "" {
		action = "create"
	} else if action != "versions" && action != "archive" && action != "instantiate" {
		return 0, nil, notFound("Маршрут", action)
	}
	var in TaskTemplateInput
	if e := strictPlannerInput(w, r, &in, "request_id expected_revision source_task_id snapshot project_id planned_date"); e != nil {
		return 0, nil, e
	}
	v, e := s.TaskTemplateCommand(r.Context(), id, action, in)
	status := 201
	if action == "archive" {
		status = 200
	}
	return status, v, e
}

func projectSourceRoute(s *Service, w http.ResponseWriter, r *http.Request, id, action string) (int, any, error) {
	if r.Method != "POST" {
		return 0, nil, methodError(w, "POST")
	}
	var in ProjectSourceInput
	if e := strictPlannerInput(w, r, &in, "request_id expected_revision preview_token snapshot anchor_date first_date rule end_date independent_decision project_mode"); e != nil {
		return 0, nil, e
	}
	if action == "template-preview" {
		v, e := s.ProjectTemplatePreview(r.Context(), id, in)
		return 200, v, e
	}
	v, e := s.SaveProjectTemplate(r.Context(), id, in)
	return 201, v, e
}

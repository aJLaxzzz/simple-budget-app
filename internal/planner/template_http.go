package planner

import (
	"net/http"
	"strconv"
)

func templateRoute(s *Service, w http.ResponseWriter, r *http.Request, id, action string) (int, any, error) {
	if action == "projects" {
		return templateProjectsRoute(s, w, r, id)
	}
	if r.Method == "GET" && action == "" {
		if id != "" {
			v, e := s.Template(r.Context(), id, r.URL.Query().Get("version_id"))
			return 200, v, e
		}
		limit, offset := 50, 0
		for key, dst := range map[string]*int{"limit": &limit, "offset": &offset} {
			if value := r.URL.Query().Get(key); value != "" {
				n, e := strconv.Atoi(value)
				if e != nil {
					return 0, nil, invalid("Некорректная пагинация")
				}
				*dst = n
			}
		}
		v, e := s.Templates(r.Context(), r.URL.Query().Get("archived") == "true", limit, offset)
		return 200, map[string]any{"items": v, "limit": limit, "offset": offset}, e
	}
	if r.Method != "POST" {
		return 0, nil, methodError(w, "GET, POST")
	}
	if id == "" {
		if action != "" {
			return 0, nil, notFound("Маршрут", action)
		}
		action = "create"
	} else if action != "versions" && action != "archive" && action != "instantiate" && action != "preview" {
		return 0, nil, notFound("Маршрут", action)
	}
	var in TemplateInput
	if e := strictPlannerInput(w, r, &in, "request_id expected_revision snapshot template_version_id start_date rule count"); e != nil {
		return 0, nil, e
	}
	if action == "preview" {
		v, e := s.TemplatePreview(r.Context(), id, in)
		return 200, map[string]any{"items": v}, e
	}
	v, e := s.TemplateCommand(r.Context(), id, action, in)
	status := 200
	if action == "create" || action == "versions" || action == "instantiate" {
		status = 201
	}
	return status, v, e
}

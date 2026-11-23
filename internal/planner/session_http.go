package planner

import "net/http"

func routeSessions(s *Service, w http.ResponseWriter, r *http.Request, id, action string) (int, any, error) {
	ctx := r.Context()
	if action != "" {
		if !fields("complete cancel restore reschedule")[action] {
			return 0, nil, notFound("Действие", action)
		}
		if r.Method != "POST" {
			return 0, nil, methodError(w, "POST")
		}
	}
	if r.Method == "GET" && action == "" {
		if id != "" {
			v, e := s.Session(ctx, id)
			return 200, v, e
		}
		items, e := s.Sessions(ctx, r.URL.Query().Get("task_id"), r.URL.Query().Get("status"))
		return 200, map[string]any{"items": items}, e
	}
	create := id == "" && r.Method == "POST"
	if !create && !(id != "" && ((action == "" && r.Method == "PATCH") || (action != "" && r.Method == "POST"))) {
		allow := "GET, PATCH"
		if id == "" {
			allow = "GET, POST"
		}
		return 0, nil, methodError(w, allow)
	}
	allowed := fields("date start_time end_time timezone")
	if create {
		allowed["task_id"] = true
		allowed["request_id"] = true
	} else {
		allowed["expected_revision"] = true
	}
	if action != "" {
		allowed = fields("expected_revision")
		if action == "complete" {
			allowed["completed_date"] = true
		}
		if action == "reschedule" {
			allowed = fields("expected_revision date start_time end_time")
		}
	}
	var in SessionInput
	if e := decodeInput(w, r, &in, allowed); e != nil {
		return 0, nil, e
	}
	if create {
		v, e := s.CreateSession(ctx, in)
		if e == nil {
			w.Header().Set("Location", "/api/planner/sessions/"+v.ID)
		}
		return 201, v, e
	}
	v, e := s.ChangeSession(ctx, id, action, in)
	return 200, v, e
}

package planner

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"modernc.org/sqlite"
)

func WriteAPIError(w http.ResponseWriter, err error) {
	var api *APIError
	if !errors.As(err, &api) {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code()&255 == 19 {
			api = &APIError{Status: 422, Code: "constraint_violation", Message: "Изменение нарушает целостность данных"}
		} else {
			log.Printf("planner: %v", err)
			api = &APIError{Status: 500, Code: "internal_error", Message: "Не удалось выполнить операцию; изменения не применены"}
		}
	}
	writeJSON(w, api.Status, map[string]any{"error": api})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func badJSON(message string) error {
	return &APIError{Status: 400, Code: "invalid_request", Message: message}
}
func decodeInput(w http.ResponseWriter, r *http.Request, target any, allowed map[string]bool) error {
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	decoder := json.NewDecoder(r.Body)
	var raw map[string]json.RawMessage
	if e := decoder.Decode(&raw); e != nil || raw == nil {
		return badJSON("Ожидается JSON-объект до 128 KiB")
	}
	if e := decoder.Decode(new(any)); e != io.EOF {
		return badJSON("После JSON-объекта обнаружены лишние данные")
	}
	for key := range raw {
		if !allowed[key] {
			return badJSON("Поле не разрешено: " + key)
		}
	}
	b, _ := json.Marshal(raw)
	if e := json.Unmarshal(b, target); e != nil {
		return badJSON("Некорректный тип поля")
	}
	return nil
}
func fields(names string) map[string]bool {
	m := map[string]bool{}
	for _, n := range strings.Fields(names) {
		m[n] = true
	}
	return m
}
func NewHandler(s *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/planner/icons") {
			s.iconsHTTP(w, r)
			return
		}
		status, result, err := route(s, w, r)
		if err != nil {
			WriteAPIError(w, err)
			return
		}
		writeJSON(w, status, result)
	})
}
func methodError(w http.ResponseWriter, allow string) error {
	w.Header().Set("Allow", allow)
	return &APIError{Status: 405, Code: "method_not_allowed", Message: "Метод не поддерживается"}
}
func route(s *Service, w http.ResponseWriter, r *http.Request) (int, any, error) {
	path := strings.TrimPrefix(r.URL.Path, "/api/planner/")
	parts := strings.Split(path, "/")
	if len(parts) > 3 || len(parts) == 0 {
		return 0, nil, notFound("Маршрут", path)
	}
	ctx := r.Context()
	kind := parts[0]
	id, action := "", ""
	if len(parts) > 1 {
		id = parts[1]
		if id == "" {
			return 0, nil, notFound("Маршрут", path)
		}
	}
	if len(parts) > 2 {
		action = parts[2]
	}
	if id != "" && (action == "delete-preview" || action == "delete" || action == "unarchive") {
		return lifecycleRoute(s, w, r, kind, id, action)
	}
	if kind == "series" && (action == "project-preview" || action == "project-apply") {
		return projectModeRoute(s, w, r, id, action)
	}
	if kind == "projects" && (action == "task-change-preview" || action == "task-change-apply") {
		return projectTaskScopeRoute(s, w, r, id, action)
	}
	if kind == "projects" && action == "order" {
		return projectOrderRoute(s, w, r, id)
	}
	if kind == "series" && (action == "dates-preview" || action == "dates-apply") {
		return projectDateRepairRoute(s, w, r, id, action)
	}
	if kind == "series" && (action == "composition-preview" || action == "composition-apply") {
		return projectCompositionRoute(s, w, r, id, action)
	}
	if kind == "projects" && action == "close-current" {
		return projectSourceActionRoute(s, w, r, id)
	}
	if kind == "project-drafts" && len(parts) == 2 {
		return projectDraftRoute(s, w, r, id)
	}
	if kind == "batch-recurrence" && len(parts) == 2 {
		return batchRepeatRoute(s, w, r, id)
	}
	if kind == "task-moves" && len(parts) == 2 {
		return taskMoveRoute(s, w, r, id)
	}
	if kind == "occurrences" && len(parts) == 2 {
		return occurrenceContextRoute(s, w, r, id)
	}
	if kind == "attention" && len(parts) == 2 {
		return attentionSettingsRoute(s, w, r, id)
	}
	if kind == "attention" && len(parts) == 1 {
		return attentionRoute(s, w, r)
	}
	if kind == "production-calendar" {
		return productionCalendarRoute(s, w, r, id, action)
	}
	if kind == "task-templates" {
		return taskTemplatesRoute(s, w, r, id, action)
	}
	if kind == "projects" && id != "" && (action == "template-preview" || action == "save-template") {
		return projectSourceRoute(s, w, r, id, action)
	}
	if kind == "templates" {
		return templateRoute(s, w, r, id, action)
	}
	if kind == "series" && (action == "disable" || action == "disable-preview") {
		return disableRoute(s, w, r, id, action)
	}
	if kind == "series" && action == "ensure-upcoming" {
		if r.Method != "POST" {
			return 0, nil, methodError(w, "POST")
		}
		v, e := s.EnsureUpcoming(ctx, id)
		return 200, v, e
	}
	if kind == "series" && (action == "appearance" || action == "appearance-preview") {
		return seriesAppearanceRoute(s, w, r, id, action)
	}
	if kind == "series" && len(parts) == 3 {
		return seriesManagementRoute(s, w, r, id, action)
	}
	if kind == "occurrences" && len(parts) == 3 {
		return occurrenceManagementRoute(s, w, r, id, action)
	}
	if kind == "incidents" && len(parts) == 1 {
		return incidentsRoute(s, w, r)
	}
	if kind == "series" && len(parts) <= 2 {
		return seriesRoute(s, w, r, id)
	}
	if kind == "materialize" && len(parts) == 1 {
		return materializeRoute(s, w, r)
	}
	if kind == "recurrence" && id == "preview" && len(parts) == 2 {
		return recurrencePreviewRoute(s, w, r)
	}
	if kind == "calendar" && len(parts) == 1 {
		if r.Method != "GET" {
			return 0, nil, methodError(w, "GET")
		}
		v, e := s.CalendarProjection(ctx, r.URL.Query().Get("from"), r.URL.Query().Get("to"))
		return 200, v, e
	}
	if kind == "tasks" && id != "" && action == "context" {
		if r.Method != "GET" {
			return 0, nil, methodError(w, "GET")
		}
		v, e := s.TaskContext(ctx, id)
		return 200, v, e
	}
	if kind == "history" && len(parts) == 1 {
		if r.Method != "GET" {
			return 0, nil, methodError(w, "GET")
		}
		limit, offset := 50, 0
		var e error
		if q := r.URL.Query().Get("limit"); q != "" {
			limit, e = strconv.Atoi(q)
			if e != nil {
				return 0, nil, badJSON("Некорректный limit")
			}
		}
		if q := r.URL.Query().Get("offset"); q != "" {
			offset, e = strconv.Atoi(q)
			if e != nil {
				return 0, nil, badJSON("Некорректный offset")
			}
		}
		items, e := s.History(ctx, r.URL.Query().Get("object_type"), r.URL.Query().Get("object_id"), limit, offset)
		return 200, map[string]any{"items": items, "limit": limit, "offset": offset}, e
	}
	if kind == "sessions" {
		return routeSessions(s, w, r, id, action)
	}
	if kind != "projects" && kind != "tasks" {
		return 0, nil, notFound("Маршрут", path)
	}
	if action != "" {
		allowed := fields("complete cancel archive")
		if kind == "tasks" {
			allowed = fields("complete cancel restore reschedule")
		}
		if !allowed[action] {
			return 0, nil, notFound("Действие", action)
		}
		if r.Method != "POST" {
			return 0, nil, methodError(w, "POST")
		}
	}
	if r.Method == "GET" && action == "" {
		if kind == "projects" {
			if id != "" {
				p, e := s.Project(ctx, id)
				return 200, p, e
			}
			archived := r.URL.Query().Get("archived")
			if archived != "" && archived != "true" && archived != "false" {
				return 0, nil, badJSON("archived должен быть true или false")
			}
			items, e := s.Projects(ctx, r.URL.Query().Get("status"), archived == "true")
			return 200, map[string]any{"items": items}, e
		}
		if id != "" {
			t, e := s.Task(ctx, id)
			return 200, t, e
		}
		var project *string
		if r.URL.Query().Has("project_id") {
			p := r.URL.Query().Get("project_id")
			project = &p
		}
		items, e := s.Tasks(ctx, r.URL.Query().Get("status"), project)
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
	allowed := fields("title description status start_date due_date color_id icon_id")
	if kind == "tasks" {
		allowed = fields("title description status priority project_id parent_task_id planned_date scheduled_time due_date color_id icon_id")
	}
	if create {
		allowed["request_id"] = true
	}
	if !create {
		allowed["expected_revision"] = true
		if kind == "tasks" {
			allowed["related_revisions"] = true
			allowed["completed_date"] = true
			allowed["children_decision"] = true
			allowed["sessions_decision"] = true
			allowed["session_revisions"] = true
		}
	}
	if action != "" {
		allowed = fields("expected_revision request_id")
		if kind == "tasks" {
			allowed["related_revisions"] = true
			if action == "complete" {
				allowed["completed_date"] = true
				allowed["children_decision"] = true
				allowed["sessions_decision"] = true
				allowed["session_revisions"] = true
			}
			if action == "cancel" {
				allowed["sessions_decision"] = true
				allowed["session_revisions"] = true
			}
			if action == "reschedule" {
				allowed["planned_date"] = true
				allowed["scheduled_time"] = true
			}
		}
	}
	code := 200
	if create {
		code = 201
	}
	if kind == "projects" {
		var in ProjectInput
		if e := decodeInput(w, r, &in, allowed); e != nil {
			return 0, nil, e
		}
		if create {
			p, e := s.CreateProject(ctx, in)
			if e == nil {
				w.Header().Set("Location", "/api/planner/projects/"+p.ID)
			}
			return code, p, e
		}
		p, e := s.ChangeProject(ctx, id, action, in)
		return code, p, e
	}
	var in TaskInput
	if e := decodeInput(w, r, &in, allowed); e != nil {
		return 0, nil, e
	}
	if create {
		t, e := s.CreateTask(ctx, in)
		if e == nil {
			w.Header().Set("Location", "/api/planner/tasks/"+t.ID)
		}
		return code, t, e
	}
	t, e := s.ChangeTask(ctx, id, action, in)
	return code, t, e
}

package planner

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func apiCall(t *testing.T, h http.Handler, method, path string, body any, status int) *httptest.ResponseRecorder {
	t.Helper()
	raw := []byte{}
	if body != nil {
		var e error
		raw, e = json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, "/api/planner"+path, strings.NewReader(string(raw))))
	if w.Code != status {
		t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	return w
}
func value[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var result T
	if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	return result
}
func project(t *testing.T, h http.Handler, title string) ProjectDTO {
	return value[ProjectDTO](t, apiCall(t, h, "POST", "/projects", map[string]any{"title": title}, 201))
}
func task(t *testing.T, h http.Handler, input map[string]any) TaskDTO {
	return value[TaskDTO](t, apiCall(t, h, "POST", "/tasks", input, 201))
}
func revisions(t TaskDTO, children ...TaskDTO) map[string]any {
	m := map[string]int64{}
	for _, c := range children {
		m[c.ID] = c.Revision
	}
	return map[string]any{"expected_revision": t.Revision, "related_revisions": m}
}
func history(t *testing.T, h http.Handler, kind, id string) []HistoryDTO {
	return value[struct {
		Items []HistoryDTO `json:"items"`
	}](t, apiCall(t, h, "GET", "/history?object_type="+kind+"&object_id="+id, nil, 200)).Items
}
func TestAPIProjectsTasksAndHierarchy(t *testing.T) {
	h := NewHandler(NewService(testStore(t)))
	p := project(t, h, "P")
	q := project(t, h, "Q")
	free := task(t, h, map[string]any{"title": "Inbox"})
	if free.ProjectID != nil || free.Revision != 1 {
		t.Fatal(free)
	}
	root := task(t, h, map[string]any{"title": "Root", "project_id": p.ID})
	child := task(t, h, map[string]any{"title": "Child", "project_id": p.ID, "parent_task_id": root.ID})
	apiCall(t, h, "POST", "/tasks", map[string]any{"title": "Third", "project_id": p.ID, "parent_task_id": child.ID}, 422)
	apiCall(t, h, "POST", "/tasks", map[string]any{"title": "Wrong project", "project_id": q.ID, "parent_task_id": root.ID}, 422)
	apiCall(t, h, "PATCH", "/tasks/"+root.ID, map[string]any{"expected_revision": 1, "parent_task_id": child.ID}, 422)
	apiCall(t, h, "PATCH", "/tasks/"+root.ID, map[string]any{"expected_revision": 1, "parent_task_id": root.ID}, 422)
	input := revisions(root, child)
	input["project_id"] = q.ID
	moved := value[TaskDTO](t, apiCall(t, h, "PATCH", "/tasks/"+root.ID, input, 200))
	movedChild := value[TaskDTO](t, apiCall(t, h, "GET", "/tasks/"+child.ID, nil, 200))
	if *moved.ProjectID != q.ID || *movedChild.ProjectID != q.ID || moved.Revision != 2 || movedChild.Revision != 2 {
		t.Fatal("move", moved, movedChild)
	}
	a, b := history(t, h, "task", root.ID), history(t, h, "task", child.ID)
	if a[0].OperationID != b[0].OperationID || a[0].Action != "moved_project" || b[0].Action != "moved_project" {
		t.Fatal("operation history")
	}
	details := value[ProjectDTO](t, apiCall(t, h, "GET", "/projects/"+q.ID, nil, 200))
	if len(details.Tasks) != 2 || details.Progress.Total != 1 {
		t.Fatal(details)
	}
	input = revisions(moved, movedChild)
	input["project_id"] = nil
	apiCall(t, h, "PATCH", "/tasks/"+root.ID, input, 422)
}
func TestCompletionChoicesActualDateRestoreAndProgress(t *testing.T) {
	svc := NewService(testStore(t))
	svc.now = func() time.Time { return time.Date(2026, 11, 16, 12, 34, 56, 123, time.UTC) }
	h := NewHandler(svc)
	p := project(t, h, "P")
	root := task(t, h, map[string]any{"title": "Root", "project_id": p.ID})
	child := task(t, h, map[string]any{"title": "Child", "project_id": p.ID, "parent_task_id": root.ID})
	cancelled := task(t, h, map[string]any{"title": "Cancelled", "project_id": p.ID})
	apiCall(t, h, "POST", "/tasks/"+cancelled.ID+"/cancel", revisions(cancelled), 200)
	apiCall(t, h, "POST", "/tasks/"+root.ID+"/complete", revisions(root, child), 422)
	input := revisions(root, child)
	input["completed_date"] = "2026-11-10"
	apiCall(t, h, "POST", "/tasks/"+root.ID+"/complete", input, 422)
	input["children_decision"] = "keep_open"
	input["completed_date"] = "2026-11-17"
	apiCall(t, h, "POST", "/tasks/"+root.ID+"/complete", input, 422)
	input["completed_date"] = "2026-11-10"
	root = value[TaskDTO](t, apiCall(t, h, "POST", "/tasks/"+root.ID+"/complete", input, 200))
	if !root.CompletedWithOpenChildren || root.OpenChildren != 1 || *root.CompletedDate != "2026-11-10" || *root.CompletedAt != svc.now().UTC().Format(time.RFC3339Nano) {
		t.Fatal(root)
	}
	details := value[ProjectDTO](t, apiCall(t, h, "GET", "/projects/"+p.ID, nil, 200))
	if details.Progress.Total != 1 || details.Progress.Percent != 0 || details.Progress.Cancelled != 1 {
		t.Fatal(details.Progress)
	}
	root = value[TaskDTO](t, apiCall(t, h, "POST", "/tasks/"+root.ID+"/restore", revisions(root), 200))
	if root.CompletedDate != nil || root.CompletedAt != nil {
		t.Fatal(root)
	}
	input = revisions(root, child)
	input["completed_date"] = "2026-11-11"
	input["children_decision"] = "complete"
	apiCall(t, h, "POST", "/tasks/"+root.ID+"/complete", input, 200)
	details = value[ProjectDTO](t, apiCall(t, h, "GET", "/projects/"+p.ID, nil, 200))
	if details.Progress.Percent != 100 || details.Progress.Completed != 1 || details.Progress.Total != 1 {
		t.Fatal(details.Progress)
	}
	events := history(t, h, "task", root.ID)
	if len(events) != 4 || events[0].Action != "completed" || events[1].Action != "restored" || events[2].Action != "completed" {
		t.Fatal(events)
	}
	if !strings.Contains(string(events[2].Changes), "2026-11-10") || !strings.Contains(string(events[1].Changes), "2026-11-10") {
		t.Fatal("restoration lost previous completion date")
	}
	if events[0].OperationID != history(t, h, "task", child.ID)[0].OperationID {
		t.Fatal("completion operation ID")
	}
	page := value[struct {
		Items []HistoryDTO `json:"items"`
	}](t, apiCall(t, h, "GET", "/history?object_type=task&object_id="+root.ID+"&limit=1&offset=1", nil, 200))
	if len(page.Items) != 1 || page.Items[0].Action != "restored" {
		t.Fatal(page)
	}
}
func TestChildCompletionDoesNotCloseParent(t *testing.T) {
	h := NewHandler(NewService(testStore(t)))
	p := project(t, h, "P")
	root := task(t, h, map[string]any{"title": "Root", "project_id": p.ID})
	child := task(t, h, map[string]any{"title": "Child", "project_id": p.ID, "parent_task_id": root.ID})
	input := revisions(child)
	input["completed_date"] = "2020-01-01"
	apiCall(t, h, "POST", "/tasks/"+child.ID+"/complete", input, 200)
	root = value[TaskDTO](t, apiCall(t, h, "GET", "/tasks/"+root.ID, nil, 200))
	if root.Status != "planned" || root.Revision != 1 {
		t.Fatal(root)
	}
}
func TestCompoundConflictAndHistoryFailureRollBack(t *testing.T) {
	store := testStore(t)
	h := NewHandler(NewService(store))
	p := project(t, h, "P")
	q := project(t, h, "Q")
	root := task(t, h, map[string]any{"title": "Root", "project_id": p.ID})
	child := task(t, h, map[string]any{"title": "Child", "project_id": p.ID, "parent_task_id": root.ID})
	input := revisions(root, child)
	input["project_id"] = q.ID
	apiCall(t, h, "PATCH", "/tasks/"+child.ID, map[string]any{"expected_revision": 1, "title": "Updated"}, 200)
	conflict := apiCall(t, h, "PATCH", "/tasks/"+root.ID, input, 409)
	if !strings.Contains(conflict.Body.String(), child.ID) || !strings.Contains(conflict.Body.String(), `"revision":2`) {
		t.Fatal(conflict.Body.String())
	}
	rootNow := value[TaskDTO](t, apiCall(t, h, "GET", "/tasks/"+root.ID, nil, 200))
	if rootNow.Revision != 1 || *rootNow.ProjectID != p.ID {
		t.Fatal(rootNow)
	}
	child = value[TaskDTO](t, apiCall(t, h, "GET", "/tasks/"+child.ID, nil, 200))
	input = revisions(root, child)
	input["project_id"] = q.ID
	exec(t, store, `CREATE TRIGGER fail_history BEFORE INSERT ON history_events WHEN NEW.task_id='`+child.ID+`' BEGIN SELECT RAISE(ABORT,'simulated history failure'); END;`)
	apiCall(t, h, "PATCH", "/tasks/"+root.ID, input, 422)
	rootNow = value[TaskDTO](t, apiCall(t, h, "GET", "/tasks/"+root.ID, nil, 200))
	childNow := value[TaskDTO](t, apiCall(t, h, "GET", "/tasks/"+child.ID, nil, 200))
	if rootNow.Revision != 1 || childNow.Revision != 2 || *childNow.ProjectID != p.ID || len(history(t, h, "task", root.ID)) != 1 {
		t.Fatal("partial commit")
	}
}
func TestParallelAPIRevisionConflict(t *testing.T) {
	h := NewHandler(NewService(testStore(t)))
	one := task(t, h, map[string]any{"title": "Original"})
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("PATCH", "/api/planner/tasks/"+one.ID, strings.NewReader(fmt.Sprintf(`{"expected_revision":1,"title":"Edit %d"}`, i))))
			codes <- w.Code
		}(i)
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatal(counts)
	}
	if len(history(t, h, "task", one.ID)) != 2 {
		t.Fatal("conflict generated history")
	}
}
func TestAPIValidationAndProjectLifecycle(t *testing.T) {
	h := NewHandler(NewService(testStore(t)))
	apiCall(t, h, "POST", "/tasks", map[string]any{"title": ""}, 422)
	apiCall(t, h, "POST", "/tasks", map[string]any{"title": "x", "revision": 1}, 400)
	apiCall(t, h, "POST", "/tasks", map[string]any{"title": "x", "created_at": "fake"}, 400)
	apiCall(t, h, "POST", "/tasks", map[string]any{"title": "x", "planned_date": "2026-02-30"}, 422)
	apiCall(t, h, "POST", "/tasks", map[string]any{"title": "x", "scheduled_time": "12:00"}, 422)
	apiCall(t, h, "POST", "/tasks", map[string]any{"title": "x", "project_id": "missing"}, 404)
	apiCall(t, h, "GET", "/tasks/missing", nil, 404)
	apiCall(t, h, "DELETE", "/tasks/missing", nil, 405)
	apiCall(t, h, "GET", "/history?object_type=task&object_id=x&limit=101", nil, 422)
	p := project(t, h, "P")
	apiCall(t, h, "PATCH", "/projects/"+p.ID, map[string]any{"title": "No revision"}, 422)
	p = value[ProjectDTO](t, apiCall(t, h, "POST", "/projects/"+p.ID+"/complete", map[string]any{"expected_revision": 1}, 200))
	if p.Status != "completed" || p.ClosedAt == nil {
		t.Fatal(p)
	}
	apiCall(t, h, "POST", "/projects/"+p.ID+"/cancel", map[string]any{"expected_revision": p.Revision}, 422)
	apiCall(t, h, "POST", "/projects/"+p.ID+"/archive", map[string]any{"expected_revision": p.Revision}, 200)
	listed := value[struct {
		Items []ProjectDTO `json:"items"`
	}](t, apiCall(t, h, "GET", "/projects?archived=true&status=completed", nil, 200))
	if len(listed.Items) != 1 {
		t.Fatal(listed)
	}
	one := task(t, h, map[string]any{"title": "One", "due_date": "2026-11-01"})
	apiCall(t, h, "POST", "/tasks/"+one.ID+"/restore", revisions(one), 422)
	input := revisions(one)
	input["planned_date"] = "2026-11-02"
	one = value[TaskDTO](t, apiCall(t, h, "POST", "/tasks/"+one.ID+"/reschedule", input, 200))
	if *one.DueDate != "2026-11-01" {
		t.Fatal("deadline changed")
	}
	one = value[TaskDTO](t, apiCall(t, h, "POST", "/tasks/"+one.ID+"/cancel", revisions(one), 200))
	one = value[TaskDTO](t, apiCall(t, h, "POST", "/tasks/"+one.ID+"/restore", revisions(one), 200))
	if one.Status != "planned" {
		t.Fatal(one)
	}
}
func TestUpgradeV1PreservesData(t *testing.T) {
	store := testStore(t)
	// Separate v1 database demonstrates that 001 stays valid and is not rerun.
	path := store.path + "-v1"
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	if e = migrate(ctx, db, migrations[:1]); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO projects(id,title) VALUES('existing','Existing')"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	upgraded, e := Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer upgraded.Close()
	p, e := NewService(upgraded).Project(ctx, "existing")
	if e != nil || p.Title != "Existing" || p.ArchivedAt != nil {
		t.Fatal(p, e)
	}
}

package planner

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func session(t *testing.T, h http.Handler, taskID, date string) SessionDTO {
	t.Helper()
	return value[SessionDTO](t, apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": taskID, "date": date}, 201))
}
func sessionRevision(v SessionDTO) map[string]any {
	return map[string]any{"expected_revision": v.Revision}
}
func getSess(t *testing.T, h http.Handler, id string) SessionDTO {
	return value[SessionDTO](t, apiCall(t, h, "GET", "/sessions/"+id, nil, 200))
}
func TestSessionsCRUDAndTaskIsolation(t *testing.T) {
	h := NewHandler(NewService(testStore(t)))
	p := project(t, h, "P")
	root := task(t, h, map[string]any{"title": "Root", "project_id": p.ID, "planned_date": "2026-01-01", "due_date": "2026-01-31"})
	child := task(t, h, map[string]any{"title": "Child", "project_id": p.ID, "parent_task_id": root.ID})
	a := session(t, h, root.ID, "2026-01-02")
	b := session(t, h, root.ID, "2026-01-03")
	session(t, h, child.ID, "2026-01-02")
	if a.StartTime != nil || a.EndTime != nil || a.Timezone != "Europe/Moscow" || a.Status != "planned" {
		t.Fatal(a)
	}
	items := value[struct {
		Items []SessionDTO `json:"items"`
	}](t, apiCall(t, h, "GET", "/sessions?task_id="+root.ID, nil, 200))
	if len(items.Items) != 2 {
		t.Fatal(items)
	}
	input := sessionRevision(a)
	input["start_time"] = "10:00"
	input["end_time"] = "11:00"
	a = value[SessionDTO](t, apiCall(t, h, "PATCH", "/sessions/"+a.ID, input, 200))
	// Overlaps are allowed, including sessions belonging to different tasks.
	apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": child.ID, "date": "2026-01-02", "start_time": "10:00", "end_time": "11:00"}, 201)
	input = sessionRevision(a)
	input["date"] = "2026-02-01"
	a = value[SessionDTO](t, apiCall(t, h, "POST", "/sessions/"+a.ID+"/reschedule", input, 200))
	input = sessionRevision(a)
	input["start_time"] = nil
	input["end_time"] = nil
	a = value[SessionDTO](t, apiCall(t, h, "PATCH", "/sessions/"+a.ID, input, 200))
	if a.StartTime != nil {
		t.Fatal(a)
	}
	for _, v := range []SessionDTO{a, b} {
		input = sessionRevision(v)
		input["completed_date"] = "2020-01-15"
		apiCall(t, h, "POST", "/sessions/"+v.ID+"/complete", input, 200)
	}
	unchanged := value[TaskDTO](t, apiCall(t, h, "GET", "/tasks/"+root.ID, nil, 200))
	if unchanged.Revision != root.Revision || unchanged.Status != "planned" || *unchanged.PlannedDate != "2026-01-01" || *unchanged.DueDate != "2026-01-31" {
		t.Fatal("session changed task", unchanged)
	}
	events := history(t, h, "session", a.ID)
	if len(events) != 5 || events[0].Action != "completed" {
		t.Fatal(events)
	}
}
func TestSessionValidationAndTerminalStates(t *testing.T) {
	svc := NewService(testStore(t))
	svc.now = func() time.Time { return time.Date(2026, 11, 16, 22, 0, 0, 123, time.UTC) }
	h := NewHandler(svc)
	one := task(t, h, map[string]any{"title": "Task"})
	for _, fields := range []map[string]any{
		{"start_time": "10:00"}, {"end_time": "11:00"}, {"start_time": "11:00", "end_time": "10:00"}, {"start_time": "10:00", "end_time": "10:00"}, {"start_time": "24:00", "end_time": "25:00"}, {"start_time": "9:00", "end_time": "10:00"}, {"timezone": "UTC"}, {"date": "2026-02-30"}, {"date": nil},
	} {
		in := map[string]any{"task_id": one.ID, "date": "2026-11-16"}
		for k, v := range fields {
			in[k] = v
		}
		apiCall(t, h, "POST", "/sessions", in, 422)
	}
	apiCall(t, h, "POST", "/sessions", map[string]any{"date": "2026-11-16"}, 422)
	apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": "missing", "date": "2026-11-16"}, 404)
	for _, field := range []string{"status", "revision", "completed_at", "created_at", "updated_at", "history"} {
		apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": one.ID, "date": "2026-11-16", field: "forged"}, 400)
	}
	a := session(t, h, one.ID, "2026-11-16")
	apiCall(t, h, "POST", "/sessions/"+a.ID+"/restore", sessionRevision(a), 422)
	apiCall(t, h, "POST", "/sessions/"+a.ID+"/complete", sessionRevision(a), 422)
	in := sessionRevision(a)
	in["completed_date"] = "2026-11-18"
	apiCall(t, h, "POST", "/sessions/"+a.ID+"/complete", in, 422)
	in["completed_date"] = "2026-11-17" // Moscow is already November 17.
	a = value[SessionDTO](t, apiCall(t, h, "POST", "/sessions/"+a.ID+"/complete", in, 200))
	if *a.CompletedDate != "2026-11-17" || *a.CompletedAt != svc.now().UTC().Format("2006-01-02T15:04:05.000000000Z") {
		t.Fatal(a)
	}
	for _, action := range []string{"cancel", "complete", "reschedule"} {
		in = sessionRevision(a)
		if action == "reschedule" {
			in["date"] = "2026-11-19"
		}
		apiCall(t, h, "POST", "/sessions/"+a.ID+"/"+action, in, 422)
	}
	in = sessionRevision(a)
	in["date"] = "2026-11-19"
	apiCall(t, h, "PATCH", "/sessions/"+a.ID, in, 422)
	a = value[SessionDTO](t, apiCall(t, h, "POST", "/sessions/"+a.ID+"/restore", sessionRevision(a), 200))
	if a.CompletedDate != nil || a.CompletedAt != nil {
		t.Fatal(a)
	}
	a = value[SessionDTO](t, apiCall(t, h, "POST", "/sessions/"+a.ID+"/cancel", sessionRevision(a), 200))
	a = value[SessionDTO](t, apiCall(t, h, "POST", "/sessions/"+a.ID+"/restore", sessionRevision(a), 200))
	if len(history(t, h, "session", a.ID)) != 5 {
		t.Fatal("lost history")
	}
	apiCall(t, h, "DELETE", "/sessions/"+a.ID, nil, 405)
	apiCall(t, h, "GET", "/sessions/missing", nil, 404)
	apiCall(t, h, "PATCH", "/sessions/"+a.ID, map[string]any{"expected_revision": a.Revision, "task_id": "different"}, 400)
}
func TestClosingTasksHandlesAllSessions(t *testing.T) {
	for _, action := range []string{"complete", "cancel", "patch_complete", "patch_cancel"} {
		t.Run(action, func(t *testing.T) {
			h := NewHandler(NewService(testStore(t)))
			one := task(t, h, map[string]any{"title": "Task"})
			past := session(t, h, one.ID, "2020-01-01")
			future := session(t, h, one.ID, "2099-01-01")
			done := session(t, h, one.ID, "2020-01-02")
			in := sessionRevision(done)
			in["completed_date"] = "2020-01-02"
			done = value[SessionDTO](t, apiCall(t, h, "POST", "/sessions/"+done.ID+"/complete", in, 200))
			method, path := "POST", "/tasks/"+one.ID+"/"+action
			input := revisions(one)
			if strings.Contains(action, "complete") {
				input["completed_date"] = "2020-01-03"
			}
			if strings.HasPrefix(action, "patch_") {
				method = "PATCH"
				path = "/tasks/" + one.ID
				if action == "patch_complete" {
					input["status"] = "completed"
				} else {
					input["status"] = "cancelled"
				}
			}
			apiCall(t, h, method, path, input, 422)
			input["sessions_decision"] = "cancel"
			input["session_revisions"] = map[string]int64{past.ID: past.Revision, future.ID: future.Revision}
			apiCall(t, h, method, path, input, 200)
			for _, v := range []SessionDTO{past, future} {
				got := getSess(t, h, v.ID)
				if got.Status != "cancelled" || got.Revision != 2 {
					t.Fatal(got)
				}
				if history(t, h, "session", v.ID)[0].OperationID != history(t, h, "task", one.ID)[0].OperationID {
					t.Fatal("operation IDs differ")
				}
			}
			if got := getSess(t, h, done.ID); got.Status != "completed" || got.Revision != done.Revision {
				t.Fatal("completed session changed")
			}
			apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": one.ID, "date": "2099-01-01"}, 422)
			apiCall(t, h, "POST", "/sessions/"+future.ID+"/restore", map[string]any{"expected_revision": 2}, 422)
			one = value[TaskDTO](t, apiCall(t, h, "GET", "/tasks/"+one.ID, nil, 200))
			apiCall(t, h, "POST", "/tasks/"+one.ID+"/restore", revisions(one), 200)
			if getSess(t, h, future.ID).Status != "cancelled" {
				t.Fatal("task restoration revived session")
			}
			apiCall(t, h, "POST", "/sessions/"+future.ID+"/restore", map[string]any{"expected_revision": 2}, 200)
		})
	}
}
func TestParentCompletionSessionsAndAtomicRollback(t *testing.T) {
	store := testStore(t)
	h := NewHandler(NewService(store))
	p := project(t, h, "P")
	root := task(t, h, map[string]any{"title": "Root", "project_id": p.ID})
	child := task(t, h, map[string]any{"title": "Child", "project_id": p.ID, "parent_task_id": root.ID})
	a := session(t, h, root.ID, "2099-01-01")
	b := session(t, h, child.ID, "2099-01-02")
	input := revisions(root, child)
	input["completed_date"] = "2020-01-01"
	input["children_decision"] = "complete"
	input["sessions_decision"] = "cancel"
	input["session_revisions"] = map[string]int64{a.ID: 1, b.ID: 1}
	// Stale child session revision rolls back the entire tree.
	apiCall(t, h, "PATCH", "/sessions/"+b.ID, map[string]any{"expected_revision": 1, "date": "2099-01-03"}, 200)
	apiCall(t, h, "POST", "/tasks/"+root.ID+"/complete", input, 409)
	if getSess(t, h, a.ID).Status != "planned" {
		t.Fatal("partial session cancellation")
	}
	input["session_revisions"] = map[string]int64{a.ID: 1, b.ID: 2}
	exec(t, store, `CREATE TRIGGER fail_task_history BEFORE INSERT ON history_events WHEN NEW.task_id='`+child.ID+`' BEGIN SELECT RAISE(ABORT,'test history failure'); END;`)
	apiCall(t, h, "POST", "/tasks/"+root.ID+"/complete", input, 422)
	if getSess(t, h, a.ID).Revision != 1 || getSess(t, h, b.ID).Revision != 2 || len(history(t, h, "session", a.ID)) != 1 || len(history(t, h, "task", root.ID)) != 1 {
		t.Fatal("history failure partially committed")
	}
	exec(t, store, "DROP TRIGGER fail_task_history")
	apiCall(t, h, "POST", "/tasks/"+root.ID+"/complete", input, 200)
	op := history(t, h, "task", root.ID)[0].OperationID
	for _, v := range []SessionDTO{a, b} {
		if getSess(t, h, v.ID).Status != "cancelled" || history(t, h, "session", v.ID)[0].OperationID != op {
			t.Fatal("tree sessions not closed atomically")
		}
	}
	if history(t, h, "task", child.ID)[0].OperationID != op {
		t.Fatal("child operation mismatch")
	}
}
func TestSessionParallelRevisionConflict(t *testing.T) {
	h := NewHandler(NewService(testStore(t)))
	one := task(t, h, map[string]any{"title": "Task"})
	a := session(t, h, one.ID, "2026-01-01")
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("PATCH", "/api/planner/sessions/"+a.ID, strings.NewReader(fmt.Sprintf(`{"expected_revision":1,"date":"2026-01-0%d"}`, i+2))))
			codes <- w.Code
		}(i)
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[200] != 1 || counts[409] != 1 || len(history(t, h, "session", a.ID)) != 2 {
		t.Fatal(counts)
	}
}
func TestSessionMigrationPreservesExistingRows(t *testing.T) {
	path := t.TempDir() + "/old.db"
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	if e = migrate(ctx, db, migrations[:2]); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO tasks(id,title) VALUES('task','Existing'); INSERT INTO sessions(id,task_id,date) VALUES('session','task','2026-01-01')"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	store, e := Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	v, e := NewService(store).Session(ctx, "session")
	if e != nil || v.CompletedDate != nil || v.Date != "2026-01-01" {
		t.Fatal(v, e)
	}
}

func TestKeepChildrenOpenRetainsTheirSessions(t *testing.T) {
	h := NewHandler(NewService(testStore(t)))
	p := project(t, h, "P")
	root := task(t, h, map[string]any{"title": "Root", "project_id": p.ID})
	child := task(t, h, map[string]any{"title": "Child", "project_id": p.ID, "parent_task_id": root.ID})
	a := session(t, h, root.ID, "2099-01-01")
	b := session(t, h, child.ID, "2099-01-01")
	input := revisions(root, child)
	input["completed_date"] = "2020-01-01"
	input["children_decision"] = "keep_open"
	input["sessions_decision"] = "cancel"
	input["session_revisions"] = map[string]int64{a.ID: 1}
	apiCall(t, h, "POST", "/tasks/"+root.ID+"/complete", input, 200)
	if getSess(t, h, a.ID).Status != "cancelled" || getSess(t, h, b.ID).Status != "planned" || getSess(t, h, b.ID).Revision != 1 {
		t.Fatal("keep_open affected child sessions")
	}
}
func TestSessionHistoryFailureRollsBack(t *testing.T) {
	store := testStore(t)
	h := NewHandler(NewService(store))
	one := task(t, h, map[string]any{"title": "Task"})
	a := session(t, h, one.ID, "2026-01-01")
	exec(t, store, `CREATE TRIGGER fail_session_event BEFORE INSERT ON history_events WHEN NEW.session_id IS NOT NULL BEGIN SELECT RAISE(ABORT,'session history failure'); END;`)
	apiCall(t, h, "POST", "/sessions/"+a.ID+"/cancel", sessionRevision(a), 422)
	if got := getSess(t, h, a.ID); got.Status != "planned" || got.Revision != 1 || len(history(t, h, "session", a.ID)) != 1 {
		t.Fatal("session failed history changed data")
	}
	apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": one.ID, "date": "2026-01-02"}, 422)
	if count(t, store, "sessions") != 1 {
		t.Fatal("session created without history")
	}
}

package planner

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"
)

func projection(t *testing.T, h http.Handler, from, to string) CalendarProjection {
	t.Helper()
	return value[CalendarProjection](t, apiCall(t, h, "GET", "/calendar?from="+from+"&to="+to, nil, 200))
}
func TestCalendarProjection(t *testing.T) {
	store := testStore(t)
	h := NewHandler(NewService(store))
	p := project(t, h, "Project")
	root := task(t, h, map[string]any{"title": "Root", "project_id": p.ID, "planned_date": "2026-12-30", "scheduled_time": "09:00", "due_date": "2027-01-03"})
	child := task(t, h, map[string]any{"title": "Child", "project_id": p.ID, "parent_task_id": root.ID, "planned_date": "2026-12-31"})
	plain := task(t, h, map[string]any{"title": "All day", "planned_date": "2027-01-01"})
	task(t, h, map[string]any{"title": "Inbox"})
	for _, d := range []string{"2026-12-30", "2027-01-02", "2027-01-02"} {
		apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": root.ID, "date": d, "start_time": "10:00", "end_time": "11:00"}, 201)
	}
	before := count(t, store, "tasks")
	historyBefore := count(t, store, "history_events")
	result := projection(t, h, "2026-12-30", "2027-01-03")
	if len(result.Items) != 6 || result.Timezone != "Europe/Moscow" || len(result.Projects) != 1 {
		t.Fatalf("%+v", result)
	}
	kinds := map[string]int{}
	for _, v := range result.Items {
		kinds[v.Type]++
		if v.TaskID == root.ID && v.Type == "task" {
			t.Fatal("duplicate scheduled task")
		}
		if v.TaskID == child.ID && (v.ParentTaskID == nil || *v.ParentTaskID != root.ID || *v.ProjectTitle != "Project") {
			t.Fatal(v)
		}
		if v.ID == plain.ID && v.StartTime != nil {
			t.Fatal(v)
		}
	}
	if kinds["session"] != 3 || kinds["deadline"] != 1 || kinds["task"] != 2 {
		t.Fatal(kinds)
	}
	if count(t, store, "tasks") != before || count(t, store, "history_events") != historyBefore {
		t.Fatal("projection mutated data")
	}
	// Different planning date becomes a marker, including when sessions are outside the range.
	apiCall(t, h, "POST", "/tasks/"+root.ID+"/reschedule", map[string]any{"expected_revision": 1, "planned_date": "2026-12-29"}, 200)
	single := projection(t, h, "2026-12-29", "2026-12-29")
	if len(single.Items) != 1 || single.Items[0].Type != "planning" || *single.Items[0].StartTime != "09:00" || single.Items[0].Revision != 2 {
		t.Fatal(single)
	}
}
func TestCalendarStatusesDeadlineAndCancelledSession(t *testing.T) {
	h := NewHandler(NewService(testStore(t)))
	a := task(t, h, map[string]any{"title": "Date and deadline", "planned_date": "2026-01-01", "due_date": "2026-01-01"})
	b := task(t, h, map[string]any{"title": "Only deadline", "due_date": "2026-01-02"})
	session := value[SessionDTO](t, apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": a.ID, "date": "2026-01-01"}, 201))
	apiCall(t, h, "POST", "/sessions/"+session.ID+"/cancel", map[string]any{"expected_revision": 1}, 200)
	apiCall(t, h, "POST", "/tasks/"+a.ID+"/complete", map[string]any{"expected_revision": 1, "completed_date": "2026-01-01"}, 200)
	apiCall(t, h, "POST", "/tasks/"+b.ID+"/cancel", map[string]any{"expected_revision": 1}, 200)
	result := projection(t, h, "2026-01-01", "2026-01-02")
	if len(result.Items) != 3 {
		t.Fatal(result)
	}
	for _, v := range result.Items {
		if v.ID == a.ID && (v.Type != "task" || v.Status != "completed") {
			t.Fatal(v)
		}
		if v.ID == b.ID && (v.Type != "deadline" || v.Status != "cancelled") {
			t.Fatal(v)
		}
	}
}
func TestCalendarRangeValidationAndIndexUse(t *testing.T) {
	store := testStore(t)
	h := NewHandler(NewService(store))
	for _, q := range []string{"", "?from=2026-01-01", "?from=2026-02-30&to=2026-03-01", "?from=2026-1-01&to=2026-01-02", "?from=2026-03-02&to=2026-03-01", "?from=2026-01-01&to=2027-01-01", "?from=0000-01-01&to=0000-01-01"} {
		apiCall(t, h, "GET", "/calendar"+q, nil, 422)
	}
	apiCall(t, h, "POST", "/calendar", map[string]any{}, 405)
	projection(t, h, "2024-02-29", "2024-03-01")
	projection(t, h, "2026-01-01", "2026-04-03") // Exactly 93 civil days.
	apiCall(t, h, "GET", "/calendar?from=2026-01-01&to=2026-04-04", nil, 422)
	e := store.Transaction(ctx, func(tx *sql.Tx) error {
		rows, e := tx.Query("EXPLAIN QUERY PLAN "+calendarSQL, "2026-11-01", "2026-11-30")
		if e != nil {
			return e
		}
		defer rows.Close()
		var plan strings.Builder
		for rows.Next() {
			var a, b, c int
			var d string
			if e = rows.Scan(&a, &b, &c, &d); e != nil {
				return e
			}
			plan.WriteString(d)
		}
		for _, index := range []string{"tasks_planned", "tasks_due", "sessions_date"} {
			if !strings.Contains(plan.String(), index) {
				t.Errorf("index %s absent: %s", index, plan.String())
			}
		}
		return rows.Err()
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestTaskContextScopedAndComplete(t *testing.T) {
	h := NewHandler(NewService(testStore(t)))
	p := project(t, h, "P")
	root := task(t, h, map[string]any{"title": "Root", "project_id": p.ID})
	child := task(t, h, map[string]any{"title": "Child", "project_id": p.ID, "parent_task_id": root.ID})
	other := task(t, h, map[string]any{"title": "Other"})
	for _, id := range []string{root.ID, child.ID, other.ID} {
		apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": id, "date": "2030-01-01"}, 201)
	}
	v := value[TaskContext](t, apiCall(t, h, "GET", "/tasks/"+root.ID+"/context", nil, 200))
	if v.Task.ID != root.ID || len(v.Children) != 1 || len(v.Sessions) != 2 {
		t.Fatal(v)
	}
	apiCall(t, h, "GET", "/tasks/missing/context", nil, 404)
	apiCall(t, h, "POST", "/tasks/"+root.ID+"/context", map[string]any{}, 405)
}

func TestCalendarDenseRangeFailsExplicitly(t *testing.T) {
	store := testStore(t)
	exec(t, store, `WITH RECURSIVE n(v) AS (SELECT 1 UNION ALL SELECT v+1 FROM n WHERE v<5001)
 INSERT INTO tasks(id,title,planned_date) SELECT 'dense-'||v,'Test event','2026-12-01' FROM n`)
	h := NewHandler(NewService(store))
	apiCall(t, h, "GET", "/calendar?from=2026-12-01&to=2026-12-01", nil, 422)
	result := projection(t, h, "2026-12-02", "2026-12-02")
	if len(result.Items) != 0 {
		t.Fatal("unrelated dates loaded")
	}
}

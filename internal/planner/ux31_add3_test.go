package planner

import (
	"budget/internal/planner/recurrence"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestUX31Add3SubtaskMapping(t *testing.T) {
	for _, instance := range []bool{false, true} {
		t.Run(map[bool]string{false: "source", true: "instance"}[instance], func(t *testing.T) {
			_, s, h := seriesFixture(t)
			in := ProjectDraftInput{RequestID: "create", AnchorDate: "2026-11-16", HasStart: true, ProjectMode: "independent", Rule: &recurrence.Rule{SchemaVersion: 1, Mode: recurrence.Schedule, Unit: recurrence.Weeks, Interval: 1, AllowedDays: recurrence.AllowedDays{Mode: recurrence.AnyDay}, Adjustment: recurrence.NoAdjustment}, Snapshot: ProjectTemplateSnapshot{Title: "Уборка", Tasks: []ProjectTemplateTask{}}}
			preview := value[ProjectDraftPreview](t, apiCall(t, h, "POST", "/project-drafts/preview", in, 200))
			in.PreviewToken = preview.PreviewToken
			p := *value[CommandResult](t, apiCall(t, h, "POST", "/project-drafts/apply", in, 200)).Project
			for _, title := range []string{"Пол", "Кухня"} {
				p, _ = s.Project(ctx, p.ID)
				scoped32(t, s, h, p, ProjectTaskScopeInput{Scope: "this_and_following", Action: "add", Patch: json.RawMessage(`{"title":"` + title + `"}`)})
			}
			if instance {
				p = *getSeriesTest(t, s, *p.RepeatSeriesID).Occurrences[0].Project
			}
			p, _ = s.Project(ctx, p.ID)
			parent := value[TaskDTO](t, apiCall(t, h, "POST", "/tasks", map[string]any{"title": "Локальный родитель", "project_id": p.ID}, 201))
			p, _ = s.Project(ctx, p.ID)
			_, result := scoped32(t, s, h, p, ProjectTaskScopeInput{Scope: "this_and_following", Action: "add", ParentID: parent.ID, Patch: json.RawMessage(`{"title":"Подзадача","planned_date":"` + *p.StartDate + `"}`)})
			if result.Task.ParentTaskID == nil || *result.Task.ParentTaskID != parent.ID {
				t.Fatal("local parent lost")
			}
			v := getSeriesTest(t, s, *p.RepeatSeriesID)
			for _, o := range v.Occurrences {
				if *o.Project.StartDate < *p.StartDate {
					continue
				}
				var child *TaskDTO
				for i := range o.Project.Tasks {
					if o.Project.Tasks[i].Title == "Подзадача" {
						child = &o.Project.Tasks[i]
					}
				}
				if child == nil || child.ParentTaskID == nil {
					t.Fatal("missing child", o.Project.ID)
				}
				found := false
				for _, root := range o.Project.Tasks {
					if root.ID == *child.ParentTaskID && root.Title == parent.Title && root.ParentTaskID == nil {
						found = true
					}
				}
				if !found {
					t.Fatal("wrong parent mapping")
				}
			}
		})
	}
}

func TestUX31Add3MigrateSequentialPreservesEntities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := openSQL(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = migrate(ctx, db, migrations[:14]); err != nil {
		t.Fatal(err)
	}
	st := &Store{db: db, path: path}
	s := NewService(st)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	h := NewHandler(s)
	p := beta3Draft(t, h, "old-sequential")
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	exec(t, st, "UPDATE project_repeat_state SET current_project_id=NULL,needs_decision=1 WHERE series_id=?", v.ID)
	future := v.Occurrences[0].Project
	apiCall(t, h, "PATCH", "/tasks/"+future.Tasks[0].ID, map[string]any{"expected_revision": future.Tasks[0].Revision, "title": "Моё исключение"}, 200)
	// Compare storage, not computed UI mode flags.
	snapshot := func(table string) string {
		rows, e := db.Query("SELECT * FROM " + table + " ORDER BY 1")
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		out := []any{}
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if e = rows.Scan(ptrs...); e != nil {
				t.Fatal(e)
			}
			out = append(out, values)
		}
		raw, _ := json.Marshal(out)
		return string(raw)
	}
	saved := map[string]string{}
	for _, table := range []string{"tasks", "projects", "occurrences", "series_runtime", "occurrence_incidents", "series_versions"} {
		saved[table] = snapshot(table)
	}
	events := count(t, st, "history_events")
	if err = migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	for table, want := range saved {
		if got := snapshot(table); got != want {
			t.Fatal("conversion changed", table)
		}
	}
	p, _ = s.Project(ctx, p.ID)
	if p.ProjectMode != "independent" || p.Preliminary || p.CurrentProjectID != nil || p.NeedsDecision {
		t.Fatal(p)
	}
	if count(t, st, "history_events") != events+1 {
		t.Fatal("missing conversion history")
	}
	if err = migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	if count(t, st, "history_events") != events+1 {
		t.Fatal("migration not idempotent")
	}
	f, _ := s.Project(ctx, future.ID)
	if f.Preliminary {
		t.Fatal("future remains blocked")
	}
	task := f.Tasks[0]
	apiCall(t, h, "POST", "/tasks/"+task.ID+"/complete", map[string]any{"expected_revision": task.Revision, "completed_date": "2026-11-24"}, 200)
	lifecycleIntegrity(t, st)
}

func TestUX31Add3ArchiveSeriesAndAttentionGroups(t *testing.T) {
	st, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("archive-add3", "2026-11-16", recurrence.Schedule, 7))
	managedApply(t, s, v.ID, SeriesCommand{Action: "archive"})
	v = getSeriesTest(t, s, v.ID)
	tasksBefore := count(t, st, "tasks")
	in := DeletionInput{ExpectedRevision: v.Revision, RequestID: "unarchive-add3"}
	result := value[CommandResult](t, apiCall(t, h, "POST", "/series/"+v.ID+"/unarchive", in, 200))
	if result.Series.State != "active" {
		t.Fatal(result)
	}
	apiCall(t, h, "POST", "/series/"+v.ID+"/unarchive", in, 200)
	if count(t, st, "tasks") != tasksBefore {
		t.Fatal("unarchive changed existing tasks")
	}
	in.RequestID = "stale"
	apiCall(t, h, "POST", "/series/"+v.ID+"/unarchive", in, 409)
	task := value[TaskDTO](t, apiCall(t, h, "POST", "/tasks", map[string]any{"title": "Просрочка", "planned_date": "2026-11-15"}, 201))
	apiCall(t, h, "POST", "/attention/"+task.ID, SnoozeInput{Type: "task", Until: "2026-11-17", RequestID: "defer-add3"}, 200)
	type attention struct {
		Items []struct {
			ID    string `json:"id"`
			Group string `json:"group"`
		} `json:"items"`
		Count int `json:"count"`
	}
	assertGroup := func(want string) {
		r := value[attention](t, apiCall(t, h, "GET", "/attention?tab=active", nil, 200))
		found := 0
		for _, item := range r.Items {
			if item.ID == task.ID {
				found++
				if item.Group != want {
					t.Fatal(item)
				}
			}
		}
		if found != 1 {
			t.Fatal("missing or duplicate problem", r)
		}
	}
	assertGroup("deferred")
	s.now = func() time.Time { return time.Date(2026, 11, 16, 21, 0, 0, 0, time.UTC) }
	assertGroup("late")
	fresh, _ := s.Task(ctx, task.ID)
	if fresh.Revision != task.Revision || *fresh.PlannedDate != *task.PlannedDate {
		t.Fatal("snooze changed task")
	}
	lifecycleIntegrity(t, st)
}

func TestUX31Add3MoveSourceDoesNotShiftSchedule(t *testing.T) {
	_, s, h := seriesFixture(t)
	p := beta3Draft(t, h, "source-move")
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	before := v.Occurrences
	data := map[string]any{"action": "reschedule", "planned_date": "2026-11-28", "tasks_date_decision": "shift", "expected_revision": p.Revision, "expected_series_revision": v.Revision, "related_revisions": revisionMapAdd3(p.Tasks), "request_id": "move-source"}
	out := value[CommandResult](t, apiCall(t, h, "POST", "/projects/"+p.ID+"/close-current", data, 200))
	if *out.Project.StartDate != "2026-11-28" || *out.Project.DueDate != "2026-11-30" {
		t.Fatal(out.Project)
	}
	after := getSeriesTest(t, s, v.ID).Occurrences
	for i, o := range before {
		if o.ID != after[i].ID || *o.Project.StartDate != *after[i].Project.StartDate {
			t.Fatal("series shifted")
		}
	}
}

func revisionMapAdd3(tasks []TaskDTO) map[string]int64 {
	r := map[string]int64{}
	for _, t := range tasks {
		r[t.ID] = t.Revision
	}
	return r
}

func TestUX31Add3SourceOnlyEndDate(t *testing.T) {
	_, s, h := seriesFixture(t)
	end := "2026-11-16"
	in := ProjectDraftInput{RequestID: "bounded", AnchorDate: end, HasStart: true, EndDate: &end, Rule: &recurrence.Rule{SchemaVersion: 1, Mode: recurrence.Schedule, Unit: recurrence.Weeks, Interval: 1, AllowedDays: recurrence.AllowedDays{Mode: recurrence.AnyDay}, Adjustment: recurrence.NoAdjustment}, Snapshot: ProjectTemplateSnapshot{Title: "Только исходное выполнение", Tasks: []ProjectTemplateTask{}}}
	preview := value[ProjectDraftPreview](t, apiCall(t, h, "POST", "/project-drafts/preview", in, 200))
	if preview.Next != nil {
		t.Fatal("preview goes beyond end")
	}
	in.PreviewToken = preview.PreviewToken
	p := value[CommandResult](t, apiCall(t, h, "POST", "/project-drafts/apply", in, 200)).Project
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	if len(v.Occurrences) != 0 || p.ProjectMode != "independent" {
		t.Fatal(v)
	}
}

func TestUX31Add3ConcurrentSubtaskPromotion(t *testing.T) {
	st, s, h := seriesFixture(t)
	p := beta3Draft(t, h, "concurrent-parent")
	parent := value[TaskDTO](t, apiCall(t, h, "POST", "/tasks", map[string]any{"project_id": p.ID, "title": "Локальный родитель"}, 201))
	p, _ = s.Project(ctx, p.ID)
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	in := ProjectTaskScopeInput{Scope: "this_and_following", Action: "add", ParentID: parent.ID, ExpectedRevision: p.Revision, ExpectedSeriesRevision: v.Revision, Patch: json.RawMessage(`{"title":"Один ребёнок"}`)}
	preview := value[ProjectTaskScopePreview](t, apiCall(t, h, "POST", "/projects/"+p.ID+"/task-change-preview", in, 200))
	in.PreviewToken = preview.PreviewToken
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			request := in
			request.RequestID = fmt.Sprintf("race-child-%d", i)
			data, _ := json.Marshal(request)
			req := httptest.NewRequest("POST", "/api/planner/projects/"+p.ID+"/task-change-apply", bytes.NewReader(data))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			codes <- w.Code
		}(i)
	}
	wg.Wait()
	close(codes)
	success, conflict := 0, 0
	for code := range codes {
		switch code {
		case 200:
			success++
		case 409:
			conflict++
		default:
			t.Fatal("unexpected status", code)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
	p, _ = s.Project(ctx, p.ID)
	children := 0
	for _, task := range p.Tasks {
		if task.ParentTaskID != nil && *task.ParentTaskID == parent.ID {
			children++
		}
	}
	if children != 1 {
		t.Fatal("duplicated child", children)
	}
	lifecycleIntegrity(t, st)
}

func TestUX31Add3AttentionKeepsEveryProcessedIncident(t *testing.T) {
	_, _, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("incidents-add3", "2026-11-16", recurrence.Schedule, 7))
	id := v.Occurrences[0].ID
	for i, action := range []string{"skip", "review", "restore", "skip", "review"} {
		c := value[CommandResult](t, apiCall(t, h, "GET", "/occurrences/"+id, nil, 200))
		in := OccurrenceCommand{RequestID: fmt.Sprintf("incident-%d", i), ExpectedRevision: c.Occurrence.Revision, ExpectedTaskRevision: c.Occurrence.Task.Revision, ExpectedSeriesRevision: c.Series.Revision}
		apiCall(t, h, "POST", "/occurrences/"+id+"/"+action, in, 200)
	}
	out := value[struct {
		Items []map[string]any `json:"items"`
	}](t, apiCall(t, h, "GET", "/attention?tab=history&series_id="+v.ID, nil, 200))
	if len(out.Items) != 2 {
		t.Fatal("history omitted an incident", out)
	}
}

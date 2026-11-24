package planner

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func scoped32(t *testing.T, s *Service, h http.Handler, p ProjectDTO, in ProjectTaskScopeInput) (ProjectTaskScopePreview, CommandResult) {
	t.Helper()
	p, _ = s.Project(ctx, p.ID)
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	in.ExpectedRevision = p.Revision
	in.ExpectedSeriesRevision = v.Revision
	if in.TaskID != "" {
		task, _ := s.Task(ctx, in.TaskID)
		in.ExpectedTaskRevision = task.Revision
	}
	preview := value[ProjectTaskScopePreview](t, apiCall(t, h, "POST", "/projects/"+p.ID+"/task-change-preview", in, 200))
	in.PreviewToken = preview.PreviewToken
	in.RequestID = "scope32-" + p.ID + "-" + in.Action + "-" + preview.PreviewToken[:8]
	in.Confirm = in.Action == "remove"
	result := value[CommandResult](t, apiCall(t, h, "POST", "/projects/"+p.ID+"/task-change-apply", in, 200))
	again := value[CommandResult](t, apiCall(t, h, "POST", "/projects/"+p.ID+"/task-change-apply", in, 200))
	if !reflect.DeepEqual(result, again) {
		t.Fatal("idempotency")
	}
	return preview, result
}
func TestBeta32ScopedFieldsDatesAndProtection(t *testing.T) {
	_, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	p := beta3Draft(t, h, "scope-fields32")
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	protected := v.Occurrences[1].Project.Tasks[0]
	apiCall(t, h, "PATCH", "/tasks/"+protected.ID, map[string]any{"expected_revision": protected.Revision, "description": "Мой перенос и описание"}, 200)
	patch := json.RawMessage(`{"planned_date":"2026-11-29","title":"Полы в воскресенье","description":"Только выбранные поля","priority":"high","scheduled_time":"15:30","color_id":"#3487AF","icon_id":"lucide:house"}`)
	pre, _ := scoped32(t, s, h, p, ProjectTaskScopeInput{Scope: "this_and_following", Action: "edit", TaskID: p.Tasks[1].ID, Patch: patch})
	task, _ := s.Task(ctx, p.Tasks[1].ID)
	if *task.PlannedDate != "2026-11-29" || task.Title != "Полы в воскресенье" || task.Priority != "high" || *task.ScheduledTime != "15:30" || !reflect.DeepEqual(task.DueDate, p.Tasks[1].DueDate) {
		t.Fatal(task)
	}
	skipped := 0
	for _, r := range pre.Future.Rows {
		next, _ := s.Project(ctx, r.Before.ID)
		if r.After == nil {
			skipped++
			if !reflect.DeepEqual(next, r.Before) {
				t.Fatal("protected project changed")
			}
			continue
		}
		for _, old := range r.Before.Tasks {
			current, _ := s.Task(ctx, old.ID)
			if old.Title == p.Tasks[1].Title {
				date, _ := offsetDate(*next.StartDate, ptr(2))
				if *current.PlannedDate != *date || current.Title != task.Title || *current.ScheduledTime != "15:30" {
					t.Fatal(current)
				}
			} else if current.Title != old.Title || !reflect.DeepEqual(current.PlannedDate, old.PlannedDate) {
				t.Fatal("unrelated task changed")
			}
		}
	}
	if skipped != 1 {
		t.Fatal(skipped)
	}
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID, Through: "2027-07-01"})
	v = getSeriesTest(t, s, v.ID)
	for _, o := range v.Occurrences {
		if o.AdjustedDate > "2027-03-01" {
			for _, task := range o.Project.Tasks {
				if task.Title == "Полы в воскресенье" {
					want, _ := offsetDate(o.AdjustedDate, ptr(2))
					if *task.PlannedDate != *want {
						t.Fatal(task)
					}
				}
			}
		}
	}
}
func TestBeta32FollowingBoundaryLongOffsetsAndRestart(t *testing.T) {
	db, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	p := beta3Draft(t, h, "scope-long32")
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	selected := *v.Occurrences[2].Project
	beforeSource, _ := s.Project(ctx, p.ID)
	beforeEarlier := *v.Occurrences[1].Project
	for _, days := range []int{8, 15, 35, 70, -8} {
		chosen, _ := offsetDate(*selected.StartDate, &days)
		raw, _ := json.Marshal(map[string]any{"planned_date": chosen, "due_date": nil})
		pre, _ := scoped32(t, s, h, selected, ProjectTaskScopeInput{Scope: "following", Action: "edit", TaskID: selected.Tasks[0].ID, Patch: raw})
		now, _ := s.Project(ctx, selected.ID)
		if !reflect.DeepEqual(now, selected) {
			t.Fatal("selected changed in following-only")
		}
		earlier, _ := s.Project(ctx, beforeEarlier.ID)
		if !reflect.DeepEqual(earlier, beforeEarlier) {
			t.Fatal("earlier cycle changed")
		}
		source, _ := s.Project(ctx, p.ID)
		if !reflect.DeepEqual(source, beforeSource) {
			t.Fatal("source changed")
		}
		for _, r := range pre.Future.Rows {
			if r.After == nil {
				continue
			}
			var counterpart TaskDTO
			for _, candidate := range r.Before.Tasks {
				if candidate.Title == selected.Tasks[0].Title {
					counterpart = candidate
				}
			}
			actual, _ := s.Task(ctx, counterpart.ID)
			want, _ := offsetDate(*r.Before.StartDate, &days)
			if *actual.PlannedDate != *want || actual.DueDate != nil {
				t.Fatal("unbounded offset", days, actual)
			}
		}
	}
	file := filepath.Join(t.TempDir(), "roundtrip.sqlite")
	if e := db.Export(ctx, file); e != nil {
		t.Fatal(e)
	}
	if e := db.Restore(ctx, file); e != nil {
		t.Fatal(e)
	}
	fresh, e := Open(ctx, filepath.Join(t.TempDir(), "reopen.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer fresh.Close()
	if e = fresh.Restore(ctx, file); e != nil {
		t.Fatal(e)
	}
	ps, e := NewService(fresh).Project(ctx, selected.ID)
	if e != nil || !reflect.DeepEqual(ps, selected) {
		t.Fatal("restore/restart changed selected")
	}
	if e = db.Transaction(ctx, func(tx *sql.Tx) error { return checkIntegrity(ctx, tx) }); e != nil {
		t.Fatal(e)
	}
}
func TestBeta32ScopedAddRemoveSubtaskOrder(t *testing.T) {
	db, s, h := seriesFixture(t)
	p := beta3Draft(t, h, "structure32")
	_, result := scoped32(t, s, h, p, ProjectTaskScopeInput{Scope: "this_and_following", Action: "add", ParentID: p.Tasks[0].ID, Patch: json.RawMessage(`{"title":"Протереть ручку","planned_date":"2026-12-30"}`)})
	if result.Task == nil || result.Task.ParentTaskID == nil {
		t.Fatal(result)
	}
	added := *result.Task
	pre, _ := scoped32(t, s, h, p, ProjectTaskScopeInput{Scope: "this_and_following", Action: "remove", TaskID: added.ID})
	if len(pre.Deletions) < 2 {
		t.Fatal("future deletions absent")
	}
	if _, e := s.Task(ctx, added.ID); e == nil {
		t.Fatal("not physically deleted")
	}
	p, _ = s.Project(ctx, p.ID)
	ids := []string{p.Tasks[2].ID, p.Tasks[0].ID, p.Tasks[1].ID}
	revs := map[string]int64{}
	for _, t := range p.Tasks {
		revs[t.ID] = t.Revision
	}
	pre, _ = scoped32(t, s, h, p, ProjectTaskScopeInput{Scope: "this_and_following", Action: "order", TaskIDs: ids, RelatedRevisions: revs})
	first, _ := s.Task(ctx, ids[0])
	if first.SortOrder != 0 {
		t.Fatal("current order")
	}
	updated := 0
	for _, r := range pre.Future.Rows {
		if r.After != nil {
			updated++
			next, _ := s.Project(ctx, r.Before.ID)
			for _, task := range next.Tasks {
				if task.Title == first.Title && task.SortOrder != 0 {
					t.Fatal("future order")
				}
			}
		}
	}
	if updated == 0 {
		t.Fatal("server-generated deletion incorrectly protected future projects")
	}
	if e := db.Transaction(ctx, func(tx *sql.Tx) error { return checkIntegrity(ctx, tx) }); e != nil {
		t.Fatal(e)
	}
}
func TestBeta32ScopedRollbackAndConflict(t *testing.T) {
	db, s, h := seriesFixture(t)
	p := beta3Draft(t, h, "rollback32")
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	in := ProjectTaskScopeInput{ExpectedRevision: p.Revision, ExpectedSeriesRevision: v.Revision, ExpectedTaskRevision: p.Tasks[0].Revision, Scope: "this_and_following", Action: "edit", TaskID: p.Tasks[0].ID, Patch: json.RawMessage(`{"planned_date":"2027-01-02"}`)}
	pre := value[ProjectTaskScopePreview](t, apiCall(t, h, "POST", "/projects/"+p.ID+"/task-change-preview", in, 200))
	in.PreviewToken = pre.PreviewToken
	in.RequestID = "rollback32-edit"
	if e := db.Transaction(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec("CREATE TRIGGER fail_scope BEFORE UPDATE ON tasks WHEN old.id='" + p.Tasks[0].ID + "' BEGIN SELECT RAISE(ABORT,'forced'); END")
		return e
	}); e != nil {
		t.Fatal(e)
	}
	apiCall(t, h, "POST", "/projects/"+p.ID+"/task-change-apply", in, 422)
	if e := db.Transaction(ctx, func(tx *sql.Tx) error { _, e := tx.Exec("DROP TRIGGER fail_scope"); return e }); e != nil {
		t.Fatal(e)
	}
	after := getSeriesTest(t, s, v.ID)
	if !reflect.DeepEqual(v, after) {
		t.Fatal("partial future update committed")
	}
	apiCall(t, h, "PATCH", "/tasks/"+p.Tasks[0].ID, map[string]any{"expected_revision": p.Tasks[0].Revision, "description": "Конкурентное изменение"}, 200)
	apiCall(t, h, "POST", "/projects/"+p.ID+"/task-change-apply", in, 409)
}

func TestBeta32ScopeProtectsClosedStartedAndSessions(t *testing.T) {
	_, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	p := beta3Draft(t, h, "protected32")
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	mode := ProjectModeInput{ExpectedRevision: v.Revision, Mode: "independent", CurrentID: p.ID}
	mp := value[ProjectModePreview](t, apiCall(t, h, "POST", "/series/"+v.ID+"/project-preview", mode, 200))
	mode.PreviewToken = mp.PreviewToken
	mode.RequestID = "protection32-independent"
	apiCall(t, h, "POST", "/series/"+v.ID+"/project-apply", mode, 200)
	v = getSeriesTest(t, s, v.ID)

	closed := v.Occurrences[0].Project.Tasks[0]
	apiCall(t, h, "POST", "/tasks/"+closed.ID+"/complete", map[string]any{"expected_revision": closed.Revision, "completed_date": "2026-11-24"}, 200)
	started := v.Occurrences[1].Project.Tasks[0]
	apiCall(t, h, "PATCH", "/tasks/"+started.ID, map[string]any{"expected_revision": started.Revision, "status": "in_progress"}, 200)
	sessionTask := v.Occurrences[2].Project.Tasks[0]
	apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": sessionTask.ID, "date": sessionTask.PlannedDate}, 201)
	before := map[string]ProjectDTO{}
	for _, o := range v.Occurrences[:3] {
		before[o.Project.ID], _ = s.Project(ctx, o.Project.ID)
	}
	current := p.Tasks[0]
	pre, _ := scoped32(t, s, h, p, ProjectTaskScopeInput{Scope: "this_and_following", Action: "edit", TaskID: current.ID, Patch: json.RawMessage(`{"due_date":"2027-01-01"}`)})
	for _, r := range pre.Future.Rows {
		if old, ok := before[r.Before.ID]; ok {
			if r.After != nil {
				t.Fatal("protected instance eligible")
			}
			actual, _ := s.Project(ctx, old.ID)
			if !reflect.DeepEqual(actual, old) {
				t.Fatal("protected instance changed")
			}
		}
	}
	now, _ := s.Task(ctx, current.ID)
	if !reflect.DeepEqual(now.PlannedDate, current.PlannedDate) || now.DueDate == nil || *now.DueDate != "2027-01-01" {
		t.Fatal("deadline changed planned date", now)
	}
	// A stale preview must not bypass protection on the explicitly selected task.
	p, _ = s.Project(ctx, p.ID)
	v = getSeriesTest(t, s, v.ID)
	in := ProjectTaskScopeInput{Scope: "this_and_following", Action: "edit", TaskID: now.ID, ExpectedRevision: p.Revision, ExpectedSeriesRevision: v.Revision, ExpectedTaskRevision: now.Revision, Patch: json.RawMessage(`{"title":"Новое название"}`)}
	pre = value[ProjectTaskScopePreview](t, apiCall(t, h, "POST", "/projects/"+p.ID+"/task-change-preview", in, 200))
	apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": now.ID, "date": now.PlannedDate}, 201)
	in.RequestID = "protected32-stale"
	in.PreviewToken = pre.PreviewToken
	apiCall(t, h, "POST", "/projects/"+p.ID+"/task-change-apply", in, 422)
	afterSeries := getSeriesTest(t, s, v.ID)
	if !reflect.DeepEqual(afterSeries.ProjectTemplate, v.ProjectTemplate) {
		t.Fatal("partial update after protection failure")
	}
}

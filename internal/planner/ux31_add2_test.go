package planner

import (
	"budget/internal/planner/recurrence"
	"encoding/json"
	"testing"
)

func TestUX31Add2EmptyRecurringProjectComposition(t *testing.T) {
	for _, mode := range []string{"sequential", "independent"} {
		t.Run(mode, func(t *testing.T) {
			db, s, h := seriesFixture(t)
			in := ProjectDraftInput{RequestID: "empty-create", AnchorDate: "2026-11-16", HasStart: true, ProjectMode: mode, Rule: &recurrence.Rule{SchemaVersion: 1, Mode: recurrence.Schedule, Unit: recurrence.Weeks, Interval: 1, AllowedDays: recurrence.AllowedDays{Mode: recurrence.AnyDay}, Adjustment: recurrence.NoAdjustment}, Snapshot: ProjectTemplateSnapshot{Title: "Пустая уборка", Tasks: []ProjectTemplateTask{}}}
			preview := value[ProjectDraftPreview](t, apiCall(t, h, "POST", "/project-drafts/preview", in, 200))
			in.PreviewToken = preview.PreviewToken
			p := *value[CommandResult](t, apiCall(t, h, "POST", "/project-drafts/apply", in, 200)).Project
			v := getSeriesTest(t, s, *p.RepeatSeriesID)
			if len(v.Occurrences) != 2 || count(t, db, "tasks") != 0 {
				t.Fatal("expected source plus two empty future instances")
			}
			// Reproduce the old default: a local add does not update the template.
			local := value[TaskDTO](t, apiCall(t, h, "POST", "/tasks", map[string]any{"title": "Только здесь", "project_id": p.ID}, 201))
			v = getSeriesTest(t, s, v.ID)
			for _, o := range v.Occurrences {
				if len(o.Project.Tasks) != 0 {
					t.Fatal("local scope leaked")
				}
			}
			// The explicit shared scope updates empty unmodified futures, atomically and idempotently.
			_, out := scoped32(t, s, h, p, ProjectTaskScopeInput{Scope: "this_and_following", Action: "add", Patch: json.RawMessage(`{"title":"Мыть пол","planned_date":"2026-11-17","scheduled_time":"15:00"}`)})
			v = getSeriesTest(t, s, v.ID)
			for _, o := range v.Occurrences {
				if len(o.Project.Tasks) != 1 || o.Project.Tasks[0].Title != "Мыть пол" || *o.Project.Tasks[0].ScheduledTime != "15:00" {
					t.Fatal("future composition missing", o.Project)
				}
			}
			p, _ = s.Project(ctx, p.ID)
			if len(p.Tasks) != 2 {
				t.Fatal("local task lost")
			}
			if _, e := s.Task(ctx, local.ID); e != nil {
				t.Fatal(e)
			}
			// A manually edited future is protected as a whole, never overwritten by propagation.
			protected := v.Occurrences[1].Project.Tasks[0]
			apiCall(t, h, "PATCH", "/tasks/"+protected.ID, map[string]any{"expected_revision": protected.Revision, "title": "Моё исключение"}, 200)
			pre, _ := scoped32(t, s, h, p, ProjectTaskScopeInput{Scope: "this_and_following", Action: "add", ParentID: out.Task.ID, Patch: json.RawMessage(`{"title":"Подзадача"}`)})
			guarded := false
			for _, r := range pre.Future.Rows {
				if r.Before.ID == v.Occurrences[1].Project.ID {
					guarded = r.After == nil
				}
			}
			if !guarded {
				t.Fatal("manual future overwritten")
			}
			before := recurrenceDBSnapshot(t, db)
			drainSeries(t, s, MaterializeInput{SeriesID: v.ID})
			if before != recurrenceDBSnapshot(t, db) {
				t.Fatal("materialization duplicated existing projects")
			}
			calendar := projection(t, h, "2026-11-16", "2026-12-31")
			for _, cp := range calendar.Projects {
				if cp.RepeatSeriesID == nil || *cp.RepeatSeriesID != v.ID {
					t.Fatal("calendar series filter metadata missing")
				}
			}
		})
	}
}

func TestUX31Add2ProjectCompletionRequiresEveryTask(t *testing.T) {
	_, s, h := seriesFixture(t)
	p := value[ProjectDTO](t, apiCall(t, h, "POST", "/projects", map[string]any{"title": "Готовность"}, 201))
	apiCall(t, h, "POST", "/projects/"+p.ID+"/complete", map[string]any{"expected_revision": p.Revision}, 422)
	parent := value[TaskDTO](t, apiCall(t, h, "POST", "/tasks", map[string]any{"title": "Родитель", "project_id": p.ID}, 201))
	child := value[TaskDTO](t, apiCall(t, h, "POST", "/tasks", map[string]any{"title": "Подзадача", "project_id": p.ID, "parent_task_id": parent.ID}, 201))
	apiCall(t, h, "POST", "/tasks/"+parent.ID+"/complete", map[string]any{"expected_revision": parent.Revision, "completed_date": "2026-11-16", "children_decision": "keep_open", "related_revisions": map[string]int64{child.ID: child.Revision}}, 200)
	fresh, _ := s.Task(ctx, child.ID)
	if fresh.Status != "planned" {
		t.Fatal("child cascade")
	}
	p, _ = s.Project(ctx, p.ID)
	if p.CompletionReady || p.RemainingTasks != 1 || p.Status != "planned" {
		t.Fatal(p)
	}
	apiCall(t, h, "POST", "/projects/"+p.ID+"/complete", map[string]any{"expected_revision": p.Revision}, 422)
	apiCall(t, h, "POST", "/tasks/"+child.ID+"/cancel", map[string]any{"expected_revision": child.Revision}, 200)
	p, _ = s.Project(ctx, p.ID)
	apiCall(t, h, "PATCH", "/projects/"+p.ID, map[string]any{"expected_revision": p.Revision, "status": "completed"}, 422)
	fresh, _ = s.Task(ctx, child.ID)
	apiCall(t, h, "POST", "/tasks/"+child.ID+"/restore", map[string]any{"expected_revision": fresh.Revision}, 200)
	fresh, _ = s.Task(ctx, child.ID)
	apiCall(t, h, "POST", "/tasks/"+child.ID+"/complete", map[string]any{"expected_revision": fresh.Revision, "completed_date": "2026-11-16"}, 200)
	p, _ = s.Project(ctx, p.ID)
	if !p.CompletionReady || p.Status != "planned" {
		t.Fatal("last task must not auto-close", p)
	}
	apiCall(t, h, "POST", "/projects/"+p.ID+"/complete", map[string]any{"expected_revision": p.Revision}, 200)
	p, _ = s.Project(ctx, p.ID)
	if p.Status != "completed" {
		t.Fatal(p)
	}
}

func TestUX31Add2RecurringCompletionCannotCascade(t *testing.T) {
	_, s, h := seriesFixture(t)
	p := beta3Draft(t, h, "ready-source")
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	apiCall(t, h, "POST", "/projects/"+p.ID+"/close-current", map[string]any{"action": "complete", "expected_revision": p.Revision, "expected_series_revision": v.Revision, "completed_date": "2026-11-16", "tasks_decision": "complete_open", "request_id": "reject-cascade"}, 422)
	for _, task := range p.Tasks {
		apiCall(t, h, "POST", "/tasks/"+task.ID+"/complete", map[string]any{"expected_revision": task.Revision, "completed_date": "2026-11-16"}, 200)
	}
	p, _ = s.Project(ctx, p.ID)
	v = getSeriesTest(t, s, v.ID)
	apiCall(t, h, "POST", "/projects/"+p.ID+"/close-current", map[string]any{"action": "complete", "expected_revision": p.Revision, "expected_series_revision": v.Revision, "completed_date": "2026-11-16", "request_id": "ready-close"}, 200)
	v = getSeriesTest(t, s, v.ID)
	o := v.Occurrences[0]
	input := projectOccurrenceInput(t, s, o.ID)
	input.CompletedDate = ptr("2026-11-16")
	input.TasksDecision = "complete_open"
	apiCall(t, h, "POST", "/occurrences/"+o.ID+"/complete", input, 422)
}

// Test setup explicitly completes each task; project completion no longer cascades.
func finishProjectTasksTest(t *testing.T, s *Service, id, date string) {
	t.Helper()
	p, e := s.Project(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	for _, task := range p.Tasks {
		if task.Status == "completed" {
			continue
		}
		fresh, e := s.Task(ctx, task.ID)
		if e != nil {
			t.Fatal(e)
		}
		related := map[string]int64{}
		current, e := s.Project(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		for _, c := range current.Tasks {
			if c.ParentTaskID != nil && *c.ParentTaskID == task.ID {
				related[c.ID] = c.Revision
			}
		}
		_, e = s.ChangeTask(ctx, task.ID, "complete", TaskInput{Mutation: Mutation{ExpectedRevision: fresh.Revision, RelatedRevisions: related}, CompletedDate: &date, ChildrenDecision: "keep_open"})
		if e != nil {
			t.Fatal(e)
		}
	}
}

func TestUX31Add2NearestSettingAndOverlappingProjectDates(t *testing.T) {
	for _, mode := range []recurrence.Mode{recurrence.Schedule, recurrence.Completion} {
		t.Run(string(mode), func(t *testing.T) {
			_, s, h := seriesFixture(t)
			setProjectLimitTest(t, h, 5)
			v := createSeries(t, h, seriesInput("nearest-shared", "2026-11-16", mode, 30))
			for i := 0; i < 5; i++ {
				if _, e := s.EnsureUpcoming(ctx, v.ID); e != nil {
					t.Fatal(e)
				}
			}
			v = getSeriesTest(t, s, v.ID)
			if len(v.Occurrences) != 5 {
				t.Fatalf("limit 5: got %d", len(v.Occurrences))
			}
			ids := map[string]bool{}
			for _, o := range v.Occurrences {
				ids[o.ID] = true
			}
			setProjectLimitTest(t, h, 1)
			if _, e := s.EnsureUpcoming(ctx, v.ID); e != nil {
				t.Fatal(e)
			}
			v = getSeriesTest(t, s, v.ID)
			if len(v.Occurrences) != 5 {
				t.Fatal("lowering quota deleted history")
			}
			for _, o := range v.Occurrences {
				if !ids[o.ID] {
					t.Fatal("ID changed")
				}
			}
		})
	}
	_, s, _ := seriesFixture(t)
	v := projectSeriesTest(t, s, createTemplateTest(t, s, cleaningSnapshot()), "2026-11-16", 7)
	first, second := v.Occurrences[0], v.Occurrences[1]
	in := projectOccurrenceInput(t, s, first.ID)
	in.PlannedDate = second.Project.StartDate
	in.TasksDateDecision = "shift"
	moved := occurrenceApply(t, s, first.ID, "reschedule", in)
	unchanged, _ := s.Project(ctx, second.Project.ID)
	if *moved.Project.StartDate != *unchanged.StartDate || moved.Project.ID == unchanged.ID {
		t.Fatal("expected two independent overlapping instances")
	}
	if len(moved.Project.Tasks) != len(first.Project.Tasks) || len(unchanged.Tasks) != len(second.Project.Tasks) {
		t.Fatal("move changed task composition")
	}
}

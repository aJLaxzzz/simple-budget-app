package planner

import (
	"budget/internal/planner/recurrence"
	"encoding/json"
	"reflect"
	"testing"
)

func TestUX31Add4DailyWeeklyComposition(t *testing.T) {
	for _, selectedFuture := range []bool{false, true} {
		t.Run(map[bool]string{false: "source", true: "future"}[selectedFuture], func(t *testing.T) {
			st, s, h := seriesFixture(t)
			in := ProjectDraftInput{RequestID: "daily", AnchorDate: "2026-11-17", HasStart: true, Rule: &recurrence.Rule{SchemaVersion: 1, Mode: recurrence.Schedule, Unit: recurrence.Days, Interval: 1, AllowedDays: recurrence.AllowedDays{Mode: recurrence.AnyDay}, Adjustment: recurrence.NoAdjustment}, Snapshot: ProjectTemplateSnapshot{Title: "Дейлик", Tasks: []ProjectTemplateTask{}}}
			preview := value[ProjectDraftPreview](t, apiCall(t, h, "POST", "/project-drafts/preview", in, 200))
			in.PreviewToken = preview.PreviewToken
			p := *value[CommandResult](t, apiCall(t, h, "POST", "/project-drafts/apply", in, 200)).Project
			_, root := scoped32(t, s, h, p, ProjectTaskScopeInput{Scope: "this_and_following", Action: "add", Patch: json.RawMessage(`{"title":"Корень","planned_date":"2026-11-17"}`)})
			scoped32(t, s, h, p, ProjectTaskScopeInput{Scope: "this_and_following", Action: "add", ParentID: root.Task.ID, Patch: json.RawMessage(`{"title":"Ребёнок","planned_date":"2026-11-18"}`)})
			v := getSeriesTest(t, s, *p.RepeatSeriesID)
			if selectedFuture {
				p = *v.Occurrences[0].Project
			} else {
				p, _ = s.Project(ctx, p.ID)
			}
			child := p.Tasks[0]
			for _, task := range p.Tasks {
				if task.ParentTaskID != nil {
					child = task
				}
			}
			date, _ := offsetDate(*p.StartDate, ptr(2))
			raw, _ := json.Marshal(map[string]any{"planned_date": date})
			scoped32(t, s, h, p, ProjectTaskScopeInput{Scope: "this_and_following", Action: "edit", TaskID: child.ID, Patch: raw})
			v = getSeriesTest(t, s, v.ID)
			ids := map[string][]string{}
			for _, o := range v.Occurrences {
				for _, task := range o.Project.Tasks {
					ids[o.ID] = append(ids[o.ID], task.ID)
				}
			}
			rule := v.Rule.Rule
			rule.Unit = recurrence.Weeks
			change := SeriesCommand{Action: "change_rule", ExpectedRevision: v.Revision, Rule: &rule, FromCycle: ptr(int64(0)), FirstDate: "2026-11-17", TasksDateDecision: "shift"}
			pre, err := s.PreviewSeriesChange(ctx, v.ID, change)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range pre.Rows {
				if row.OccurrenceID != "" && row.Disposition != "change_project" {
					t.Errorf("untouched shared edit protected: cycle %d %s", row.CycleIndex, row.Disposition)
				}
			}
			managedApply(t, s, v.ID, change)
			v = getSeriesTest(t, s, v.ID)
			for _, o := range v.Occurrences {
				want, _ := offsetDate("2026-11-17", ptr(int(o.CycleIndex)*7))
				if *o.Project.StartDate != *want {
					t.Errorf("cycle %d start %s want %s", o.CycleIndex, *o.Project.StartDate, *want)
				}
				var gotIDs []string
				for _, task := range o.Project.Tasks {
					gotIDs = append(gotIDs, task.ID)
				}
				if !reflect.DeepEqual(gotIDs, ids[o.ID]) {
					t.Fatal("task or occurrence IDs changed")
				}
				if len(o.Project.Tasks) != 2 {
					t.Fatal("tree lost", o.Project)
				}
			}
			p, _ = s.Project(ctx, p.ID)
			date, _ = offsetDate(*p.StartDate, ptr(3))
			raw, _ = json.Marshal(map[string]any{"planned_date": date})
			scoped32(t, s, h, p, ProjectTaskScopeInput{Scope: "this_and_following", Action: "edit", TaskID: child.ID, Patch: raw})
			v = getSeriesTest(t, s, v.ID)
			for _, o := range v.Occurrences {
				if selectedFuture && *o.Project.StartDate < *p.StartDate {
					continue
				}
				var rootID string
				for _, task := range o.Project.Tasks {
					if task.ParentTaskID == nil {
						rootID = task.ID
					}
				}
				for _, task := range o.Project.Tasks {
					if task.ParentTaskID != nil {
						want, _ := offsetDate(*o.Project.StartDate, ptr(3))
						if *task.ParentTaskID != rootID || *task.PlannedDate != *want {
							t.Fatal("wrong child", task)
						}
					}
				}
			}
			countBefore := count(t, st, "tasks")
			drainSeries(t, s, MaterializeInput{SeriesID: v.ID, Through: "2027-01-01"})
			drainSeries(t, s, MaterializeInput{SeriesID: v.ID, Through: "2027-01-01"})
			if count(t, st, "tasks") != countBefore {
				t.Fatal("quota or duplicates")
			}
			lifecycleIntegrity(t, st)
		})
	}
}

// A shared edit must never adopt a baseline that includes unrelated manual work.
func TestUX31Add4SharedEditPreservesIndividualException(t *testing.T) {
	st, s, h := seriesFixture(t)
	p := beta3Draft(t, h, "shared-after-exception")
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	selected := *v.Occurrences[0].Project
	manual := selected.Tasks[0]
	apiCall(t, h, "PATCH", "/tasks/"+manual.ID, map[string]any{"expected_revision": manual.Revision, "description": "Индивидуальная заметка"}, 200)
	scoped32(t, s, h, selected, ProjectTaskScopeInput{Scope: "this_and_following", Action: "edit", TaskID: selected.Tasks[1].ID, Patch: json.RawMessage(`{"scheduled_time":"15:00"}`)})
	before, _ := s.Project(ctx, selected.ID)
	history := count(t, st, "history_events")
	v = getSeriesTest(t, s, v.ID)
	rule := v.Rule.Rule
	rule.Unit = recurrence.Days
	change := SeriesCommand{Action: "change_rule", ExpectedRevision: v.Revision, Rule: &rule, FromCycle: ptr(int64(0)), FirstDate: *p.StartDate, TasksDateDecision: "shift"}
	preview, err := s.PreviewSeriesChange(ctx, v.ID, change)
	if err != nil {
		t.Fatal(err)
	}
	protected := false
	for _, row := range preview.Rows {
		if row.OccurrenceID == *selected.OccurrenceID {
			protected = row.Disposition == "protected_user_changes"
		}
	}
	if !protected {
		t.Fatal("manual exception was adopted as shared baseline")
	}
	managedApply(t, s, v.ID, change)
	after, _ := s.Project(ctx, selected.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("protected instance changed")
	}
	if count(t, st, "history_events") < history {
		t.Fatal("history lost")
	}
	lifecycleIntegrity(t, st)
}

func TestUX31Add4SharedAddInFutureRemainsReschedulable(t *testing.T) {
	_, s, h := seriesFixture(t)
	p := beta3Draft(t, h, "future-shared-add")
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	selected := *v.Occurrences[0].Project
	var parent TaskDTO
	for _, task := range selected.Tasks {
		if task.ParentTaskID == nil {
			parent = task
			break
		}
	}
	_, result := scoped32(t, s, h, selected, ProjectTaskScopeInput{Scope: "this_and_following", Action: "add", ParentID: parent.ID, Patch: json.RawMessage(`{"title":"Новый шаг"}`)})
	v = getSeriesTest(t, s, v.ID)
	rule := v.Rule.Rule
	rule.Interval = 2
	change := SeriesCommand{Action: "change_rule", ExpectedRevision: v.Revision, Rule: &rule, FromCycle: ptr(int64(0)), FirstDate: *p.StartDate, TasksDateDecision: "shift"}
	preview, err := s.PreviewSeriesChange(ctx, v.ID, change)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range preview.Rows {
		if row.OccurrenceID == *selected.OccurrenceID && row.Disposition != "change_project" {
			t.Fatal("shared child falsely protected", row.Disposition)
		}
	}
	managedApply(t, s, v.ID, change)
	child, err := s.Task(ctx, result.Task.ID)
	if err != nil || child.ParentTaskID == nil || *child.ParentTaskID != parent.ID {
		t.Fatal("child mapping lost", err)
	}
}

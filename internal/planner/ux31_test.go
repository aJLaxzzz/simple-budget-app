package planner

import (
	"budget/internal/planner/recurrence"
	"database/sql"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func setProjectLimitTest(t *testing.T, h http.Handler, n int) {
	t.Helper()
	v := value[ProjectRepeatLimit](t, apiCall(t, h, "GET", "/settings/project-repeat-limit", nil, 200))
	apiCall(t, h, "PUT", "/settings/project-repeat-limit", map[string]any{"limit": n, "expected_revision": v.Revision}, 200)
}

func TestUX31ProjectLimitsWholeTreesSourceAndIdempotence(t *testing.T) {
	for _, n := range []int{1, 3, 5} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			db, s, h := seriesFixture(t)
			setProjectLimitTest(t, h, n)
			p := beta3Draft(t, h, "limit-source")
			if count(t, db, "projects") != n {
				t.Fatal("source counted outside quota", count(t, db, "projects"))
			}
			before := recurrenceDBSnapshot(t, db)
			drainSeries(t, s, MaterializeInput{SeriesID: *p.RepeatSeriesID})
			if before != recurrenceDBSnapshot(t, db) {
				t.Fatal("unchanged materialization wrote data")
			}
			// The source is not an occurrence row. Its closure must also refill
			// the quota, including N=1 where no future row existed beforehand.
			finishProjectTasksTest(t, s, p.ID, "2026-11-16")
			p, _ = s.Project(ctx, p.ID)
			sourceSeries := getSeriesTest(t, s, *p.RepeatSeriesID)
			revisions := map[string]int64{}
			for _, task := range p.Tasks {
				revisions[task.ID] = task.Revision
			}
			apiCall(t, h, "POST", "/projects/"+p.ID+"/close-current", map[string]any{
				"action": "complete", "tasks_decision": "complete_open", "completed_date": "2026-11-16",
				"expected_revision": p.Revision, "expected_series_revision": sourceSeries.Revision,
				"related_revisions": revisions, "request_id": "close-limit-source",
			}, 200)
			drainSeries(t, s, MaterializeInput{SeriesID: *p.RepeatSeriesID})
			sourceSeries = getSeriesTest(t, s, *p.RepeatSeriesID)
			if len(sourceSeries.Occurrences) != n {
				t.Fatal("source closure did not refill", len(sourceSeries.Occurrences))
			}
			template := createTemplateTest(t, s, cleaningSnapshot())
			series := projectSeriesTest(t, s, template, "2026-11-16", 7)
			drainSeries(t, s, MaterializeInput{SeriesID: series.ID})
			series = getSeriesTest(t, s, series.ID)
			if len(series.Occurrences) != n {
				t.Fatal(len(series.Occurrences))
			}
			for _, o := range series.Occurrences {
				if o.Project == nil || len(o.Project.Tasks) != 4 {
					t.Fatal("partial project tree", o)
				}
			}
			for _, action := range []string{"complete", "skip", "cancel"} {
				series = getSeriesTest(t, s, series.ID)
				var open TaskOccurrenceDTO
				for _, o := range series.Occurrences {
					if o.Project != nil && active(o.Project.Status) {
						open = o
						break
					}
				}
				if action == "complete" {
					finishProjectTasksTest(t, s, open.Project.ID, "2026-11-16")
				}
				in := projectOccurrenceInput(t, s, open.ID)
				in.TasksDecision = map[string]string{"complete": "complete_open", "skip": "skip_open", "cancel": "cancel_open"}[action]
				if action == "complete" {
					in.CompletedDate = ptr("2026-11-16")
				}
				occurrenceApply(t, s, open.ID, action, in)
				drainSeries(t, s, MaterializeInput{SeriesID: series.ID})
				series = getSeriesTest(t, s, series.ID)
				activeCount := 0
				for _, o := range series.Occurrences {
					if o.Project != nil && active(o.Project.Status) {
						activeCount++
					}
				}
				if activeCount != n {
					t.Fatal("closure did not refill", action, activeCount)
				}
			}
			before = recurrenceDBSnapshot(t, db)
			drainSeries(t, s, MaterializeInput{SeriesID: series.ID})
			if before != recurrenceDBSnapshot(t, db) {
				t.Fatal("duplicate/refill is not idempotent")
			}
		})
	}
}

func TestUX31OverdueAndExistingExcessPreserveAllRows(t *testing.T) {
	db, s, h := seriesFixture(t)
	setProjectLimitTest(t, h, 5)
	v := projectSeriesTest(t, s, createTemplateTest(t, s, cleaningSnapshot()), "2026-11-16", 7)
	original := getSeriesTest(t, s, v.ID)
	// Existing manually edited future rows must not be hidden or discarded.
	task := original.Occurrences[3].Project.Tasks[0]
	apiCall(t, h, "PATCH", "/tasks/"+task.ID, map[string]any{"expected_revision": task.Revision, "description": "synthetic retained exception"}, 200)
	setProjectLimitTest(t, h, 3)
	before := recurrenceDBSnapshot(t, db)
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID})
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("legacy excess altered")
	}
	if count(t, db, "projects") != 5 {
		t.Fatal("excess removed")
	}
	// The first two multiday projects become overdue. They remain in addition
	// to three current/future projects. Then one more overdue opens a slot.
	s.now = func() time.Time { return time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC) }
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID})
	if count(t, db, "projects") != 5 {
		t.Fatal("overdue classification")
	}
	s.now = func() time.Time { return time.Date(2026, 12, 4, 12, 0, 0, 0, time.UTC) }
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID})
	if count(t, db, "projects") != 6 {
		t.Fatal("overdue blocks future")
	}
	for _, o := range original.Occurrences {
		p, err := s.Project(ctx, o.Project.ID)
		if err != nil || len(p.Tasks) != 4 {
			t.Fatal("existing tree lost", err)
		}
	}
	kept, _ := s.Task(ctx, task.ID)
	if kept.Description != "synthetic retained exception" {
		t.Fatal("edit lost")
	}
}

func TestUX31LimitSettingsRevisionAndMultiDayCounting(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := value[ProjectRepeatLimit](t, apiCall(t, h, "GET", "/settings/project-repeat-limit", nil, 200))
	if v.Limit != 3 || v.Revision != 0 {
		t.Fatal(v)
	}
	setProjectLimitTest(t, h, 1)
	apiCall(t, h, "PUT", "/settings/project-repeat-limit", map[string]any{"limit": 5, "expected_revision": 0}, 409)
	apiCall(t, h, "PUT", "/settings/project-repeat-limit", map[string]any{"limit": 0, "expected_revision": 1}, 422)
	snap := cleaningSnapshot()
	snap.DueOffset = nil
	series := projectSeriesTest(t, s, createTemplateTest(t, s, snap), "2026-11-16", 30)
	s.now = func() time.Time { return time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) }
	drainSeries(t, s, MaterializeInput{SeriesID: series.ID})
	if count(t, db, "projects") != 1 {
		t.Fatal("multiday current wrongly overdue")
	}
	s.now = func() time.Time { return time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC) }
	drainSeries(t, s, MaterializeInput{SeriesID: series.ID})
	if count(t, db, "projects") != 2 {
		t.Fatal("last task date not used")
	}
	if err := db.Transaction(ctx, func(tx *sql.Tx) error { return checkIntegrity(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
}

func TestUX31CompletedCalendarKeepsPlannedDateAndProjectTree(t *testing.T) {
	_, s, h := seriesFixture(t)
	v := projectSeriesTest(t, s, createTemplateTest(t, s, cleaningSnapshot()), "2026-11-01", 30)
	o := v.Occurrences[0]
	before := projection(t, h, "2026-10-31", "2026-11-15")
	finishProjectTasksTest(t, s, o.Project.ID, "2026-11-16")
	in := projectOccurrenceInput(t, s, o.ID)
	in.TasksDecision = "complete_open"
	in.CompletedDate = ptr("2026-11-16")
	occurrenceApply(t, s, o.ID, "complete", in)
	after := projection(t, h, "2026-10-31", "2026-11-15")
	if len(after.Items) != len(before.Items) || len(after.Items) == 0 {
		t.Fatal("completed past dates lost")
	}
	for i, item := range after.Items {
		old := before.Items[i]
		if item.Status != "completed" || item.Date != old.Date || item.TaskID != old.TaskID || !reflect.DeepEqual(item.ProjectID, old.ProjectID) {
			t.Fatal("history changed", item)
		}
	}
	in = projectOccurrenceInput(t, s, o.ID)
	in.TasksDecision = "restore_closed"
	occurrenceApply(t, s, o.ID, "restore", in)
	restored := projection(t, h, "2026-10-31", "2026-11-15")
	for _, item := range restored.Items {
		if item.Status != "completed" {
			t.Fatal("project restore must retain completed task history", item)
		}
	}
}

// Legacy regression fixtures explicitly retain a large existing population.
// This models pre-limit databases; production code never removes their rows.
func legacyProjectPopulation(t *testing.T, db *Store, n int) {
	t.Helper()
	if err := db.Transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO planner_settings(key,value) VALUES('project_repeat_limit',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, n)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUX31MovedInstanceFreesOnlyOneWholeSlot(t *testing.T) {
	db, s, h := seriesFixture(t)
	setProjectLimitTest(t, h, 3)
	snapshot := cleaningSnapshot()
	snapshot.DueOffset = nil
	v := projectSeriesTest(t, s, createTemplateTest(t, s, snapshot), "2026-11-16", 7)
	original := v.Occurrences[0]
	in := projectOccurrenceInput(t, s, original.ID)
	in.PlannedDate = ptr("2026-11-01")
	in.TasksDateDecision = "shift"
	moved := occurrenceApply(t, s, original.ID, "reschedule", in)
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID})
	if count(t, db, "projects") != 4 {
		t.Fatal("overdue move must free exactly one whole slot")
	}
	kept, _ := currentOccurrence(t, s, original.ID)
	if !reflect.DeepEqual(moved, kept) {
		t.Fatal("materialization changed manually moved occurrence")
	}
	before := recurrenceDBSnapshot(t, db)
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID})
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("repeated materialization changed existing rows")
	}
}

func TestUX31LongPauseIsBoundedAndTaskSeriesUnaffected(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := projectSeriesTest(t, s, createTemplateTest(t, s, cleaningSnapshot()), "2026-11-16", 7)
	managedApply(t, s, v.ID, SeriesCommand{Action: "pause", StartDate: "2026-12-01", EndDate: "2028-12-01"})
	setProjectLimitTest(t, h, 5)
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID})
	fresh := getSeriesTest(t, s, v.ID)
	compact := 0
	for _, o := range fresh.Occurrences {
		if o.Compact {
			compact++
			if o.ProjectStatus != "skipped" {
				t.Fatal("pause is not a skip")
			}
		}
	}
	if compact == 0 || len(fresh.Occurrences) > 12 {
		t.Fatal("unbounded or absent pause incidents", len(fresh.Occurrences))
	}
	before := recurrenceDBSnapshot(t, db)
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID})
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("pause replay wrote data")
	}
	setProjectLimitTest(t, h, 1)
	regular := createSeries(t, h, seriesInput("ordinary-unchanged", "2026-11-16", "schedule", 7))
	if len(regular.Occurrences) != 9 {
		t.Fatal("project limit leaked into task series", len(regular.Occurrences))
	}
}

func TestUX31MissingFutureCalendarDoesNotUndoProjectCompletion(t *testing.T) {
	db, s, h := seriesFixture(t)
	if err := db.ImportCalendarYear(ctx, syntheticRecurrenceYear(2026)); err != nil {
		t.Fatal(err)
	}
	template := createTemplateTest(t, s, cleaningSnapshot())
	in := seriesInput("project-year-boundary", "2026-12-16", recurrence.Schedule, 7)
	in.SourceType = "project"
	in.Task = nil
	in.TemplateID = template.ID
	in.ExpectedRevision = template.Revision
	in.Rule.AllowedDays.Mode = recurrence.RussianWorkdays
	in.Rule.Adjustment = recurrence.Next
	v := createSeries(t, h, in)
	o := v.Occurrences[0]
	finishProjectTasksTest(t, s, o.Project.ID, "2026-11-16")
	close := projectOccurrenceInput(t, s, o.ID)
	close.TasksDecision = "complete_open"
	close.CompletedDate = ptr("2026-11-16")
	occurrenceApply(t, s, o.ID, "complete", close)
	result, err := s.Materialize(ctx, MaterializeInput{SeriesID: v.ID})
	if err != nil || len(result.Results) != 1 || result.Results[0].Error == nil || result.Results[0].Error.Code != "production_calendar_unavailable" {
		t.Fatal("missing calendar not reported", result, err)
	}
	project, err := s.Project(ctx, o.Project.ID)
	if err != nil || project.Status != "completed" {
		t.Fatal("refill error undid completion", err)
	}
	for _, task := range project.Tasks {
		if task.Status != "completed" {
			t.Fatal("task completion lost")
		}
	}
	if getSeriesTest(t, s, v.ID).CalculationError == nil {
		t.Fatal("series error hidden")
	}
	if err := db.ImportCalendarYear(ctx, syntheticRecurrenceYear(2027)); err != nil {
		t.Fatal(err)
	}
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID})
	if count(t, db, "projects") != 4 || getSeriesTest(t, s, v.ID).CalculationError != nil {
		t.Fatal("refill did not recover after calendar import")
	}
}

func TestUX31InstanceOnlyMoveAndUndoPreserveOtherCycles(t *testing.T) {
	for _, mode := range []recurrence.Mode{recurrence.Schedule, recurrence.Completion} {
		t.Run(string(mode), func(t *testing.T) {
			_, s, h := seriesFixture(t)
			v := createSeries(t, h, seriesInput("instance-only", "2026-11-16", mode, 7))
			before := getSeriesTest(t, s, v.ID)
			first := before.Occurrences[0]
			in := occurrenceInput(t, s, first.ID)
			in.InstanceOnly = true
			in.PlannedDate = ptr("2026-11-18")
			in.ScheduledTime = Field[string]{Set: true, Value: ptr("15:00")}
			result := value[CommandResult](t, apiCall(t, h, "POST", "/occurrences/"+first.ID+"/reschedule", in, 200))
			if *result.Occurrence.Task.ScheduledTime != "15:00" {
				t.Fatal("time missing")
			}
			drainSeries(t, s, MaterializeInput{SeriesID: v.ID})
			after := getSeriesTest(t, s, v.ID)
			for i, o := range before.Occurrences {
				if i > 0 && !reflect.DeepEqual(o.Task, after.Occurrences[i].Task) {
					t.Fatal("another cycle changed", i)
				}
			}
			undo := occurrenceInput(t, s, first.ID)
			undo.InstanceOnly = true
			undo.PlannedDate = first.Task.PlannedDate
			undo.ScheduledTime = Field[string]{Set: true, Value: nil}
			apiCall(t, h, "POST", "/occurrences/"+first.ID+"/reschedule", in, 200) // identical request is idempotent
			restored := occurrenceApply(t, s, first.ID, "reschedule", undo)
			if restored.Task.ScheduledTime != nil || *restored.Task.PlannedDate != *first.Task.PlannedDate {
				t.Fatal("undo did not restore absence of time")
			}
			stale := undo
			stale.RequestID = "stale-undo"
			apiCall(t, h, "POST", "/occurrences/"+first.ID+"/reschedule", stale, 409)
			invalid := occurrenceInput(t, s, first.ID)
			invalid.InstanceOnly = true
			invalid.CompletedDate = ptr("2026-11-16")
			apiCall(t, h, "POST", "/occurrences/"+first.ID+"/complete", invalid, 422)
		})
	}
}

package planner

import (
	"budget/internal/planner/recurrence"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func seriesFixture(t *testing.T) (*Store, *Service, http.Handler) {
	t.Helper()
	db := testStore(t)
	s := NewService(db)
	s.now = func() time.Time { return time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC) }
	return db, s, NewHandler(s)
}
func seriesInput(key, first string, mode recurrence.Mode, days int64) CreateSeriesInput {
	return CreateSeriesInput{RequestID: key, FirstDate: first, Task: &TaskTemplate{Title: "Regular task", Priority: "normal"}, Rule: recurrence.Rule{SchemaVersion: 1, Mode: mode, Unit: recurrence.Days, Interval: days, AllowedDays: recurrence.AllowedDays{Mode: recurrence.AnyDay}, Adjustment: recurrence.NoAdjustment}}
}
func createSeries(t *testing.T, h http.Handler, in CreateSeriesInput) SeriesDTO {
	t.Helper()
	return value[SeriesDTO](t, apiCall(t, h, "POST", "/series", in, 201))
}
func getSeriesTest(t *testing.T, s *Service, id string) SeriesDTO {
	t.Helper()
	v, e := s.Series(ctx, id, 100, 0)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func drainSeries(t *testing.T, s *Service, in MaterializeInput) MaterializeResult {
	t.Helper()
	for n := 0; n < 100; n++ {
		v, e := s.Materialize(ctx, in)
		if e != nil {
			t.Fatal(e)
		}
		for _, r := range v.Results {
			if r.Error != nil {
				t.Fatal(r.Error)
			}
		}
		if !v.Pending && v.NextAfterSeriesID == "" {
			return v
		}
		in.AfterSeriesID = v.NextAfterSeriesID
	}
	t.Fatal("catch-up did not terminate")
	return MaterializeResult{}
}
func completeSeriesTask(t *testing.T, h http.Handler, task TaskDTO, date string) TaskDTO {
	t.Helper()
	return value[TaskDTO](t, apiCall(t, h, "POST", "/tasks/"+task.ID+"/complete", map[string]any{"expected_revision": task.Revision, "completed_date": date}, 200))
}
func TestSeriesFirstCycleScheduleAndIdempotency(t *testing.T) {
	db, s, h := seriesFixture(t)
	in := seriesInput("weekly", "2026-11-16", recurrence.Schedule, 7)
	v := createSeries(t, h, in)
	if len(v.Occurrences) != 9 || v.Occurrences[0].BaseDate != "2026-11-16" || v.Occurrences[0].CycleIndex != 0 || v.Occurrences[8].BaseDate != "2027-01-11" {
		t.Fatal(v)
	}
	for _, o := range v.Occurrences {
		if o.Task.Status != "planned" || o.Task.OccurrenceID == nil || *o.Task.OccurrenceID != o.ID || *o.Task.SeriesID != v.ID || *o.Task.CycleIndex != o.CycleIndex {
			t.Fatal(o)
		}
	}
	before := recurrenceDBSnapshot(t, db)
	again := createSeries(t, h, in)
	if again.ID != v.ID {
		t.Fatal("duplicate series")
	}
	drainSeries(t, s, MaterializeInput{})
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("idempotent request changed existing data")
	}
	in.Task.Title = "different"
	apiCall(t, h, "POST", "/series", in, 409)
	in = seriesInput("haircut", "2026-11-16", recurrence.Schedule, 45)
	hair := createSeries(t, h, in)
	if len(hair.Occurrences) != 2 || hair.Occurrences[1].BaseDate != "2026-12-31" {
		t.Fatal(hair)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.Materialize(ctx, MaterializeInput{Through: "2027-03-01"})
			if e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	var duplicates int
	if e := db.db.QueryRow(`SELECT count(*) FROM (SELECT series_id,cycle_index,count(*) n FROM occurrences GROUP BY series_id,cycle_index HAVING n>1)`).Scan(&duplicates); e != nil || duplicates != 0 {
		t.Fatal(e, duplicates)
	}
	list := apiCall(t, h, "GET", "/series", nil, 200)
	if !strings.Contains(list.Body.String(), v.ID) {
		t.Fatal(list.Body.String())
	}
	page := value[SeriesDTO](t, apiCall(t, h, "GET", "/series/"+v.ID+"?limit=2&offset=1", nil, 200))
	if len(page.Occurrences) != 2 || !page.HasMore || page.Occurrences[0].CycleIndex != 1 {
		t.Fatal(page)
	}
}
func TestDailySeriesFillsDefaultSixtyDaysOnCreation(t *testing.T) {
	_, _, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("daily-default", "2026-11-16", recurrence.Schedule, 1))
	if len(v.Occurrences) != 60 || v.Occurrences[0].BaseDate != "2026-11-16" || v.Occurrences[59].BaseDate != "2027-01-14" {
		t.Fatal(v)
	}
}

func TestSeriesAttachExistingAndCompletedSource(t *testing.T) {
	db, s, h := seriesFixture(t)
	original := task(t, h, map[string]any{"title": "Existing", "description": "source", "planned_date": "2026-11-01", "due_date": "2026-11-05"})
	in := seriesInput("attach", "2026-11-01", recurrence.Completion, 45)
	in.Task = nil
	in.ExistingTaskID = original.ID
	in.ExpectedRevision = original.Revision
	v := createSeries(t, h, in)
	if count(t, db, "tasks") != 1 || v.Occurrences[0].Task.ID != original.ID || *v.Occurrences[0].Task.DueDate != "2026-11-05" {
		t.Fatal(v)
	}
	old := v.Occurrences[0].Task
	done := completeSeriesTask(t, h, old, "2026-11-10")
	v = getSeriesTest(t, s, v.ID)
	if len(v.Occurrences) != 2 || v.Occurrences[1].BaseDate != "2026-12-25" || *v.AnchorDate != "2026-11-10" {
		t.Fatal(v)
	}
	apiCall(t, h, "POST", "/tasks/"+old.ID+"/complete", map[string]any{"expected_revision": old.Revision, "completed_date": "2026-11-10"}, 409)
	apiCall(t, h, "POST", "/tasks/"+done.ID+"/complete", map[string]any{"expected_revision": done.Revision, "completed_date": "2026-11-10"}, 422)
	if len(getSeriesTest(t, s, v.ID).Occurrences) != 2 {
		t.Fatal("duplicate completion cycle")
	}
	// A completed ordinary task is itself cycle zero; actual completion, not its
	// old plan or request time, anchors cycle one.
	source := task(t, h, map[string]any{"title": "Already done", "planned_date": "2026-10-01"})
	source = completeSeriesTask(t, h, source, "2026-11-02")
	in = seriesInput("completed-source", "2026-10-01", recurrence.Completion, 45)
	in.Task = nil
	in.ExistingTaskID = source.ID
	in.ExpectedRevision = source.Revision
	v = createSeries(t, h, in)
	if len(v.Occurrences) != 2 || v.Occurrences[0].Task.ID != source.ID || v.Occurrences[1].BaseDate != "2026-12-17" {
		t.Fatal(v)
	}
	if len(history(t, h, "task", source.ID)) != 3 {
		t.Fatal("previous history lost")
	}
}
func TestCompletionHorizonCancellationAndAtomicSuccess(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("completion", "2026-11-16", recurrence.Completion, 90))
	drainSeries(t, s, MaterializeInput{})
	if len(getSeriesTest(t, s, v.ID).Occurrences) != 1 {
		t.Fatal("uncompleted cycle advanced")
	}
	done := completeSeriesTask(t, h, v.Occurrences[0].Task, "2026-11-15")
	v = getSeriesTest(t, s, v.ID)
	if len(v.Occurrences) != 1 || v.WaitingOccurrenceID != nil || *v.AnchorDate != "2026-11-15" {
		t.Fatal(v)
	}
	drainSeries(t, s, MaterializeInput{Through: "2027-03-01"})
	v = getSeriesTest(t, s, v.ID)
	if len(v.Occurrences) != 2 || v.Occurrences[1].BaseDate != "2027-02-13" {
		t.Fatal(v)
	}
	apiCall(t, h, "POST", "/tasks/"+done.ID+"/restore", map[string]any{"expected_revision": done.Revision}, 422)
	next := v.Occurrences[1].Task
	apiCall(t, h, "POST", "/tasks/"+next.ID+"/cancel", map[string]any{"expected_revision": next.Revision}, 200)
	v = getSeriesTest(t, s, v.ID)
	if v.State != "needs_anchor" || v.WaitingOccurrenceID != nil {
		t.Fatal(v)
	}
	snapshot := recurrenceDBSnapshot(t, db)
	drainSeries(t, s, MaterializeInput{Through: "2027-03-01"})
	if snapshot != recurrenceDBSnapshot(t, db) {
		t.Fatal("cancel advanced")
	}
	// Immediate successor and completion share operation_id and transaction.
	v = createSeries(t, h, seriesInput("immediate", "2026-11-15", recurrence.Completion, 7))
	done = completeSeriesTask(t, h, v.Occurrences[0].Task, "2026-11-16")
	v = getSeriesTest(t, s, v.ID)
	a, b := history(t, h, "task", done.ID), history(t, h, "task", v.Occurrences[1].Task.ID)
	if a[0].OperationID != b[0].OperationID || b[0].Actor != "system" || v.Occurrences[1].BaseDate != "2026-11-23" {
		t.Fatal(a, b, v)
	}
}
func TestMaterializerPreservesManualTasksSessionsAndClosedHistory(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("manual", "2026-11-16", recurrence.Schedule, 45))
	first := v.Occurrences[0].Task
	changed := value[TaskDTO](t, apiCall(t, h, "PATCH", "/tasks/"+first.ID, map[string]any{"expected_revision": first.Revision, "title": "Personal", "description": "Do not copy", "priority": "high", "planned_date": "2026-11-10", "due_date": "2026-12-01"}, 200))
	apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": changed.ID, "date": "2026-11-10"}, 201)
	second := v.Occurrences[1].Task
	completeSeriesTask(t, h, second, "2026-11-16")
	taskBefore, _ := s.Task(ctx, first.ID)
	oldHistory := history(t, h, "task", second.ID)
	drainSeries(t, s, MaterializeInput{Through: "2027-04-01"})
	taskAfter, _ := s.Task(ctx, first.ID)
	if !reflect.DeepEqual(taskBefore, taskAfter) || !reflect.DeepEqual(oldHistory, history(t, h, "task", second.ID)) || count(t, db, "sessions") != 1 {
		t.Fatal("overwrote existing state")
	}
	v = getSeriesTest(t, s, v.ID)
	if *v.Occurrences[0].ManualDate != "2026-11-10" || v.Occurrences[2].BaseDate != "2027-02-14" || v.Occurrences[2].Task.Title != "Regular task" || v.Occurrences[2].Task.Priority != "normal" {
		t.Fatal(v)
	}
}
func TestSeriesAtomicFailures(t *testing.T) {
	for _, table := range []string{"tasks", "history_events"} {
		t.Run(table, func(t *testing.T) {
			db, _, h := seriesFixture(t)
			snapshot := recurrenceDBSnapshot(t, db)
			exec(t, db, "CREATE TRIGGER fail_cycle BEFORE INSERT ON "+table+" BEGIN SELECT RAISE(ABORT,'injected'); END")
			apiCall(t, h, "POST", "/series", seriesInput("bad", "2026-11-16", recurrence.Schedule, 7), 422)
			exec(t, db, "DROP TRIGGER fail_cycle")
			if snapshot != recurrenceDBSnapshot(t, db) {
				t.Fatal("partial creation")
			}
		})
	}
	db, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("rollback-complete", "2026-11-16", recurrence.Completion, 7))
	before := recurrenceDBSnapshot(t, db)
	exec(t, db, `CREATE TRIGGER fail_cycle BEFORE INSERT ON history_events WHEN NEW.actor='system' BEGIN SELECT RAISE(ABORT,'injected'); END`)
	apiCall(t, h, "POST", "/tasks/"+v.Occurrences[0].Task.ID+"/complete", map[string]any{"expected_revision": 1, "completed_date": "2026-11-16"}, 422)
	exec(t, db, "DROP TRIGGER fail_cycle")
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("completion partly committed")
	}
	// An error in one series does not roll back another series's batch.
	good := createSeries(t, h, seriesInput("good", "2026-11-16", recurrence.Schedule, 45))
	bad := createSeries(t, h, seriesInput("bad-extension", "2026-11-16", recurrence.Schedule, 45))
	exec(t, db, fmt.Sprintf(`CREATE TRIGGER fail_cycle BEFORE INSERT ON occurrences WHEN NEW.series_id='%s' BEGIN SELECT RAISE(ABORT,'injected'); END`, bad.ID))
	r, e := s.Materialize(ctx, MaterializeInput{Through: "2027-03-01"})
	if e != nil {
		t.Fatal(e)
	}
	errors := 0
	for _, one := range r.Results {
		if one.Error != nil {
			errors++
		}
	}
	if errors != 1 || len(getSeriesTest(t, s, good.ID).Occurrences) != 3 || len(getSeriesTest(t, s, bad.ID).Occurrences) != 2 {
		t.Fatal(r)
	}
}
func TestSeriesProductionCalendarCollisionsAndAdjustments(t *testing.T) {
	db, s, h := seriesFixture(t)
	in := seriesInput("missing", "2026-11-16", recurrence.Schedule, 7)
	in.Rule.AllowedDays.Mode = recurrence.RussianWorkdays
	in.Rule.Adjustment = recurrence.Next
	before := recurrenceDBSnapshot(t, db)
	apiCall(t, h, "POST", "/series", in, 422)
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("missing calendar partly wrote")
	}
	// Completion needs the missing next year atomically, including rollback of its
	// actual completion. Importing synthetic data enables the exact same operation.
	if e := db.ImportCalendarYear(ctx, syntheticRecurrenceYear(2026)); e != nil {
		t.Fatal(e)
	}
	in = seriesInput("rf", "2026-11-16", recurrence.Completion, 45)
	in.Rule.AllowedDays.Mode = recurrence.RussianWorkdays
	in.Rule.Adjustment = recurrence.Next
	v := createSeries(t, h, in)
	if v.Occurrences[0].AdjustedDate != "2026-11-17" {
		t.Fatal(v)
	}
	before = recurrenceDBSnapshot(t, db)
	apiCall(t, h, "POST", "/tasks/"+v.Occurrences[0].Task.ID+"/complete", map[string]any{"expected_revision": 1, "completed_date": "2026-11-16"}, 422)
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("completion survived missing calendar")
	}
	if e := db.ImportCalendarYear(ctx, syntheticRecurrenceYear(2027)); e != nil {
		t.Fatal(e)
	}
	completeSeriesTask(t, h, v.Occurrences[0].Task, "2026-11-16")
	in = seriesInput("collisions", "2026-11-20", recurrence.Schedule, 1)
	in.Rule.AllowedDays.Mode = recurrence.Weekdays
	in.Rule.Adjustment = recurrence.Next
	v = createSeries(t, h, in)
	for _, o := range v.Occurrences[1:4] {
		if o.AdjustedDate != "2026-11-23" {
			t.Fatal(o)
		}
	}
	// A base date after the horizon may adjust backward into the requested range.
	in = seriesInput("backwards", "2026-11-22", recurrence.Schedule, 7)
	in.Rule.AllowedDays = recurrence.AllowedDays{Mode: recurrence.SelectedDays, Weekdays: []int{1}}
	in.Rule.Adjustment = recurrence.Previous
	v = createSeries(t, h, in)
	found := false
	for _, o := range v.Occurrences {
		if o.BaseDate == "2027-01-17" && o.AdjustedDate == "2027-01-11" {
			found = true
		}
	}
	if !found {
		t.Fatal(v)
	}
	drainSeries(t, s, MaterializeInput{})
}
func TestSeriesRestartLongDowntimeAndCalendarReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "planner.db")
	db, e := Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	s := NewService(db)
	s.now = func() time.Time { return time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC) }
	v, e := s.CreateSeries(ctx, seriesInput("offline", "2024-01-01", recurrence.Schedule, 1))
	if e != nil {
		t.Fatal(e)
	}
	id := v.ID
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	db, e = Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s = NewService(db)
	s.now = func() time.Time { return time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC) }
	first, e := s.Materialize(ctx, MaterializeInput{})
	if e != nil || !first.Pending || first.Results[0].Created > materializeCyclesPerBatch {
		t.Fatal(first, e)
	}
	drainSeries(t, s, MaterializeInput{})
	var n int
	db.db.QueryRow("SELECT count(*) FROM occurrences WHERE series_id=?", id).Scan(&n)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2027, 1, 14, 0, 0, 0, 0, time.UTC)
	if n != int(end.Sub(start).Hours()/24)+1 {
		t.Fatal("lost offline dates", n)
	}
	snapshot := recurrenceDBSnapshot(t, db)
	projection, e := s.CalendarProjection(ctx, "2027-04-01", "2027-04-30")
	if e != nil || len(projection.Items) != 0 || snapshot != recurrenceDBSnapshot(t, db) {
		t.Fatal("GET materialized", e)
	}
	drainSeries(t, s, MaterializeInput{Through: "2027-04-30"})
	projection, e = s.CalendarProjection(ctx, "2027-04-01", "2027-04-30")
	if e != nil || len(projection.Items) != 30 || projection.Items[0].SeriesID == nil || *projection.Items[0].SeriesID != id {
		t.Fatal(projection, e)
	}
	snapshot = recurrenceDBSnapshot(t, db)
	drainSeries(t, s, MaterializeInput{Through: "2027-04-30"})
	if snapshot != recurrenceDBSnapshot(t, db) {
		t.Fatal("restart duplicate/update")
	}
}
func TestSeriesSQLConstraintsAndValidation(t *testing.T) {
	db, _, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("constraints", "2026-11-16", recurrence.Completion, 7))
	exec(t, db, `INSERT INTO series_versions(id,series_id,version,effective_from,rule_json,source_snapshot_json) VALUES('new-version','`+v.ID+`',2,'2026-11-16','{}','{}')`)
	e := db.Transaction(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT INTO occurrences(id,series_id,rule_version_id,cycle_index,base_date,adjusted_date,task_link) VALUES('duplicate',?,'new-version',0,'2026-11-16','2026-11-16','duplicate')`, v.ID)
		return e
	})
	if e == nil {
		t.Fatal("cross-version duplicate allowed")
	}
	for _, query := range []string{
		`INSERT INTO occurrences(id,series_id,rule_version_id,cycle_index,base_date,adjusted_date,task_link) VALUES('orphan','` + v.ID + `','new-version',100,'2026-11-16','2026-11-16','orphan')`,
		`INSERT INTO occurrences(id,series_id,rule_version_id,cycle_index,base_date,adjusted_date) VALUES('unbound','` + v.ID + `','new-version',100,'2026-11-16','2026-11-16')`,
		`UPDATE tasks SET occurrence_id=NULL WHERE id='` + v.Occurrences[0].Task.ID + `'`,
		`UPDATE occurrences SET cycle_index=99 WHERE id='` + v.Occurrences[0].ID + `'`,
		`DELETE FROM tasks WHERE id='` + v.Occurrences[0].Task.ID + `'`,
	} {
		if e = db.Transaction(ctx, func(tx *sql.Tx) error { _, e := tx.Exec(query); return e }); e == nil {
			t.Fatal("constraint missing", query)
		}
	}
	apiCall(t, h, "GET", "/materialize", nil, 405)
	apiCall(t, h, "PATCH", "/series/"+v.ID, map[string]any{}, 405)
	apiCall(t, h, "POST", "/materialize", MaterializeInput{Through: "2026-02-30"}, 422)
	apiCall(t, h, "POST", "/materialize", MaterializeInput{Through: "2030-01-01"}, 422)
	in := seriesInput("invalid", "2026-11-16", recurrence.Schedule, 1)
	in.Rule.Interval = 0
	apiCall(t, h, "POST", "/series", in, 422)
	raw, _ := json.Marshal(seriesInput("extra", "2026-11-16", recurrence.Schedule, 1))
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	fields["task"].(map[string]any)["status"] = "completed"
	apiCall(t, h, "POST", "/series", fields, 400)
	fields["task"].(map[string]any)["status"] = nil
	fields["source_type"] = "project"
	apiCall(t, h, "POST", "/series", fields, 400)
}
func TestMaterializerWorkerStartsAndStops(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("worker", "2025-01-01", recurrence.Schedule, 1))
	before := count(t, db, "occurrences")
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); s.RunMaterializer(workerCtx, time.Hour) }()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("worker did not run on startup")
		case <-ticker.C:
			current := getSeriesTest(t, s, v.ID)
			if current.NextCycle > int64(before) {
				cancel()
				<-done
				return
			}
		}
	}
}

func TestRecurringChildCompletionAndSessionsRollbackTogether(t *testing.T) {
	db, s, h := seriesFixture(t)
	p := project(t, h, "P")
	parent := task(t, h, map[string]any{"title": "Parent", "project_id": p.ID})
	child := task(t, h, map[string]any{"title": "Child", "project_id": p.ID, "parent_task_id": parent.ID, "planned_date": "2026-11-16"})
	in := seriesInput("child-series", "2026-11-16", recurrence.Completion, 7)
	in.Task = nil
	in.ExistingTaskID = child.ID
	in.ExpectedRevision = child.Revision
	v := createSeries(t, h, in)
	child = v.Occurrences[0].Task
	session := value[SessionDTO](t, apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": child.ID, "date": "2026-11-20"}, 201))
	body := map[string]any{"expected_revision": parent.Revision, "completed_date": "2026-11-16", "children_decision": "complete", "related_revisions": map[string]int64{child.ID: child.Revision}, "sessions_decision": "cancel", "session_revisions": map[string]int64{session.ID: session.Revision}}
	before := recurrenceDBSnapshot(t, db)
	exec(t, db, `CREATE TRIGGER fail_child_cycle BEFORE INSERT ON tasks BEGIN SELECT RAISE(ABORT,'injected'); END`)
	apiCall(t, h, "POST", "/tasks/"+parent.ID+"/complete", body, 422)
	exec(t, db, "DROP TRIGGER fail_child_cycle")
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("parent/child/sessions partly completed")
	}
	apiCall(t, h, "POST", "/tasks/"+parent.ID+"/complete", body, 200)
	v = getSeriesTest(t, s, v.ID)
	if len(v.Occurrences) != 2 || v.Occurrences[0].Task.Status != "completed" {
		t.Fatal(v)
	}
	histories := [][]HistoryDTO{history(t, h, "task", parent.ID), history(t, h, "task", child.ID), history(t, h, "session", session.ID), history(t, h, "task", v.Occurrences[1].Task.ID)}
	for _, events := range histories {
		if events[0].OperationID != histories[0][0].OperationID {
			t.Fatal("compound operation lost", histories)
		}
	}
}
func TestCompletionManualMoveDoesNotBecomeAnchor(t *testing.T) {
	_, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("manual-completion", "2026-11-16", recurrence.Completion, 45))
	first := v.Occurrences[0].Task
	moved := value[TaskDTO](t, apiCall(t, h, "POST", "/tasks/"+first.ID+"/reschedule", map[string]any{"expected_revision": first.Revision, "planned_date": "2026-12-01"}, 200))
	drainSeries(t, s, MaterializeInput{Through: "2027-03-01"})
	current := getSeriesTest(t, s, v.ID)
	if len(current.Occurrences) != 1 || current.AnchorDate != nil {
		t.Fatal("manual move confirmed an anchor")
	}
	moved = value[TaskDTO](t, apiCall(t, h, "PATCH", "/tasks/"+moved.ID, map[string]any{"expected_revision": moved.Revision, "status": "in_progress"}, 200))
	completeSeriesTask(t, h, moved, "2026-11-15")
	current = getSeriesTest(t, s, v.ID)
	if current.Occurrences[1].BaseDate != "2026-12-30" {
		t.Fatal(current)
	}
}
func TestMaterializerVersionMigrationAndFailureRollback(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprint(duplicate), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "v3.db")
			db, e := sql.Open("sqlite", path)
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			if _, e = db.Exec("PRAGMA foreign_keys=ON"); e != nil {
				t.Fatal(e)
			}
			if e = migrate(ctx, db, migrations[:3]); e != nil {
				t.Fatal(e)
			}
			sqlText := `INSERT INTO series(id,source_type,anchor_mode) VALUES('s','task','schedule');
 INSERT INTO series_versions(id,series_id,version,effective_from,rule_json,source_snapshot_json) VALUES('v1','s',1,'2026-01-01','{}','{}'),('v2','s',2,'2026-02-01','{}','{}');
 INSERT INTO occurrences(id,series_id,rule_version_id,cycle_index,base_date,adjusted_date) VALUES('o1','s','v1',0,'2026-01-01','2026-01-01');
 INSERT INTO tasks(id,title,occurrence_id) VALUES('t1','Historical task','o1');`
			if duplicate {
				sqlText += `INSERT INTO occurrences(id,series_id,rule_version_id,cycle_index,base_date,adjusted_date) VALUES('o2','s','v2',0,'2026-02-01','2026-02-01');INSERT INTO tasks(id,title,occurrence_id) VALUES('t2','Other version','o2');`
			}
			if _, e = db.Exec(sqlText); e != nil {
				t.Fatal(e)
			}
			e = migrate(ctx, db, migrations)
			if duplicate && e == nil || !duplicate && e != nil {
				t.Fatal("unexpected migration result", e)
			}
			var version int
			db.QueryRow("PRAGMA user_version").Scan(&version)
			if duplicate {
				if version != 3 {
					t.Fatal("migration partly applied")
				}
				var n int
				db.QueryRow("SELECT count(*) FROM occurrences").Scan(&n)
				if n != 2 {
					t.Fatal("history deduplicated")
				}
			} else {
				if version != len(migrations) {
					t.Fatal(version)
				}
				var title, link string
				if e = db.QueryRow("SELECT t.title,o.task_link FROM tasks t JOIN occurrences o ON o.id=t.occurrence_id").Scan(&title, &link); e != nil || title != "Historical task" || link != "o1" {
					t.Fatal(title, link, e)
				}
				if e = checkIntegrity(ctx, db); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}

func TestMaterializerPeriodicExtensionWithTestClock(t *testing.T) {
	_, s, h := seriesFixture(t)
	var clock atomic.Value
	clock.Store(s.now())
	s.now = func() time.Time { return clock.Load().(time.Time) }
	v := createSeries(t, h, seriesInput("periodic", "2026-11-16", recurrence.Schedule, 7))
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); s.RunMaterializer(workerCtx, 10*time.Millisecond) }()
	defer func() { cancel(); <-done }()
	clock.Store(clock.Load().(time.Time).AddDate(0, 0, 7))
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("periodic horizon not extended")
		case <-ticker.C:
			current := getSeriesTest(t, s, v.ID)
			if current.NextCycle > v.NextCycle {
				return
			}
		}
	}
}

func TestConcurrentCreateSeriesRequestKey(t *testing.T) {
	db, s, _ := seriesFixture(t)
	in := seriesInput("parallel-create", "2026-11-16", recurrence.Completion, 45)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := s.CreateSeries(ctx, in)
			if e != nil {
				t.Error(e)
				return
			}
			ids <- v.ID
		}()
	}
	wg.Wait()
	close(ids)
	expected := ""
	for id := range ids {
		if expected != "" && expected != id {
			t.Fatal("different series")
		}
		expected = id
	}
	if count(t, db, "series") != 1 || count(t, db, "tasks") != 1 || count(t, db, "occurrences") != 1 {
		t.Fatal("duplicate concurrent creation")
	}
}
func TestCompletionSQLAllowsOnlyOneWaitingTask(t *testing.T) {
	db, _, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("single-waiting", "2026-11-16", recurrence.Completion, 7))
	e := db.Transaction(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT INTO occurrences(id,series_id,rule_version_id,cycle_index,base_date,adjusted_date,task_link,pending_completion) VALUES('another',?,?,2,'2026-11-23','2026-11-23','another',0)`, v.ID, v.RuleVersionID)
		if e != nil {
			return e
		}
		_, e = tx.Exec(`INSERT INTO tasks(id,title,occurrence_id) VALUES('another-task','Extra active task','another')`)
		return e
	})
	if e == nil {
		t.Fatal("second waiting task allowed")
	}
	if count(t, db, "occurrences") != 1 || count(t, db, "tasks") != 1 {
		t.Fatal("partial duplicate")
	}
}

func TestEarlyCompletionUsesActualAnchorBeforeFirstPlannedDate(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("early-completion", "2026-12-01", recurrence.Completion, 7))
	completeSeriesTask(t, h, v.Occurrences[0].Task, "2026-11-16")
	current := getSeriesTest(t, s, v.ID)
	if len(current.Occurrences) != 2 || current.Occurrences[1].BaseDate != "2026-11-23" {
		t.Fatal(current)
	}
	var from string
	if e := db.db.QueryRow("SELECT effective_from FROM series_versions WHERE id=?", v.RuleVersionID).Scan(&from); e != nil || from > current.Occurrences[1].BaseDate {
		t.Fatal("cycle outside version scope", from, e)
	}
}

func TestSeriesSnapshotRestoresCursorAndWaitingCycle(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("snapshot", "2026-11-16", recurrence.Completion, 7))
	before := recurrenceDBSnapshot(t, db)
	path := filepath.Join(t.TempDir(), "snapshot.sqlite")
	if e := db.Export(ctx, path); e != nil {
		t.Fatal(e)
	}
	completeSeriesTask(t, h, v.Occurrences[0].Task, "2026-11-16")
	if e := db.Restore(ctx, path); e != nil {
		t.Fatal(e)
	}
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("runtime/history not restored")
	}
	drainSeries(t, s, MaterializeInput{})
	current := getSeriesTest(t, s, v.ID)
	if current.NextCycle != 1 || len(current.Occurrences) != 1 || current.WaitingOccurrenceID == nil {
		t.Fatal(current)
	}
	completeSeriesTask(t, h, current.Occurrences[0].Task, "2026-11-16")
	if len(getSeriesTest(t, s, v.ID).Occurrences) != 2 {
		t.Fatal("restored series did not continue")
	}
}

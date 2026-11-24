package planner

import (
	"budget/internal/planner/recurrence"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestBeta2MinimumAnnualAndManualSkip(t *testing.T) {
	db, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	for _, mode := range []recurrence.Mode{recurrence.Schedule, recurrence.Completion} {
		in := seriesInput("annual-"+string(mode), "2026-11-24", mode, 1)
		in.Rule.Unit = recurrence.Years
		v := createSeries(t, h, in)
		if len(v.Occurrences) != 3 {
			t.Fatalf("%s: %d real rows", mode, len(v.Occurrences))
		}
		for i, date := range []string{"2026-11-24", "2027-11-24", "2028-11-24"} {
			if *v.Occurrences[i].Task.PlannedDate != date {
				t.Fatal(v.Occurrences)
			}
		}
		before := v.HorizonDate
		_, e := s.EnsureUpcoming(ctx, v.ID)
		if e != nil {
			t.Fatal(e)
		}
		v = getSeriesTest(t, s, v.ID)
		if v.HorizonDate != before {
			t.Fatal("minimum expanded horizon")
		}
		occurrenceApply(t, s, v.Occurrences[0].ID, "skip", occurrenceInput(t, s, v.Occurrences[0].ID))
		incidents, e := s.Incidents(ctx, v.ID, "skipped", "processed", 100, 0)
		if e != nil || len(incidents) != 1 || incidents[0].Resolution != "acknowledged" {
			t.Fatal(incidents, e)
		}
		v = getSeriesTest(t, s, v.ID)
		if mode == recurrence.Completion && v.State != "needs_anchor" {
			t.Fatal(v.State)
		}
		if mode == recurrence.Schedule && len(v.Occurrences) != 4 {
			t.Fatal("missing replacement", len(v.Occurrences))
		}
	}
	if e := checkIntegrity(ctx, db.db); e != nil {
		t.Fatal(e)
	}
}
func disableBeta2(t *testing.T, s *Service, id, keep, policy string) DisableInput {
	t.Helper()
	v := getSeriesTest(t, s, id)
	in := DisableInput{ExpectedRevision: v.Revision, KeepID: keep, Future: policy, Confirm: true, RequestID: "disable-" + id}
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		p, e := s.disablePreviewTx(tx, id, in)
		in.PreviewToken = p.PreviewToken
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	return in
}
func TestBeta2DisablePreservesSourceAndHistory(t *testing.T) {
	db, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	in := seriesInput("disableannual", "2026-11-24", recurrence.Schedule, 1)
	in.Rule.Unit = recurrence.Years
	v := createSeries(t, h, in)
	id := v.Occurrences[0].Task.ID
	command := disableBeta2(t, s, v.ID, id, "delete")
	apply := func() (CommandResult, error) {
		var out CommandResult
		e := db.Transaction(ctx, func(tx *sql.Tx) error { var e error; out, e = s.disableTx(ctx, tx, v.ID, command); return e })
		return out, e
	}
	out, e := apply()
	if e != nil {
		t.Fatal(e)
	}
	if out.Series.State != "disabled" {
		t.Fatal(out)
	}
	if _, e = apply(); e != nil {
		t.Fatal("idempotent replay", e)
	}
	task, e := s.Task(ctx, id)
	if e != nil || task.OccurrenceID != nil || *task.PlannedDate != "2026-11-24" {
		t.Fatal(task, e)
	}
	v = getSeriesTest(t, s, v.ID)
	if len(v.Occurrences) != 1 {
		t.Fatal("future not removed", len(v.Occurrences))
	}
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID})
	if len(getSeriesTest(t, s, v.ID).Occurrences) != 1 {
		t.Fatal("disabled materialized")
	}
	snapshot := filepath.Join(t.TempDir(), "disabled.db")
	if e = db.Export(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	if e = db.Restore(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	p, e := s.PreviewDeletion(ctx, "series", v.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.DeleteForever(ctx, "series", v.ID, DeletionInput{ExpectedRevision: p.Revision, PreviewToken: p.PreviewToken, Confirm: true, RequestID: "delete-disabled"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Task(ctx, id); e != nil {
		t.Fatal("standalone source deleted", e)
	}
	var history int
	if e = db.db.QueryRow("SELECT count(*) FROM history_events WHERE task_id=?", id).Scan(&history); e != nil || history < 2 {
		t.Fatal("lost history", history, e)
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	if e = db.Export(ctx, backup); e != nil {
		t.Fatal(e)
	}
	if e = db.Restore(ctx, backup); e != nil {
		t.Fatal(e)
	}
	if e = checkIntegrity(ctx, db.db); e != nil {
		t.Fatal(e)
	}
}
func TestBeta2DisableProtectsChangesAndRollsBackConflict(t *testing.T) {
	db, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	in := seriesInput("protect-disable", "2026-11-24", recurrence.Completion, 42)
	v := createSeries(t, h, in)
	command := disableBeta2(t, s, v.ID, v.Occurrences[0].Task.ID, "delete")
	t2 := v.Occurrences[1].Task
	apiCall(t, h, "PATCH", "/tasks/"+t2.ID, map[string]any{"expected_revision": t2.Revision, "title": "Личная правка"}, 200)
	e := db.Transaction(ctx, func(tx *sql.Tx) error { _, e := s.disableTx(ctx, tx, v.ID, command); return e })
	if e == nil {
		t.Fatal("stale preview applied")
	}
	if getSeriesTest(t, s, v.ID).State != "active" {
		t.Fatal("partial mutation")
	}
	command = disableBeta2(t, s, v.ID, v.Occurrences[0].Task.ID, "delete")
	e = db.Transaction(ctx, func(tx *sql.Tx) error { _, e := s.disableTx(ctx, tx, v.ID, command); return e })
	if e != nil {
		t.Fatal(e)
	}
	saved, e := s.Task(ctx, t2.ID)
	if e != nil || saved.Title != "Личная правка" || saved.OccurrenceID != nil {
		t.Fatal(saved, e)
	}
	if e = checkIntegrity(ctx, db.db); e != nil {
		t.Fatal(e)
	}
}
func TestBeta2BatchBirthdaysAndNestedConversion(t *testing.T) {
	db, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	var p ProjectDTO
	json.Unmarshal(apiCall(t, h, "POST", "/projects", map[string]any{"title": "ДР"}, 201).Body.Bytes(), &p)
	in := BatchRepeatInput{ExpectedRevisions: map[string]int64{}, Rule: seriesInput("x", "2026-11-24", recurrence.Schedule, 1).Rule, RequestID: "batch-birthday"}
	in.Rule.Unit = recurrence.Years
	for _, date := range []string{"2026-11-24", "2027-09-13", "2027-10-28", "2027-11-04"} {
		var task TaskDTO
		json.Unmarshal(apiCall(t, h, "POST", "/tasks", map[string]any{"title": "ДР " + date, "project_id": p.ID, "planned_date": date}, 201).Body.Bytes(), &task)
		in.TaskIDs = append(in.TaskIDs, task.ID)
		in.ExpectedRevisions[task.ID] = task.Revision
	}
	var preview BatchRepeatPreview
	json.Unmarshal(apiCall(t, h, "POST", "/batch-recurrence/preview", in, 200).Body.Bytes(), &preview)
	in.PreviewToken = preview.PreviewToken
	first := apiCall(t, h, "POST", "/batch-recurrence/apply", in, 200)
	second := apiCall(t, h, "POST", "/batch-recurrence/apply", in, 200)
	if first.Body.String() != second.Body.String() {
		t.Fatal("batch not idempotent")
	}
	p, e := s.Project(ctx, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Tasks) != 12 || p.RepeatSeriesID != nil {
		t.Fatal("birthday project", len(p.Tasks))
	}
	source := ProjectSourceInput{ExpectedRevision: p.Revision, AnchorDate: "2026-11-24", FirstDate: "2027-11-24", Rule: &in.Rule, RequestID: "convert-birthday"}
	if _, e = s.ProjectTemplatePreview(ctx, p.ID, source); e == nil {
		t.Fatal("nested repetitions allowed")
	}
	source.IndependentDecision = "convert"
	pr, e := s.ProjectTemplatePreview(ctx, p.ID, source)
	if e != nil {
		t.Fatal(e)
	}
	if len(pr.Snapshot.Tasks) != 4 || len(pr.Excluded) != 8 {
		t.Fatal("duplicate template", len(pr.Snapshot.Tasks), len(pr.Excluded))
	}
	source.PreviewToken = pr.PreviewToken
	out, e := s.SaveProjectTemplate(ctx, p.ID, source)
	if e != nil {
		t.Fatal(e)
	}
	p, e = s.Project(ctx, p.ID)
	if e != nil || p.RepeatSeriesID == nil {
		t.Fatal(p, e)
	}
	command := disableBeta2(t, s, out.Series.ID, p.ID, "delete")
	e = db.Transaction(ctx, func(tx *sql.Tx) error { _, e := s.disableTx(ctx, tx, out.Series.ID, command); return e })
	if e != nil {
		t.Fatal(e)
	}
	p, e = s.Project(ctx, p.ID)
	if e != nil || p.RepeatSeriesID != nil {
		t.Fatal("source not preserved", e)
	}
	for _, id := range in.TaskIDs {
		task, e := s.Task(ctx, id)
		if e != nil || task.ProjectID == nil || *task.ProjectID != p.ID {
			t.Fatal("source task lost", id, e)
		}
	}
	if e = checkIntegrity(ctx, db.db); e != nil {
		t.Fatal(e)
	}
}

func TestBeta2QuickCompletionUndoDynamic(t *testing.T) {
	db, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	v := createSeries(t, h, seriesInput("undo-dynamic", "2026-11-24", recurrence.Completion, 42))
	o := v.Occurrences[0]
	in := occurrenceInput(t, s, o.ID)
	in.CompletedDate = strBeta("2026-11-23")
	out, e := s.OccurrenceCommand(ctx, o.ID, "complete", in)
	if e != nil || out.Undo == nil {
		t.Fatal(out, e)
	}
	undo := occurrenceInput(t, s, o.ID)
	undo.CompletionRequestID = in.RequestID
	_, e = s.OccurrenceCommand(ctx, o.ID, "undo-complete", undo)
	if e != nil {
		t.Fatal(e)
	}
	v = getSeriesTest(t, s, v.ID)
	if *v.WaitingOccurrenceID != o.ID || v.Occurrences[0].Task.Status != "planned" {
		t.Fatal("not current", v)
	}
	for i, date := range []string{"2026-11-24", "2027-01-05", "2027-02-16"} {
		if *v.Occurrences[i].Task.PlannedDate != date {
			t.Fatal("bad restored chain", v.Occurrences)
		}
	}
	if e = checkIntegrity(ctx, db.db); e != nil {
		t.Fatal(e)
	}
	in = occurrenceInput(t, s, o.ID)
	in.CompletedDate = strBeta("2026-11-24")
	_, e = s.OccurrenceCommand(ctx, o.ID, "complete", in)
	if e != nil {
		t.Fatal(e)
	}
	task := getSeriesTest(t, s, v.ID).Occurrences[1].Task
	apiCall(t, h, "PATCH", "/tasks/"+task.ID, map[string]any{"expected_revision": task.Revision, "title": "Изменено после завершения"}, 200)
	undo = occurrenceInput(t, s, o.ID)
	undo.CompletionRequestID = in.RequestID
	if _, e = s.OccurrenceCommand(ctx, o.ID, "undo-complete", undo); e == nil {
		t.Fatal("unsafe undo")
	}
}

func TestBeta2BatchRollbackAndDeletionRefill(t *testing.T) {
	db, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	in := BatchRepeatInput{ExpectedRevisions: map[string]int64{}, Rule: seriesInput("x", "2026-11-24", recurrence.Schedule, 1).Rule, RequestID: "rollback-batch"}
	in.Rule.Unit = recurrence.Years
	for _, title := range []string{"ДР мамы", "ДР папы"} {
		task := value[TaskDTO](t, apiCall(t, h, "POST", "/tasks", map[string]any{"title": title, "planned_date": "2026-11-24"}, 201))
		in.TaskIDs = append(in.TaskIDs, task.ID)
		in.ExpectedRevisions[task.ID] = task.Revision
	}
	preview := value[BatchRepeatPreview](t, apiCall(t, h, "POST", "/batch-recurrence/preview", in, 200))
	in.PreviewToken = preview.PreviewToken
	exec(t, db, `CREATE TRIGGER fail_second_repeat BEFORE INSERT ON series_versions WHEN json_extract(NEW.source_snapshot_json,'$.title')='ДР папы' BEGIN SELECT RAISE(ABORT,'injected'); END`)
	apiCall(t, h, "POST", "/batch-recurrence/apply", in, 422)
	exec(t, db, "DROP TRIGGER fail_second_repeat")
	for _, id := range in.TaskIDs {
		task, e := s.Task(ctx, id)
		if e != nil || task.SeriesID != nil || task.Revision != in.ExpectedRevisions[id] {
			t.Fatal("partial batch", task, e)
		}
	}
	apiCall(t, h, "POST", "/batch-recurrence/apply", in, 200)
	task, _ := s.Task(ctx, in.TaskIDs[0])
	v := getSeriesTest(t, s, *task.SeriesID)
	removed := v.Occurrences[1]
	removeTest(t, h, "occurrences", removed.ID)
	v = getSeriesTest(t, s, v.ID)
	if len(v.Occurrences) != 3 {
		t.Fatal("minimum after delete", len(v.Occurrences))
	}
	for _, o := range v.Occurrences {
		if o.CycleIndex == removed.CycleIndex {
			t.Fatal("removed cycle returned")
		}
	}
	backup := filepath.Join(t.TempDir(), "restart.db")
	if e := db.Export(ctx, backup); e != nil {
		t.Fatal(e)
	}
	restart, e := Open(ctx, backup)
	if e != nil {
		t.Fatal(e)
	}
	defer restart.Close()
	service := NewService(restart)
	service.now = s.now
	drainSeries(t, service, MaterializeInput{SeriesID: v.ID})
	if len(getSeriesTest(t, service, v.ID).Occurrences) != 3 {
		t.Fatal("restart duplicates")
	}
	lifecycleIntegrity(t, restart)
}
func TestBeta2ArchivedDisableAndEndBoundary(t *testing.T) {
	db, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	in := seriesInput("ended", "2026-11-24", recurrence.Schedule, 1)
	in.Rule.Unit = recurrence.Years
	in.EndDate = strBeta("2027-11-24")
	v := createSeries(t, h, in)
	if len(v.Occurrences) != 2 {
		t.Fatal("end exceeded")
	}
	exec(t, db, "UPDATE series SET state='archived' WHERE id=?", v.ID)
	cmd := disableBeta2(t, s, v.ID, v.Occurrences[0].Task.ID, "keep")
	e := db.Transaction(ctx, func(tx *sql.Tx) error { _, e := s.disableTx(ctx, tx, v.ID, cmd); return e })
	if e != nil {
		t.Fatal(e)
	}
	for _, o := range v.Occurrences {
		task, e := s.Task(ctx, o.Task.ID)
		if e != nil || task.OccurrenceID != nil {
			t.Fatal("archived detachment", task, e)
		}
	}
	lifecycleIntegrity(t, db)
}

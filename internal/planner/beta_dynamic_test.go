package planner

import (
	"budget/internal/planner/recurrence"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestBetaDynamicRealChainRescheduleCompleteAndRestart(t *testing.T) {
	db, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 30, 12, 0, 0, 0, time.UTC) }
	v := createSeries(t, h, seriesInput("dynamic-hair", "2026-11-24", recurrence.Completion, 42))
	if len(v.Occurrences) != 3 {
		t.Fatalf("need three actual tasks: %d", len(v.Occurrences))
	}
	ids := []string{v.Occurrences[0].Task.ID, v.Occurrences[1].Task.ID, v.Occurrences[2].Task.ID}
	check := func(dates ...string) {
		t.Helper()
		v = getSeriesTest(t, s, v.ID)
		for i, date := range dates {
			if *v.Occurrences[i].Task.PlannedDate != date {
				t.Fatalf("cycle %d: %s != %s", i, *v.Occurrences[i].Task.PlannedDate, date)
			}
			if i < 3 && v.Occurrences[i].Task.ID != ids[i] {
				t.Fatal("ID changed")
			}
		}
	}
	check("2026-11-24", "2027-01-05", "2027-02-16")
	apiCall(t, h, "PATCH", "/tasks/"+ids[1], map[string]any{"expected_revision": v.Occurrences[1].Task.Revision, "title": "Моя стрижка", "color_id": "#123456", "icon_id": "app:tooth", "due_date": "2027-01-20"}, 200)
	var historyID, historyPayload string
	if e := db.db.QueryRow("SELECT id,payload_json FROM history_events WHERE task_id=? ORDER BY rowid LIMIT 1", ids[1]).Scan(&historyID, &historyPayload); e != nil {
		t.Fatal(e)
	}

	if v.Occurrences[0].Task.Dynamic || !v.Occurrences[1].Task.Dynamic {
		t.Fatal("current/dependent roles")
	}
	in := occurrenceInput(t, s, v.Occurrences[0].ID)
	in.PlannedDate = strBeta("2026-11-30")
	occurrenceApply(t, s, v.Occurrences[0].ID, "reschedule", in)
	check("2026-11-30", "2027-01-11", "2027-02-22")
	in = occurrenceInput(t, s, v.Occurrences[0].ID)
	in.CompletedDate = strBeta("2026-11-29")
	occurrenceApply(t, s, v.Occurrences[0].ID, "complete", in)
	check("2026-11-30", "2027-01-10", "2027-02-21", "2027-04-04")
	own := v.Occurrences[1].Task
	if own.Title != "Моя стрижка" || *own.ColorID != "#123456" || *own.IconID != "app:tooth" || *own.DueDate != "2027-01-20" {
		t.Fatal("recalculation lost own fields")
	}
	var payload string
	var events int
	if e := db.db.QueryRow("SELECT payload_json FROM history_events WHERE id=?", historyID).Scan(&payload); e != nil || payload != historyPayload {
		t.Fatal("old history rewritten", e)
	}
	if e := db.db.QueryRow("SELECT count(*) FROM history_events WHERE task_id=? AND action='dynamic_recalculated'", ids[1]).Scan(&events); e != nil || events != 2 {
		t.Fatal("missing recalculation history", e, events)
	}
	if v.Occurrences[1].Task.Dynamic || !v.Occurrences[2].Task.Dynamic {
		t.Fatal("next was not promoted")
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot.db")
	if e := db.Export(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	if e := db.Restore(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	restarted := NewService(db)
	restarted.now = s.now
	drainSeries(t, restarted, MaterializeInput{SeriesID: v.ID})
	check("2026-11-30", "2027-01-10", "2027-02-21", "2027-04-04")
	c, e := s.CalendarProjection(ctx, "2027-02-01", "2027-02-28")
	if e != nil {
		t.Fatal(e)
	}
	if len(c.Items) != 1 || !c.Items[0].Dynamic {
		t.Fatal("calendar lost dynamic marker")
	}
	lifecycleIntegrity(t, db)
}

func TestBetaDynamicManualConflictEarlyCompletionAndDeletion(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("dynamic-manual", "2026-11-01", recurrence.Completion, 10))
	first, next := v.Occurrences[0], v.Occurrences[1]
	in := occurrenceInput(t, s, next.ID)
	in.PlannedDate = strBeta("2026-11-19")
	occurrenceApply(t, s, next.ID, "reschedule", in)
	in = occurrenceInput(t, s, first.ID)
	in.PlannedDate = strBeta("2026-11-10")
	apiCall(t, h, "POST", "/occurrences/"+first.ID+"/reschedule", in, 409)
	v = getSeriesTest(t, s, v.ID)
	if *v.Occurrences[0].Task.PlannedDate != "2026-11-01" {
		t.Fatal("partial recalc")
	}
	in.DynamicDecision = "keep_manual"
	occurrenceApply(t, s, first.ID, "reschedule", in)
	v = getSeriesTest(t, s, v.ID)
	if *v.Occurrences[1].Task.PlannedDate != "2026-11-19" {
		t.Fatal("manual override lost")
	}
	in = occurrenceInput(t, s, next.ID)
	in.CompletedDate = strBeta("2026-11-16")
	apiCall(t, h, "POST", "/occurrences/"+next.ID+"/complete", in, 409)
	in.BranchDecision = "historical_only"
	occurrenceApply(t, s, next.ID, "complete", in)
	v = getSeriesTest(t, s, v.ID)
	if *v.WaitingOccurrenceID != first.ID {
		t.Fatal("second chain")
	}
	restore := occurrenceInput(t, s, next.ID)
	occurrenceApply(t, s, next.ID, "restore", restore)
	restored := getSeriesTest(t, s, v.ID)
	if *restored.WaitingOccurrenceID != first.ID || !restored.Occurrences[1].Task.Dynamic || restored.Occurrences[1].Task.Status != "planned" {
		t.Fatal("restoring a future row forked chain")
	}
	removeTest(t, h, "occurrences", v.Occurrences[2].ID)
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID})
	v = getSeriesTest(t, s, v.ID)
	for _, o := range v.Occurrences {
		if o.CycleIndex == 2 {
			t.Fatal("deleted cycle returned")
		}
	}
	in = occurrenceInput(t, s, first.ID)
	occurrenceApply(t, s, first.ID, "cancel", in)
	v = getSeriesTest(t, s, v.ID)
	if v.State != "needs_anchor" || v.WaitingOccurrenceID != nil {
		t.Fatal("invented anchor")
	}
	v = *managedApply(t, s, v.ID, SeriesCommand{Action: "reanchor", ExpectedRevision: v.Revision, AnchorDate: "2026-11-16"}).Series
	if v.WaitingOccurrenceID == nil {
		t.Fatal("no current after anchor")
	}
	var n int
	if e := db.Transaction(ctx, func(tx *sql.Tx) error {
		return tx.QueryRow("SELECT count(*) FROM occurrences WHERE series_id=? AND pending_completion=1", v.ID).Scan(&n)
	}); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	lifecycleIntegrity(t, db)
}

func TestBetaMoveExistingTreeAndRecurringSeries(t *testing.T) {
	db, s, h := seriesFixture(t)
	project := value[ProjectDTO](t, apiCall(t, h, "POST", "/projects", map[string]any{"title": "Дни рождения"}, 201))
	v := createSeries(t, h, seriesInput("birthdays", "2026-11-16", recurrence.Schedule, 365))
	first := v.Occurrences[0].Task
	in := TaskMoveInput{TaskIDs: []string{first.ID}, Scope: "series", ProjectID: &project.ID, ExpectedRevisions: map[string]int64{first.ID: first.Revision}, RequestID: "move-birthday"}
	p, e := s.PreviewTaskMove(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	in.PreviewToken = p.PreviewToken
	if _, e = s.MoveTasks(ctx, in); e != nil {
		t.Fatal(e)
	}
	if _, e = s.MoveTasks(ctx, in); e != nil {
		t.Fatal(e)
	}
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID, Through: "2028-11-16"})
	v = getSeriesTest(t, s, v.ID)
	for _, o := range v.Occurrences {
		if o.Task.ProjectID == nil || *o.Task.ProjectID != project.ID {
			t.Fatal("source assignment lost")
		}
	}
	if *v.Template.ProjectID != project.ID {
		t.Fatal("future source")
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot.db")
	if e = db.Export(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	if e = db.Restore(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	removeTest(t, h, "series", v.ID)
	lifecycleIntegrity(t, db)
}

func TestBetaMoveBatchPreviewConflictAndRollback(t *testing.T) {
	db, s, h := seriesFixture(t)
	old := project(t, h, "Старый проект")
	target := project(t, h, "Дом")
	parent := task(t, h, map[string]any{"title": "Разобрать шкаф", "project_id": old.ID, "color_id": "rose"})
	child := task(t, h, map[string]any{"title": "Полка", "project_id": old.ID, "parent_task_id": parent.ID})
	other := task(t, h, map[string]any{"title": "Купить продукты"})
	session := value[SessionDTO](t, apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": parent.ID, "date": "2026-11-18"}, 201))
	in := TaskMoveInput{TaskIDs: []string{parent.ID, other.ID}, ProjectID: &target.ID, Scope: "instance", ExpectedRevisions: map[string]int64{parent.ID: parent.Revision, other.ID: other.Revision}, RequestID: "batch-move"}
	p, e := s.PreviewTaskMove(ctx, in)
	if e != nil || len(p.Tasks) != 3 {
		t.Fatal(p, e)
	}
	in.PreviewToken = p.PreviewToken
	exec(t, db, "CREATE TRIGGER fail_move BEFORE UPDATE OF project_id ON tasks WHEN NEW.title='Купить продукты' BEGIN SELECT RAISE(ABORT,'injected'); END")
	if _, e = s.MoveTasks(ctx, in); e == nil {
		t.Fatal("injected failure ignored")
	}
	exec(t, db, "DROP TRIGGER fail_move")
	current, _ := s.Task(ctx, parent.ID)
	if *current.ProjectID != old.ID {
		t.Fatal("partial batch")
	}
	if _, e = s.MoveTasks(ctx, in); e != nil {
		t.Fatal(e)
	}
	if _, e = s.MoveTasks(ctx, in); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{parent.ID, child.ID, other.ID} {
		current, _ := s.Task(ctx, id)
		if *current.ProjectID != target.ID {
			t.Fatal(current)
		}
	}
	saved := value[SessionDTO](t, apiCall(t, h, "GET", "/sessions/"+session.ID, nil, 200))
	if saved.TaskID != parent.ID {
		t.Fatal("session lost")
	}
	current, _ = s.Task(ctx, parent.ID)
	if *current.ColorID != "rose" {
		t.Fatal("own color lost")
	}
	lifecycleIntegrity(t, db)
}

func TestBetaBirthdaysAndWeeklyCleaningComposition(t *testing.T) {
	db, s, h := seriesFixture(t)
	p := project(t, h, "Дни рождения")
	for i, first := range []string{"2026-09-13", "2026-11-24"} {
		in := seriesInput([]string{"mom", "alina"}[i], first, recurrence.Schedule, 1)
		in.Rule.Unit = recurrence.Years
		in.Task.ProjectID = &p.ID
		v := createSeries(t, h, in)
		drainSeries(t, s, MaterializeInput{SeriesID: v.ID, Through: "2028-11-16"})
		v = getSeriesTest(t, s, v.ID)
		for _, o := range v.Occurrences {
			if *o.Task.ProjectID != p.ID || o.AdjustedDate[5:] != first[5:] {
				t.Fatal("birthday shifted")
			}
		}
	}
	cleaning := project(t, h, "Уборка квартиры")
	for i, title := range []string{"Пропылесосить", "Помыть полы", "Протереть пыль"} {
		date := "2026-11-21"
		if i == 2 {
			date = "2026-11-22"
		}
		task(t, h, map[string]any{"title": title, "project_id": cleaning.ID, "planned_date": date})
	}
	rule := seriesInput("", "", recurrence.Schedule, 7).Rule
	input := ProjectSourceInput{ExpectedRevision: cleaning.Revision, AnchorDate: "2026-11-21", FirstDate: "2026-11-28", Rule: &rule, RequestID: "weekly-cleaning"}
	preview, e := s.ProjectTemplatePreview(ctx, cleaning.ID, input)
	if e != nil {
		t.Fatal(e)
	}
	if len(preview.Items) == 0 {
		t.Fatal("no future composition")
	}
	for _, t0 := range preview.Items[0].Project.Tasks {
		expected := "2026-11-28"
		if t0.Title == "Протереть пыль" {
			expected = "2026-11-29"
		}
		if *t0.PlannedDate != expected {
			t.Fatal(t0)
		}
	}
	input.PreviewToken = preview.PreviewToken
	result, e := s.SaveProjectTemplate(ctx, cleaning.ID, input)
	if e != nil {
		t.Fatal(e)
	}
	v := getSeriesTest(t, s, result.Series.ID)
	if v.Occurrences[0].Project.ID == v.Occurrences[1].Project.ID {
		t.Fatal("projects not independent")
	}
	for _, projectID := range []string{cleaning.ID, v.Occurrences[0].Project.ID} {
		bad := seriesInput("double-child-"+projectID, "2026-11-28", recurrence.Schedule, 7)
		bad.Task.ProjectID = &projectID
		apiCall(t, h, "POST", "/series", bad, 409)

	}
	conflictInput := ProjectSourceInput{ExpectedRevision: p.Revision, AnchorDate: "2026-11-16", FirstDate: "2026-11-23", Rule: &rule}
	if _, e = s.ProjectTemplatePreview(ctx, p.ID, conflictInput); e == nil {
		t.Fatal("independent repetition copied into repeating project")
	}
	lifecycleIntegrity(t, db)
}

func TestBetaDynamicBoundedCalendarExtensionAndReopen(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("dynamic-calendar-extension", "2026-11-16", recurrence.Completion, 1))
	first := v.Occurrences[0].Task.ID
	// A calendar explicitly opened inside the default horizon also gets real dates.
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID, Through: "2026-11-30"})
	month, e := s.CalendarProjection(ctx, "2026-11-16", "2026-11-30")
	if e != nil || len(month.Items) != 15 {
		t.Fatalf("near calendar capped: %d %v", len(month.Items), e)
	}
	r, e := s.Materialize(ctx, MaterializeInput{SeriesID: v.ID, Through: "2027-05-01"})
	if e != nil || !r.Pending || r.Results[0].Created != materializeCyclesPerBatch {
		t.Fatalf("bounded first batch: %#v %v", r, e)
	}
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID, Through: "2027-05-01"})
	var count, current int
	if e = db.db.QueryRow("SELECT count(*),sum(pending_completion) FROM occurrences WHERE series_id=?", v.ID).Scan(&count, &current); e != nil {
		t.Fatal(e)
	}
	if count != 167 || current != 1 {
		t.Fatalf("chain %d, current %d", count, current)
	}
	snapshot := filepath.Join(t.TempDir(), "restart.db")
	if e = db.Export(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(ctx, snapshot)
	if e != nil {
		t.Fatal(e)
	}
	if e = reopened.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e = Open(ctx, snapshot)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	restarted := NewService(reopened)
	restarted.now = s.now
	drainSeries(t, restarted, MaterializeInput{SeriesID: v.ID, Through: "2027-05-01"})
	var after int
	if e = reopened.db.QueryRow("SELECT count(*) FROM occurrences WHERE series_id=?", v.ID).Scan(&after); e != nil || after != count {
		t.Fatalf("reopen duplicates %d %v", after, e)
	}
	if getSeriesTest(t, restarted, v.ID).Occurrences[0].Task.ID != first {
		t.Fatal("current ID changed")
	}
	c, e := restarted.CalendarProjection(ctx, "2027-04-01", "2027-04-30")
	if e != nil || len(c.Items) != 30 {
		t.Fatalf("calendar capped: %d %v", len(c.Items), e)
	}
	for _, item := range c.Items {
		if !item.Dynamic {
			t.Fatal("dynamic marker lost")
		}
	}
	lifecycleIntegrity(t, reopened)
}

func TestBetaRussianWeekdaysAPIUsesImportedCalendar(t *testing.T) {
	db, _, h := seriesFixture(t)
	if e := db.ImportCalendarYear(ctx, syntheticRecurrenceYear(2026)); e != nil {
		t.Fatal(e)
	}
	in := previewInput()
	in.Rule.Interval = 1
	in.Rule.AllowedDays.Mode = recurrence.RussianWeekdays
	in.Rule.Adjustment = recurrence.Next
	in.Anchor.Date = "2026-11-20"
	in.Count = 1
	result := value[recurrence.PreviewResult](t, apiCall(t, h, "POST", "/recurrence/preview", in, 200))
	if result.Items[0].AdjustedDate != "2026-11-23" || len(result.Calendars) != 1 {
		t.Fatalf("working Saturday must be excluded: %#v", result)
	}
	in.Anchor.Date = "2026-12-31"
	apiCall(t, h, "POST", "/recurrence/preview", in, 422)
}

func TestBetaMovingParentKeepsRecurringChildAssignment(t *testing.T) {
	db, s, h := seriesFixture(t)
	old := project(t, h, "Личные дела")
	target := project(t, h, "Дом")
	parent := task(t, h, map[string]any{"title": "Уход за домом", "project_id": old.ID})
	in := seriesInput("regular-child", "2026-11-16", recurrence.Completion, 42)
	in.Task.ProjectID = &old.ID
	in.Task.ParentTaskID = &parent.ID
	series := createSeries(t, h, in)
	move := TaskMoveInput{TaskIDs: []string{parent.ID}, ProjectID: &target.ID, Scope: "instance", ExpectedRevisions: map[string]int64{parent.ID: parent.Revision}, RequestID: "move-parent-recurring"}
	preview, e := s.PreviewTaskMove(ctx, move)
	if e != nil {
		t.Fatal(e)
	}
	move.PreviewToken = preview.PreviewToken
	if _, e = s.MoveTasks(ctx, move); e != nil {
		t.Fatal(e)
	}
	cmd := occurrenceInput(t, s, series.Occurrences[0].ID)
	cmd.CompletedDate = strBeta("2026-11-16")
	occurrenceApply(t, s, series.Occurrences[0].ID, "complete", cmd)
	series = getSeriesTest(t, s, series.ID)
	if len(series.Occurrences) != 4 || *series.Template.ProjectID != target.ID {
		t.Fatal("child source not moved")
	}
	for _, o := range series.Occurrences {
		if *o.Task.ProjectID != target.ID || *o.Task.ParentTaskID != parent.ID {
			t.Fatal("child tree detached")
		}
	}
	lifecycleIntegrity(t, db)
}

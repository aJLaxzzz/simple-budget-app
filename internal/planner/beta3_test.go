package planner

import (
	"budget/internal/planner/recurrence"
	"database/sql"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func beta3Draft(t *testing.T, h http.Handler, key string) ProjectDTO {
	t.Helper()
	in := ProjectDraftInput{RequestID: key, AnchorDate: "2026-11-27", HasStart: true, ProjectMode: "sequential", Rule: &recurrence.Rule{SchemaVersion: 1, Mode: recurrence.Schedule, Unit: recurrence.Weeks, Interval: 1, AllowedDays: recurrence.AllowedDays{Mode: recurrence.AnyDay}, Adjustment: recurrence.NoAdjustment}, Snapshot: ProjectTemplateSnapshot{Title: "Уборка", DueOffset: ptr(2), Tasks: []ProjectTemplateTask{{Key: "a", Title: "Посудомойка", PlannedOffset: ptr(0)}, {Key: "b", Title: "Пылесос", PlannedOffset: ptr(1), DueOffset: ptr(2)}, {Key: "c", Title: "Полы", PlannedOffset: ptr(2)}}}}
	preview := value[ProjectDraftPreview](t, apiCall(t, h, "POST", "/project-drafts/preview", in, 200))
	if *preview.Next.Tasks[0].PlannedDate != "2026-12-04" || *preview.Next.Tasks[1].PlannedDate != "2026-12-05" || *preview.Next.Tasks[2].PlannedDate != "2026-12-06" {
		t.Fatal(preview)
	}
	in.PreviewToken = preview.PreviewToken
	out := value[CommandResult](t, apiCall(t, h, "POST", "/project-drafts/apply", in, 200))
	again := value[CommandResult](t, apiCall(t, h, "POST", "/project-drafts/apply", in, 200))
	if out.Project.ID != again.Project.ID {
		t.Fatal("duplicate")
	}
	return *out.Project
}
func TestBeta3SequentialManualClosureAndCleanup(t *testing.T) {
	db, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	p := beta3Draft(t, h, "beta3-cleaning")
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	if !v.Occurrences[0].Project.Preliminary {
		t.Fatal("future active")
	}
	for _, task := range p.Tasks {
		apiCall(t, h, "POST", "/tasks/"+task.ID+"/complete", map[string]any{"expected_revision": task.Revision, "completed_date": "2026-11-24", "request_id": "done-" + task.ID}, 200)
	}
	p, _ = s.Project(ctx, p.ID)
	if p.Status != "planned" || p.Progress.Percent != 100 {
		t.Fatal(p)
	}
	apiCall(t, h, "POST", "/projects/"+p.ID+"/complete", map[string]any{"expected_revision": p.Revision}, 200)
	next, _ := s.Project(ctx, v.Occurrences[0].Project.ID)
	if next.Preliminary || next.CurrentProjectID == nil || *next.CurrentProjectID != next.ID {
		t.Fatal(next)
	}
	plan := value[DeletionPreview](t, apiCall(t, h, "GET", "/series/"+v.ID+"/delete-preview", nil, 200))
	if len(plan.Blockers) > 0 {
		t.Fatal(plan)
	}
	in := DeletionInput{ExpectedRevision: plan.Revision, PreviewToken: plan.PreviewToken, Confirm: true, RequestID: "delete-beta3"}
	apiCall(t, h, "POST", "/series/"+v.ID+"/delete", in, 200)
	apiCall(t, h, "POST", "/series/"+v.ID+"/delete", in, 200)
	if e := db.Transaction(ctx, func(tx *sql.Tx) error {
		for _, table := range []string{"series", "projects", "tasks", "project_templates", "project_repeat_state"} {
			var n int
			if e := tx.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil {
				return e
			}
			if n != 0 {
				t.Fatalf("%s orphan %d", table, n)
			}
		}
		rows, e := tx.Query("PRAGMA foreign_key_check")
		if e != nil {
			return e
		}
		defer rows.Close()
		if rows.Next() {
			t.Fatal("FK")
		}
		var integrity string
		if e = tx.QueryRow("PRAGMA integrity_check").Scan(&integrity); e != nil {
			return e
		}
		if integrity != "ok" {
			t.Fatal(integrity)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestBeta3OffsetsProtectedRepairAndRollback(t *testing.T) {
	_, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	p := beta3Draft(t, h, "beta3-repair")
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	snapshot := *v.ProjectTemplate
	snapshot.Tasks[0].PlannedOffset = ptr(4)
	in := CompositionInput{ExpectedRevision: v.Revision, Snapshot: &snapshot}
	preview := value[CompositionPreview](t, apiCall(t, h, "POST", "/series/"+v.ID+"/composition-preview", in, 200))
	if preview.Rows[0].After == nil {
		t.Fatal(preview.Rows[0].Reason)
	}
	in.PreviewToken = preview.PreviewToken
	in.RequestID = "repair-a"
	apiCall(t, h, "POST", "/series/"+v.ID+"/composition-apply", in, 200)
	old := v.Occurrences[0].Project.Tasks[0]
	for _, task := range v.Occurrences[0].Project.Tasks {
		if task.Title == snapshot.Tasks[0].Title {
			old = task
		}
	}
	changed, _ := s.Task(ctx, old.ID)
	if *changed.PlannedDate != "2026-12-08" {
		t.Fatal(changed)
	}
	v = getSeriesTest(t, s, v.ID)
	fix := CompositionInput{ExpectedRevision: v.Revision, SourceID: p.ID, AnchorDate: "2026-11-27"}
	pr := value[CompositionPreview](t, apiCall(t, h, "POST", "/series/"+v.ID+"/composition-preview", fix, 200))
	fix.PreviewToken = pr.PreviewToken
	fix.RequestID = "repair-b"
	apiCall(t, h, "POST", "/series/"+v.ID+"/composition-apply", fix, 200)
	changed, _ = s.Task(ctx, old.ID)
	if *changed.PlannedDate != "2026-12-04" {
		t.Fatal(changed)
	}
	apiCall(t, h, "POST", "/series/"+v.ID+"/composition-apply", CompositionInput{ExpectedRevision: 1, Snapshot: &snapshot, PreviewToken: pr.PreviewToken, RequestID: "stale"}, 409)
}

func TestBeta3ModeOrderDetachRestoreAndDeletion(t *testing.T) {
	db, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	p := beta3Draft(t, h, "persist-beta3")
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	mode := ProjectModeInput{ExpectedRevision: v.Revision, Mode: "independent", RequestID: "mode-a"}
	preview := value[ProjectModePreview](t, apiCall(t, h, "POST", "/series/"+v.ID+"/project-preview", mode, 200))
	mode.PreviewToken = preview.PreviewToken
	apiCall(t, h, "POST", "/series/"+v.ID+"/project-apply", mode, 200)
	apiCall(t, h, "POST", "/series/"+v.ID+"/project-apply", mode, 200)
	next, _ := s.Project(ctx, v.Occurrences[0].Project.ID)
	if next.Preliminary {
		t.Fatal(next)
	}
	ids := []string{p.Tasks[2].ID, p.Tasks[0].ID, p.Tasks[1].ID}
	revs := map[string]int64{}
	for _, task := range p.Tasks {
		revs[task.ID] = task.Revision
	}
	order := ProjectOrderInput{Mutation: Mutation{ExpectedRevision: p.Revision, RelatedRevisions: revs}, TaskIDs: ids, RequestID: "order-a"}
	apiCall(t, h, "POST", "/projects/"+p.ID+"/order", order, 200)
	apiCall(t, h, "POST", "/projects/"+p.ID+"/order", order, 200)
	path := t.TempDir() + "/snapshot.db"
	if e := db.Export(ctx, path); e != nil {
		t.Fatal(e)
	}
	if e := db.Restore(ctx, path); e != nil {
		t.Fatal(e)
	}
	for i, id := range ids {
		task, e := s.Task(ctx, id)
		if e != nil || task.SortOrder != i {
			t.Fatal(task, e)
		}
	}
	v = getSeriesTest(t, s, v.ID)
	in := DisableInput{ExpectedRevision: v.Revision, KeepID: p.ID, Future: "delete", Confirm: true, RequestID: "disable-beta3"}
	pre := value[DisablePreview](t, apiCall(t, h, "POST", "/series/"+v.ID+"/disable-preview", in, 200))
	in.PreviewToken = pre.PreviewToken
	apiCall(t, h, "POST", "/series/"+v.ID+"/disable", in, 200)
	plan := value[DeletionPreview](t, apiCall(t, h, "GET", "/series/"+v.ID+"/delete-preview", nil, 200))
	apiCall(t, h, "POST", "/series/"+v.ID+"/delete", DeletionInput{ExpectedRevision: plan.Revision, PreviewToken: plan.PreviewToken, RequestID: "delete-disabled-beta3", Confirm: true}, 200)
	survived, e := s.Project(ctx, p.ID)
	if e != nil || len(survived.Tasks) != 3 || survived.RepeatSeriesID != nil {
		t.Fatal(survived, e)
	}
}
func TestBeta3DeleteCurrentAdvancesAndDoesNotRematerialize(t *testing.T) {
	_, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	p := beta3Draft(t, h, "delete-current-beta3")
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	mode := ProjectModeInput{ExpectedRevision: v.Revision, Mode: "sequential", CurrentID: v.Occurrences[0].Project.ID, RequestID: "choose-current"}
	preview := value[ProjectModePreview](t, apiCall(t, h, "POST", "/series/"+v.ID+"/project-preview", mode, 200))
	mode.PreviewToken = preview.PreviewToken
	apiCall(t, h, "POST", "/series/"+v.ID+"/project-apply", mode, 200)
	o := v.Occurrences[0]
	plan := value[DeletionPreview](t, apiCall(t, h, "GET", "/occurrences/"+o.ID+"/delete-preview", nil, 200))
	apiCall(t, h, "POST", "/occurrences/"+o.ID+"/delete", DeletionInput{ExpectedRevision: plan.Revision, PreviewToken: plan.PreviewToken, RequestID: "delete-current", Confirm: true}, 200)
	next, _ := s.Project(ctx, v.Occurrences[1].Project.ID)
	if next.Preliminary || next.CurrentProjectID == nil || *next.CurrentProjectID != next.ID {
		t.Fatal(next)
	}
	apiCall(t, h, "GET", "/projects/"+o.Project.ID, nil, 404)
}
func TestBeta3AtomicDraftRollback(t *testing.T) {
	db, _, h := seriesFixture(t)
	input := ProjectDraftInput{AnchorDate: "2026-11-27", Snapshot: ProjectTemplateSnapshot{Title: "Без частичного сохранения", Tasks: []ProjectTemplateTask{{Key: "one", Title: "Обычная задача"}}}}
	preview := value[ProjectDraftPreview](t, apiCall(t, h, "POST", "/project-drafts/preview", input, 200))
	input.PreviewToken = preview.PreviewToken
	input.RequestID = "rollback-beta3"
	if e := db.Transaction(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec(`CREATE TRIGGER beta3_fail BEFORE INSERT ON tasks BEGIN SELECT RAISE(ABORT,'intentional test fault'); END`)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	apiCall(t, h, "POST", "/project-drafts/apply", input, 422)
	db.Transaction(ctx, func(tx *sql.Tx) error {
		var n int
		tx.QueryRow(`SELECT count(*) FROM projects`).Scan(&n)
		if n != 0 {
			t.Fatal("partial project")
		}
		return nil
	})
}

func TestBeta3ElevenAnnualRoutinesAndMixedProgress(t *testing.T) {
	_, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	p := project(t, h, "Дни рождения")
	for i := 0; i < 11; i++ {
		in := seriesInput("birthday-beta3-"+strconv.Itoa(i), "2026-12-01", recurrence.Schedule, 1)
		in.Rule.Unit = recurrence.Years
		in.Task.ProjectID = &p.ID
		createSeries(t, h, in)
	}
	p, e := s.Project(ctx, p.ID)
	if e != nil || len(p.Tasks) != 33 || p.Routines == nil || p.Routines.Count != 11 || p.Routines.Ordinary.Total != 0 {
		t.Fatal(p, e)
	}
	task(t, h, map[string]any{"title": "Заказать подарок", "project_id": p.ID})
	p, _ = s.Project(ctx, p.ID)
	if p.Routines.Ordinary.Total != 1 || p.Routines.Count != 11 {
		t.Fatal(p.Routines)
	}
}
func TestBeta3CompositionPreservesManualDatesAndFutureOrder(t *testing.T) {
	_, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	p := beta3Draft(t, h, "order-future-beta3")
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	protected := v.Occurrences[1].Project.Tasks[0]
	apiCall(t, h, "PATCH", "/tasks/"+protected.ID, map[string]any{"expected_revision": protected.Revision, "planned_date": "2026-12-20"}, 200)
	snapshot := *v.ProjectTemplate
	snapshot.Tasks[0], snapshot.Tasks[2] = snapshot.Tasks[2], snapshot.Tasks[0]
	in := CompositionInput{ExpectedRevision: v.Revision, Snapshot: &snapshot, RequestID: "composition-order-beta3"}
	preview := value[CompositionPreview](t, apiCall(t, h, "POST", "/series/"+v.ID+"/composition-preview", in, 200))
	if preview.Rows[1].After != nil {
		t.Fatal("manual project overwritten")
	}
	in.PreviewToken = preview.PreviewToken
	apiCall(t, h, "POST", "/series/"+v.ID+"/composition-apply", in, 200)
	changed, _ := s.Task(ctx, protected.ID)
	if *changed.PlannedDate != "2026-12-20" {
		t.Fatal(changed)
	}
	updated, _ := s.Project(ctx, v.Occurrences[0].Project.ID)
	for _, task := range updated.Tasks {
		if task.Title == snapshot.Tasks[0].Title && task.SortOrder != 0 {
			t.Fatal("future order lost")
		}
	}
}

func TestBeta3SourceSkipIsExplicitAtomicAndRestorable(t *testing.T) {
	_, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	p := beta3Draft(t, h, "skip-source-beta3")
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	in := map[string]any{"action": "skip", "expected_revision": p.Revision, "expected_series_revision": v.Revision, "related_revisions": map[string]int64{}, "request_id": "skip-source"}
	apiCall(t, h, "POST", "/projects/"+p.ID+"/close-current", in, 422)
	fresh, _ := s.Project(ctx, p.ID)
	if fresh.Status != "planned" {
		t.Fatal("partial skip")
	}
	revs := map[string]int64{}
	for _, task := range p.Tasks {
		revs[task.ID] = task.Revision
	}
	in["related_revisions"] = revs
	in["tasks_decision"] = "skip_open"
	result := value[CommandResult](t, apiCall(t, h, "POST", "/projects/"+p.ID+"/close-current", in, 200))
	apiCall(t, h, "POST", "/projects/"+p.ID+"/close-current", in, 200)
	if result.Project.Status != "skipped" {
		t.Fatal(result)
	}
	for _, task := range result.Project.Tasks {
		if task.Status != "skipped" || task.CompletedDate != nil {
			t.Fatal(task)
		}
	}
	next, _ := s.Project(ctx, v.Occurrences[0].Project.ID)
	if next.Preliminary {
		t.Fatal("did not advance")
	}
	v = getSeriesTest(t, s, v.ID)
	apiCall(t, h, "POST", "/projects/"+p.ID+"/close-current", map[string]any{"action": "restore", "expected_revision": result.Project.Revision, "expected_series_revision": v.Revision, "request_id": "restore-source"}, 200)
	restored, _ := s.Project(ctx, p.ID)
	if restored.Status != "planned" || !restored.Preliminary {
		t.Fatal(restored)
	}
}

func TestBeta3CompositionRemovalRequiresExplicitDecision(t *testing.T) {
	_, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	p := beta3Draft(t, h, "composition-remove")
	v := getSeriesTest(t, s, *p.RepeatSeriesID)
	snapshot := *v.ProjectTemplate
	snapshot.Tasks = snapshot.Tasks[:2]
	in := CompositionInput{ExpectedRevision: v.Revision, Snapshot: &snapshot}
	apiCall(t, h, "POST", "/series/"+v.ID+"/composition-preview", in, 409)
	in.RemovedTasksDecision = "cancel"
	pre := value[CompositionPreview](t, apiCall(t, h, "POST", "/series/"+v.ID+"/composition-preview", in, 200))
	in.PreviewToken = pre.PreviewToken
	in.RequestID = "confirm-composition-remove"
	apiCall(t, h, "POST", "/series/"+v.ID+"/composition-apply", in, 200)
	next, _ := s.Project(ctx, v.Occurrences[0].Project.ID)
	if len(next.Tasks) != 3 {
		t.Fatal("removed task history lost", next)
	}
	cancelled := 0
	for _, task := range next.Tasks {
		if task.Status == "cancelled" {
			cancelled++
		}
	}
	if cancelled != 1 {
		t.Fatal("removed task not cancelled", next)
	}
	source, _ := s.Project(ctx, p.ID)
	for _, task := range source.Tasks {
		if task.Status != "planned" {
			t.Fatal("source changed", source)
		}
	}
}

func TestBeta3ProjectDraftKeepsOriginalMonthlyAnchor(t *testing.T) {
	_, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC) }
	in := ProjectDraftInput{RequestID: "month-end-project", AnchorDate: "2026-01-31", HasStart: true, ProjectMode: "sequential", Rule: &recurrence.Rule{SchemaVersion: 1, Mode: recurrence.Schedule, Unit: recurrence.Months, Interval: 1, AllowedDays: recurrence.AllowedDays{Mode: recurrence.AnyDay}, Adjustment: recurrence.NoAdjustment}, Snapshot: ProjectTemplateSnapshot{Title: "Ежемесячные дела", Tasks: []ProjectTemplateTask{{Key: "a", Title: "Проверка", PlannedOffset: ptr(0)}}}}
	pre := value[ProjectDraftPreview](t, apiCall(t, h, "POST", "/project-drafts/preview", in, 200))
	if pre.FirstDate != "2026-02-28" {
		t.Fatal(pre)
	}
	in.PreviewToken = pre.PreviewToken
	out := value[CommandResult](t, apiCall(t, h, "POST", "/project-drafts/apply", in, 200))
	v := getSeriesTest(t, s, *out.Project.RepeatSeriesID)
	if v.Occurrences[0].CycleIndex != 1 || v.Occurrences[0].AdjustedDate != "2026-02-28" {
		t.Fatal(v.Occurrences)
	}
	err := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		c, _, e := calculateStoredCycle(ctx, tx, v, 2, nil)
		if e == nil && c.AdjustedDate != "2026-03-31" {
			t.Fatal(c)
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
}

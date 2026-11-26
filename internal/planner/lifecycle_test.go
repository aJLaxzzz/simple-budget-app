package planner

import (
	"budget/internal/planner/recurrence"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func deleteInput(t *testing.T, h http.Handler, kind, id string) DeletionInput {
	t.Helper()
	p := value[DeletionPreview](t, apiCall(t, h, "GET", "/"+kind+"/"+id+"/delete-preview", nil, 200))
	key, _ := operation()
	return DeletionInput{ExpectedRevision: p.Revision, PreviewToken: p.PreviewToken, RequestID: key, Confirm: true}
}
func removeTest(t *testing.T, h http.Handler, kind, id string) {
	t.Helper()
	in := deleteInput(t, h, kind, id)
	r := value[CommandResult](t, apiCall(t, h, "POST", "/"+kind+"/"+id+"/delete", in, 200))
	if r.Deleted == nil || r.Deleted.ID != id {
		t.Fatal(r)
	}
	apiCall(t, h, "POST", "/"+kind+"/"+id+"/delete", in, 200)
}
func lifecycleIntegrity(t *testing.T, db *Store) {
	t.Helper()
	if e := db.Transaction(ctx, func(tx *sql.Tx) error {
		var ok string
		if e := tx.QueryRow("PRAGMA integrity_check").Scan(&ok); e != nil {
			return e
		}
		if ok != "ok" {
			t.Fatal(ok)
		}
		rows, e := tx.Query("PRAGMA foreign_key_check")
		if e != nil {
			return e
		}
		defer rows.Close()
		if rows.Next() {
			t.Fatal("foreign key violation")
		}
		return rows.Err()
	}); e != nil {
		t.Fatal(e)
	}
}
func TestLifecycleUnarchivePreservesStatusAndHistory(t *testing.T) {
	db, _, h := seriesFixture(t)
	for _, cancel := range []bool{false, true} {
		p := project(t, h, "Архивный проект")
		child := task(t, h, map[string]any{"title": "Сохранить", "project_id": p.ID, "color_id": "rose"})
		if cancel {
			p = value[ProjectDTO](t, apiCall(t, h, "POST", "/projects/"+p.ID+"/cancel", map[string]any{"expected_revision": p.Revision}, 200))
		}
		p = value[ProjectDTO](t, apiCall(t, h, "POST", "/projects/"+p.ID+"/archive", map[string]any{"expected_revision": p.Revision}, 200))
		n := len(history(t, h, "project", p.ID))
		key, _ := operation()
		in := DeletionInput{ExpectedRevision: p.Revision, RequestID: key}
		r := value[CommandResult](t, apiCall(t, h, "POST", "/projects/"+p.ID+"/unarchive", in, 200))
		if r.Project.ID != p.ID || r.Project.ArchivedAt != nil || r.Project.Status != p.Status {
			t.Fatal(r)
		}
		apiCall(t, h, "POST", "/projects/"+p.ID+"/unarchive", in, 200)
		if len(history(t, h, "project", p.ID)) != n+1 {
			t.Fatal("history")
		}
		apiCall(t, h, "GET", "/tasks/"+child.ID, nil, 200)
		in.RequestID += "stale"
		apiCall(t, h, "POST", "/projects/"+p.ID+"/unarchive", in, 409)
	}
	lifecycleIntegrity(t, db)
}
func TestLifecycleTaskTreeSessionsAndPreviewRace(t *testing.T) {
	db, _, h := seriesFixture(t)
	p := project(t, h, "Дом")
	root := task(t, h, map[string]any{"title": "Комнаты", "project_id": p.ID})
	child := task(t, h, map[string]any{"title": "Полки", "project_id": p.ID, "parent_task_id": root.ID})
	apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": child.ID, "date": "2026-11-18"}, 201)
	in := deleteInput(t, h, "tasks", root.ID)
	task(t, h, map[string]any{"title": "Ещё полка", "project_id": p.ID, "parent_task_id": root.ID})
	apiCall(t, h, "POST", "/tasks/"+root.ID+"/delete", in, 409)
	in = deleteInput(t, h, "tasks", root.ID)
	bad := in
	bad.Confirm = false
	apiCall(t, h, "POST", "/tasks/"+root.ID+"/delete", bad, 422)
	removeTest(t, h, "tasks", root.ID)
	if count(t, db, "tasks") != 0 || count(t, db, "sessions") != 0 {
		t.Fatal("tree left")
	}
	apiCall(t, h, "GET", "/projects/"+p.ID, nil, 200)
	removeTest(t, h, "projects", p.ID)
	lifecycleIntegrity(t, db)
}
func TestLifecycleRollbackAndIdempotencyScope(t *testing.T) {
	db, _, h := seriesFixture(t)
	p := project(t, h, "Удаляемый проект")
	x := task(t, h, map[string]any{"title": "Задача", "project_id": p.ID})
	in := deleteInput(t, h, "projects", p.ID)
	db.Transaction(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec("CREATE TRIGGER test_delete_failure BEFORE DELETE ON projects BEGIN SELECT RAISE(ABORT,'injected'); END")
		return e
	})
	apiCall(t, h, "POST", "/projects/"+p.ID+"/delete", in, 422)
	apiCall(t, h, "GET", "/tasks/"+x.ID, nil, 200)
	if len(history(t, h, "task", x.ID)) == 0 {
		t.Fatal("history lost on rollback")
	}
	db.Transaction(ctx, func(tx *sql.Tx) error { _, e := tx.Exec("DROP TRIGGER test_delete_failure"); return e })
	apiCall(t, h, "POST", "/projects/"+p.ID+"/delete", in, 200)
	other := project(t, h, "Не затрагивать")
	apiCall(t, h, "POST", "/projects/"+other.ID+"/delete", in, 409)
	apiCall(t, h, "GET", "/projects/"+other.ID, nil, 200)
	lifecycleIntegrity(t, db)
}
func TestLifecycleOccurrencesCompletionAndSnapshot(t *testing.T) {
	db, s, h := seriesFixture(t)
	for _, mode := range []recurrence.Mode{recurrence.Schedule, recurrence.Completion} {
		v := createSeries(t, h, seriesInput("delete-"+string(mode), "2026-11-18", mode, 14))
		v = getSeriesTest(t, s, v.ID)
		o := v.Occurrences[0]
		taskID := o.Task.ID
		removeTest(t, h, "occurrences", o.ID)
		apiCall(t, h, "GET", "/tasks/"+taskID, nil, 404)
		drainSeries(t, s, MaterializeInput{SeriesID: v.ID})
		fresh := getSeriesTest(t, s, v.ID)
		if mode == recurrence.Completion && (fresh.State != "needs_anchor" || fresh.WaitingOccurrenceID != nil) {
			t.Fatal(fresh)
		}
		snap := filepath.Join(t.TempDir(), "snapshot.sqlite")
		if e := db.Export(ctx, snap); e != nil {
			t.Fatal(e)
		}
		if e := db.Restore(ctx, snap); e != nil {
			t.Fatal(e)
		}
		if count(t, db, "removed_cycles") == 0 {
			t.Fatal("reservation lost")
		}
		drainSeries(t, s, MaterializeInput{SeriesID: v.ID})
		apiCall(t, h, "GET", "/occurrences/"+o.ID, nil, 404)
		removeTest(t, h, "series", v.ID)
		lifecycleIntegrity(t, db)
	}
	if count(t, db, "series") != 0 || count(t, db, "occurrences") != 0 || count(t, db, "removed_cycles") != 0 {
		t.Fatal("series left")
	}
}
func TestLifecycleTemplatesDependenciesAndProjectOccurrences(t *testing.T) {
	db, s, h := seriesFixture(t)
	tpl := createTemplateTest(t, s, cleaningSnapshot())
	arch := value[CommandResult](t, apiCall(t, h, "POST", "/templates/"+tpl.ID+"/archive", TemplateInput{RequestID: "arch", ExpectedRevision: tpl.Revision}, 200))
	back := value[CommandResult](t, apiCall(t, h, "POST", "/templates/"+tpl.ID+"/unarchive", DeletionInput{RequestID: "back", ExpectedRevision: arch.Template.Revision}, 200)).Template
	if back.ID != tpl.ID || back.Archived || back.VersionID != tpl.VersionID {
		t.Fatal(back)
	}
	v := projectSeriesTest(t, s, *back, "2026-11-18", 14)
	v = getSeriesTest(t, s, v.ID)
	o := v.Occurrences[0]
	in := deleteInput(t, h, "templates", tpl.ID)
	apiCall(t, h, "POST", "/templates/"+tpl.ID+"/delete", in, 409)
	in = deleteInput(t, h, "projects", o.Project.ID)
	apiCall(t, h, "POST", "/projects/"+o.Project.ID+"/delete", in, 409)
	removeTest(t, h, "occurrences", o.ID)
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID})
	apiCall(t, h, "GET", "/projects/"+o.Project.ID, nil, 404)
	removeTest(t, h, "series", v.ID)
	removeTest(t, h, "templates", tpl.ID)
	taskTpl := value[CommandResult](t, apiCall(t, h, "POST", "/task-templates", TaskTemplateInput{RequestID: "task-tpl", Snapshot: &TaskBlueprint{Title: "Заготовка", Priority: "normal"}}, 201)).Template
	taskTpl = value[CommandResult](t, apiCall(t, h, "POST", "/task-templates/"+taskTpl.ID+"/archive", TaskTemplateInput{RequestID: "task-arch", ExpectedRevision: taskTpl.Revision}, 200)).Template
	taskTpl = value[CommandResult](t, apiCall(t, h, "POST", "/task-templates/"+taskTpl.ID+"/unarchive", DeletionInput{RequestID: "task-back", ExpectedRevision: taskTpl.Revision}, 200)).Template
	removeTest(t, h, "task-templates", taskTpl.ID)
	lifecycleIntegrity(t, db)
}
func TestLifecycleBlocksIndependentSeries(t *testing.T) {
	db, _, h := seriesFixture(t)
	p := project(t, h, "Дом")
	in := seriesInput("nested-delete", "2026-11-18", recurrence.Schedule, 30)
	in.Task.ProjectID = &p.ID
	v := createSeries(t, h, in)
	del := deleteInput(t, h, "projects", p.ID)
	apiCall(t, h, "POST", "/projects/"+p.ID+"/delete", del, 409)
	removeTest(t, h, "series", v.ID)
	removeTest(t, h, "projects", p.ID)
	lifecycleIntegrity(t, db)
}

func TestLifecycleSourceProjectAndMigrationRestart(t *testing.T) {
	db, s, h := seriesFixture(t)
	p := project(t, h, "Самостоятельный источник")
	task(t, h, map[string]any{"title": "Исходное дерево", "project_id": p.ID, "planned_date": "2026-11-21"})
	rule := seriesInput("unused", "2026-12-01", recurrence.Schedule, 14).Rule
	in := ProjectSourceInput{RequestID: "source-delete-repeat", ExpectedRevision: p.Revision, AnchorDate: "2026-11-21", FirstDate: "2026-12-01", Rule: &rule}
	preview, e := s.ProjectTemplatePreview(ctx, p.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	in.PreviewToken = preview.PreviewToken
	result, e := s.SaveProjectTemplate(ctx, p.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	linked, e := s.PreviewDeletion(ctx, "projects", p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if len(linked.Dependencies) != 1 || linked.Dependencies[0].ID != result.Series.ID || len(linked.Blockers) != 0 {
		t.Fatal("standalone source association missing", linked)
	}
	removeTest(t, h, "series", result.Series.ID)
	fresh := value[ProjectDTO](t, apiCall(t, h, "GET", "/projects/"+p.ID, nil, 200))
	if len(fresh.Tasks) != 1 || fresh.RepeatSeriesID != nil || fresh.SavedTemplateID == nil {
		t.Fatal(fresh)
	}
	blocked := deleteInput(t, h, "templates", result.Template.ID)
	apiCall(t, h, "POST", "/templates/"+result.Template.ID+"/delete", blocked, 409)
	removeTest(t, h, "projects", p.ID)
	removeTest(t, h, "templates", result.Template.ID)
	lifecycleIntegrity(t, db)
	v := createSeries(t, h, seriesInput("restart-deleted", "2026-11-18", recurrence.Schedule, 30))
	o := getSeriesTest(t, s, v.ID).Occurrences[0]
	removeTest(t, h, "occurrences", o.ID)
	dbPath := db.path
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(ctx, dbPath)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	next := NewService(reopened)
	next.now = s.now
	drainSeries(t, next, MaterializeInput{SeriesID: v.ID})
	newh := NewHandler(next)
	apiCall(t, newh, "GET", "/occurrences/"+o.ID, nil, 404)
	lifecycleIntegrity(t, reopened)
}
func TestLifecycleUpgradeFromNineAndReservationEnforcement(t *testing.T) {
	dbpath := filepath.Join(t.TempDir(), "upgrade.db")
	if e := os.WriteFile(dbpath, nil, 0600); e != nil {
		t.Fatal(e)
	}
	db, e := openSQL(dbpath, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = migrate(ctx, db, migrations[:9]); e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	upgraded, e := Open(ctx, dbpath)
	if e != nil {
		t.Fatal(e)
	}
	defer upgraded.Close()
	var version int
	upgraded.db.QueryRow("PRAGMA user_version").Scan(&version)
	if version != len(migrations) {
		t.Fatal(version)
	}
	lifecycleIntegrity(t, upgraded)
	store, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("reservation", "2026-11-18", recurrence.Schedule, 45))
	o := getSeriesTest(t, s, v.ID).Occurrences[1]
	removeTest(t, h, "occurrences", o.ID)
	e = store.Transaction(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT INTO occurrences(id,series_id,rule_version_id,cycle_index,base_date,adjusted_date,task_link) VALUES('replacement',?,?,?,?,?,'replacement')`, v.ID, o.RuleVersionID, o.CycleIndex, o.BaseDate, o.AdjustedDate)
		return e
	})
	if e == nil {
		t.Fatal("deleted cycle inserted")
	}
	// Even a cursor rewind cannot resurrect a removed cycle; the normal cursor never rewinds.
	exec(t, store, "UPDATE series_runtime SET next_cycle=? WHERE series_id=?", o.CycleIndex, v.ID)
	// Beta 2 already owns three real cycles. A deliberately rewound cursor must fail atomically at the existing successor without reviving the removed cycle.
	if e = store.Transaction(ctx, func(tx *sql.Tx) error {
		vv, e := getSeries(tx, v.ID)
		if e != nil {
			return e
		}
		vv.HorizonDate = "2026-11-18"
		_, _, e = s.materializeSeriesTx(ctx, tx, &vv, "reservation-test", stamp(s.now()))
		return e
	}); e == nil {
		t.Fatal("rewound cursor crossed an existing successor")
	}
	if getSeriesTest(t, s, v.ID).NextCycle != o.CycleIndex {
		t.Fatal("failed transaction changed cursor")
	}
	apiCall(t, h, "GET", "/occurrences/"+o.ID, nil, 404)
	lifecycleIntegrity(t, store)
}

func TestLifecycleCompletionContinuationAndSkippedProject(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("deleted-continuation", "2026-11-18", recurrence.Completion, 14))
	first := getSeriesTest(t, s, v.ID).Occurrences[0]
	completeSeriesTask(t, h, first.Task, "2026-11-16")
	v = getSeriesTest(t, s, v.ID)
	waiting := *v.WaitingOccurrenceID
	removeTest(t, h, "occurrences", waiting)
	managedApply(t, s, v.ID, SeriesCommand{Action: "reanchor", AnchorDate: "2026-11-25"})
	v = getSeriesTest(t, s, v.ID)
	if v.WaitingOccurrenceID == nil {
		t.Fatal("continuation missing")
	}
	next := *v.WaitingOccurrenceID
	removeTest(t, h, "occurrences", first.ID)
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID})
	v = getSeriesTest(t, s, v.ID)
	if v.WaitingOccurrenceID == nil || *v.WaitingOccurrenceID != next || len(v.Occurrences) != 3 {
		t.Fatal("second completion branch or lost successor", v)
	}
	tpl := createTemplateTest(t, s, cleaningSnapshot())
	projects := projectSeriesTest(t, s, tpl, "2026-11-18", 14)
	o := getSeriesTest(t, s, projects.ID).Occurrences[0]
	done := taskNamed(t, *o.Project, "Купить средства")
	completeSeriesTask(t, h, done, "2026-11-16")
	in := projectOccurrenceInput(t, s, o.ID)
	in.TasksDecision = "skip_open"
	in.SessionsDecision = "cancel"
	if _, e := s.OccurrenceCommand(ctx, o.ID, "skip", in); e != nil {
		t.Fatal(e)
	}
	if count(t, db, "project_closed_tasks") == 0 || count(t, db, "occurrence_incidents") == 0 {
		t.Fatal("skip not registered")
	}
	removeTest(t, h, "occurrences", o.ID)
	if count(t, db, "project_closed_tasks") != 0 || count(t, db, "occurrence_incidents") != 0 {
		t.Fatal("orphaned skip data")
	}
	drainSeries(t, s, MaterializeInput{SeriesID: projects.ID})
	apiCall(t, h, "GET", "/projects/"+o.Project.ID, nil, 404)
	lifecycleIntegrity(t, db)
}

func TestLifecycleHistoricalSourceDependencyWithoutLiveInstances(t *testing.T) {
	db, s, h := seriesFixture(t)
	p := project(t, h, "Источник повторения")
	in := seriesInput("empty-but-linked", "2026-11-18", recurrence.Schedule, 30)
	in.Task.ProjectID = &p.ID
	v := createSeries(t, h, in)
	for _, o := range getSeriesTest(t, s, v.ID).Occurrences {
		removeTest(t, h, "occurrences", o.ID)
	}
	preview, e := s.PreviewDeletion(ctx, "projects", p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if len(preview.Blockers) == 0 || len(preview.Dependencies) != 1 || preview.Dependencies[0].ID != v.ID {
		t.Fatal("source reference not protected", preview)
	}
	apiCall(t, h, "POST", "/projects/"+p.ID+"/delete", deleteInput(t, h, "projects", p.ID), 409)
	removeTest(t, h, "series", v.ID)
	removeTest(t, h, "projects", p.ID)
	lifecycleIntegrity(t, db)
}

func TestLifecycleCompactProjectDeletionPreservesOtherManualDates(t *testing.T) {
	db, s, h := seriesFixture(t)
	tpl := createTemplateTest(t, s, cleaningSnapshot())
	v := projectSeriesTest(t, s, tpl, "2026-11-18", 14)
	first := getSeriesTest(t, s, v.ID).Occurrences[0]
	move := projectOccurrenceInput(t, s, first.ID)
	move.PlannedDate = ptr("2026-11-19")
	move.TasksDateDecision = "keep"
	if _, e := s.OccurrenceCommand(ctx, first.ID, "reschedule", move); e != nil {
		t.Fatal(e)
	}
	managedApply(t, s, v.ID, SeriesCommand{Action: "pause", StartDate: "2027-01-20", EndDate: "2027-02-28"})
	setProjectLimitTest(t, h, 20)
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID, Through: "2027-02-28"})
	v = getSeriesTest(t, s, v.ID)
	var compact string
	for _, o := range v.Occurrences {
		if o.Compact {
			compact = o.ID
			break
		}
	}
	if compact == "" {
		t.Fatal("compact pause fixture missing")
	}
	removeTest(t, h, "occurrences", compact)
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID, Through: "2027-02-28"})
	fresh, _ := currentOccurrence(t, s, first.ID)
	if fresh.ManualDate == nil || *fresh.ManualDate != "2026-11-19" || !fresh.ManualOverride {
		t.Fatal("manual date changed", fresh)
	}
	apiCall(t, h, "GET", "/occurrences/"+compact, nil, 404)
	removeTest(t, h, "series", v.ID)
	lifecycleIntegrity(t, db)
}

func TestLifecycleDelayedCreationReplayCannotResurrectSeries(t *testing.T) {
	db, s, h := seriesFixture(t)
	in := seriesInput("delayed-creation-replay", "2026-11-18", recurrence.Schedule, 30)
	v := createSeries(t, h, in)
	removeTest(t, h, "series", v.ID)
	apiCall(t, h, "POST", "/series", in, 409)
	in.Task.Title = "Другой объект тем же ключом"
	apiCall(t, h, "POST", "/series", in, 409)
	if count(t, db, "series") != 0 || count(t, db, "tasks") != 0 || count(t, db, "retired_series_requests") != 1 {
		t.Fatal("recreated after deletion")
	}
	snapshot := filepath.Join(t.TempDir(), "retired-requests.sqlite")
	if e := db.Export(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	if e := db.Restore(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	apiCall(t, h, "POST", "/series", in, 409)
	// A genuinely new command is allowed; no task or series payload was kept.
	in.RequestID = "genuinely-new-command"
	created := createSeries(t, h, in)
	if created.ID == v.ID {
		t.Fatal("reused object ID")
	}
	tpl := createTemplateTest(t, s, cleaningSnapshot())
	projectIn := seriesInput("project-replay", "2026-11-18", recurrence.Schedule, 30)
	projectIn.Task = nil
	projectIn.SourceType = "project"
	projectIn.TemplateID = tpl.ID
	projectIn.ExpectedRevision = tpl.Revision
	projectSeries := createSeries(t, h, projectIn)
	removeTest(t, h, "series", projectSeries.ID)
	// Project creation already uses durable command receipts: replay returns the
	// original receipt, never recreates the physical series.
	replay := createSeries(t, h, projectIn)
	if replay.ID != projectSeries.ID || count(t, db, "series") != 1 {
		t.Fatal("project creation resurrected")
	}
	lifecycleIntegrity(t, db)
}

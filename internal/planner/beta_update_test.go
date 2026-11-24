package planner

import (
	"budget/internal/planner/recurrence"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func strBeta(s string) *string { return &s }
func TestBetaSeriesAppearanceTransactionsAndHistory(t *testing.T) {
	db, s, h := seriesFixture(t)
	in := seriesInput("beta-color", "2026-11-16", recurrence.Schedule, 2)
	in.Task.Appearance = Appearance{ColorID: strBeta("teal"), IconID: strBeta("lucide:house")}
	v := createSeries(t, h, in)
	original := v.RuleVersionID
	originalRule := v.Rule
	closed := completeSeriesTask(t, h, v.Occurrences[0].Task, "2026-11-16")
	local := v.Occurrences[1].Task
	apiCall(t, h, "PATCH", "/tasks/"+local.ID, map[string]any{"expected_revision": local.Revision, "color_id": "#123456"}, 200)
	// Returning an individual color to the source value is still an explicit choice.
	sameColor := v.Occurrences[3].Task
	firstEdit := value[TaskDTO](t, apiCall(t, h, "PATCH", "/tasks/"+sameColor.ID, map[string]any{"expected_revision": sameColor.Revision, "color_id": "rose"}, 200))
	apiCall(t, h, "PATCH", "/tasks/"+sameColor.ID, map[string]any{"expected_revision": firstEdit.Revision, "color_id": "teal"}, 200)
	v = getSeriesTest(t, s, v.ID)
	change := SeriesAppearanceInput{Appearance: Appearance{ColorID: strBeta("red"), IconID: strBeta("app:tooth")}, ExpectedRevision: v.Revision}
	preview := value[AppearancePreview](t, apiCall(t, h, "POST", "/series/"+v.ID+"/appearance-preview", change, 200))
	change.PreviewToken = preview.PreviewToken
	change.RequestID = "beta-apply"
	// A concurrent local override invalidates the entire preview transaction.
	target := v.Occurrences[2].Task
	apiCall(t, h, "PATCH", "/tasks/"+target.ID, map[string]any{"expected_revision": target.Revision, "title": "Changed elsewhere"}, 200)
	apiCall(t, h, "POST", "/series/"+v.ID+"/appearance", change, 409)
	preview = value[AppearancePreview](t, apiCall(t, h, "POST", "/series/"+v.ID+"/appearance-preview", change, 200))
	change.PreviewToken = preview.PreviewToken
	// Inject a write error after source creation: source and every task must rollback.
	db.Transaction(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec("CREATE TRIGGER beta_fail BEFORE UPDATE ON tasks BEGIN SELECT RAISE(ABORT,'injected'); END")
		return e
	})
	apiCall(t, h, "POST", "/series/"+v.ID+"/appearance", change, 422)
	db.Transaction(ctx, func(tx *sql.Tx) error {
		var n int
		tx.QueryRow("SELECT count(*) FROM series_appearances").Scan(&n)
		if n != 0 {
			t.Fatal("partial source")
		}
		_, e := tx.Exec("DROP TRIGGER beta_fail")
		return e
	})
	apiCall(t, h, "POST", "/series/"+v.ID+"/appearance", change, 200)
	apiCall(t, h, "POST", "/series/"+v.ID+"/appearance", change, 200)
	current := getSeriesTest(t, s, v.ID)
	if original != current.RuleVersionID || !reflect.DeepEqual(originalRule, current.Rule) {
		t.Fatal("schedule changed")
	}
	read := func(id string) TaskDTO { return value[TaskDTO](t, apiCall(t, h, "GET", "/tasks/"+id, nil, 200)) }
	if !reflect.DeepEqual(read(closed.ID).Appearance, closed.Appearance) {
		t.Fatal("closed history changed")
	}
	if *read(local.ID).ColorID != "#123456" || *read(local.ID).IconID != "app:tooth" {
		t.Fatal("per-field override lost")
	}
	if *read(sameColor.ID).ColorID != "teal" {
		t.Fatal("explicit same-value override lost")
	}
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID, Through: "2027-03-01"})
	after := getSeriesTest(t, s, v.ID)
	last := after.Occurrences[len(after.Occurrences)-1].Task
	if *last.ColorID != "red" || *last.IconID != "app:tooth" {
		t.Fatal(last.Appearance)
	}
	snap := filepath.Join(t.TempDir(), "snapshot.db")
	if e := db.Export(ctx, snap); e != nil {
		t.Fatal(e)
	}
	if e := db.Restore(ctx, snap); e != nil {
		t.Fatal(e)
	}
	lifecycleIntegrity(t, db)
	removeTest(t, h, "series", v.ID)
	lifecycleIntegrity(t, db)
}
func TestBetaProjectSeriesAppearanceInheritance(t *testing.T) {
	db, s, h := seriesFixture(t)
	template := value[CommandResult](t, apiCall(t, h, "POST", "/templates", map[string]any{"request_id": "beta-template", "snapshot": map[string]any{"title": "Уборка", "color_id": "teal", "tasks": []any{map[string]any{"key": "a", "title": "Полки", "priority": "normal", "planned_offset": 0}, map[string]any{"key": "b", "title": "Кухня", "priority": "normal", "planned_offset": 1, "color_id": "rose"}}}}, 201)).Template
	input := seriesInput("beta-project", "2026-11-16", recurrence.Schedule, 14)
	input.Task = nil
	input.SourceType = "project"
	input.TemplateID = template.ID
	input.ExpectedRevision = template.Revision
	v := createSeries(t, h, input)
	change := SeriesAppearanceInput{Appearance: Appearance{ColorID: strBeta("#A1B2C3")}, ExpectedRevision: v.Revision, RequestID: "beta-project-style"}
	p := value[AppearancePreview](t, apiCall(t, h, "POST", "/series/"+v.ID+"/appearance-preview", change, 200))
	change.PreviewToken = p.PreviewToken
	apiCall(t, h, "POST", "/series/"+v.ID+"/appearance", change, 200)
	project := value[ProjectDTO](t, apiCall(t, h, "GET", "/projects/"+v.Occurrences[0].Project.ID, nil, 200))
	if *project.ColorID != "#A1B2C3" {
		t.Fatal(project)
	}
	for _, task := range project.Tasks {
		if task.ColorID == nil && task.EffectiveColorID != "#A1B2C3" {
			t.Fatal(task)
		}
		if task.ColorID != nil && *task.ColorID != "rose" {
			t.Fatal(task)
		}
	}
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID, Through: "2027-02-01"})
	v = getSeriesTest(t, s, v.ID)
	if *v.Occurrences[len(v.Occurrences)-1].Project.ColorID != "#A1B2C3" {
		t.Fatal("future project appearance")
	}
	lifecycleIntegrity(t, db)
}
func TestBetaAtomicRecurringCreationAndUpcoming(t *testing.T) {
	db, s, h := seriesFixture(t)
	in := seriesInput("beta-new", "2026-11-16", recurrence.Schedule, 45)
	in.FirstDueDate = strBeta("2026-11-20")
	in.FirstStatus = "in_progress"
	in.Task.Appearance = Appearance{ColorID: strBeta("#abcdef"), IconID: strBeta("app:toothbrush")}
	v := createSeries(t, h, in)
	same := createSeries(t, h, in)
	if v.ID != same.ID || *v.Occurrences[0].Task.DueDate != "2026-11-20" {
		t.Fatal(v)
	}
	r := value[UpcomingResult](t, apiCall(t, h, "POST", "/series/"+v.ID+"/ensure-upcoming", map[string]any{}, 200))
	if r.Created != 0 {
		t.Fatal(r)
	}
	v = getSeriesTest(t, s, v.ID)
	if len(v.Occurrences) != 3 {
		t.Fatal(len(v.Occurrences))
	}
	if v.Occurrences[1].Task.DueDate != nil {
		t.Fatal("absolute deadline copied")
	}
	apiCall(t, h, "POST", "/series/"+v.ID+"/ensure-upcoming", map[string]any{}, 200)
	if len(getSeriesTest(t, s, v.ID).Occurrences) != 3 {
		t.Fatal("duplicated")
	}
	completion := createSeries(t, h, seriesInput("beta-after", "2026-11-16", recurrence.Completion, 2))
	apiCall(t, h, "POST", "/series/"+completion.ID+"/ensure-upcoming", map[string]any{}, 200)
	if len(getSeriesTest(t, s, completion.ID).Occurrences) != 3 {
		t.Fatal("dynamic chain must contain three tasks")
	}
	rare := seriesInput("beta-rare", "2026-11-16", recurrence.Schedule, 1)
	rare.Rule.Unit = recurrence.Years
	rv := createSeries(t, h, rare)
	rr := value[UpcomingResult](t, apiCall(t, h, "POST", "/series/"+rv.ID+"/ensure-upcoming", map[string]any{}, 200))
	if len(rr.Forecast) != 0 || len(getSeriesTest(t, s, rv.ID).Occurrences) != 3 {
		t.Fatal(rr)
	}
	bad := in
	bad.RequestID = "beta-bad"
	bad.FirstDueDate = strBeta("bad")
	apiCall(t, h, "POST", "/series", bad, 422)
	for _, color := range []string{"#abc", "red;url(x)", "#abcdefx", "#12GG22"} {
		task(t, h, map[string]any{"title": "safe"})
		apiCall(t, h, "POST", "/tasks", map[string]any{"title": "bad", "color_id": color}, 422)
	}
	lifecycleIntegrity(t, db)
}

func TestBetaMigrationTenRetainsOwnedColorsAndRejectsCSS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v10.db")
	if e := os.WriteFile(path, nil, 0600); e != nil {
		t.Fatal(e)
	}
	db, e := openSQL(path, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = migrate(ctx, db, migrations[:10]); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO projects(id,title,color_id) VALUES('p','Home','teal'); INSERT INTO tasks(id,title,project_id,color_id) VALUES('a','Parent','p','rose'); INSERT INTO tasks(id,title,project_id,parent_task_id) VALUES('b','Child','p','a')"); e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	st, e := Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	svc := NewService(st)
	task, e := svc.Task(ctx, "b")
	if e != nil || task.EffectiveColorID != "rose" {
		t.Fatal(task, e)
	}
	if e = st.Transaction(ctx, func(tx *sql.Tx) error { _, e := tx.Exec("UPDATE tasks SET color_id='#AABBCC' WHERE id='a'"); return e }); e != nil {
		t.Fatal(e)
	}
	if e = st.Transaction(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec("UPDATE tasks SET color_id='url(evil)' WHERE id='a'")
		return e
	}); e == nil {
		t.Fatal("unsafe SQL color accepted")
	}
	lifecycleIntegrity(t, st)
}

package planner

import (
	"budget/internal/planner/recurrence"
	"bytes"
	"database/sql"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	img.Set(1, 1, color.NRGBA{R: 255, A: 255})
	var b bytes.Buffer
	if e := png.Encode(&b, img); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestAppearanceInheritanceOverridesAndHistory(t *testing.T) {
	_, _, h := seriesFixture(t)
	p := value[ProjectDTO](t, apiCall(t, h, "POST", "/projects", map[string]any{"title": "Home", "color_id": "teal", "icon_id": "lucide:house"}, 201))
	a := task(t, h, map[string]any{"title": "parent", "project_id": p.ID})
	b := task(t, h, map[string]any{"title": "child", "project_id": p.ID, "parent_task_id": a.ID})
	get := func(id string) TaskDTO { return value[TaskDTO](t, apiCall(t, h, "GET", "/tasks/"+id, nil, 200)) }
	if b.EffectiveColorID != "teal" || b.EffectiveIconID != "lucide:house" || b.ColorID != nil {
		t.Fatal(b)
	}
	a = value[TaskDTO](t, apiCall(t, h, "PATCH", "/tasks/"+a.ID, map[string]any{"expected_revision": a.Revision, "color_id": "rose", "icon_id": "lucide:coffee"}, 200))
	if a.EffectiveColorID != "rose" || get(b.ID).EffectiveIconID != "lucide:coffee" {
		t.Fatal(a, get(b.ID))
	}
	apiCall(t, h, "PATCH", "/projects/"+p.ID, map[string]any{"expected_revision": 1, "color_id": "blue"}, 200)
	if get(b.ID).EffectiveColorID != "rose" {
		t.Fatal("override lost")
	}
	a = value[TaskDTO](t, apiCall(t, h, "PATCH", "/tasks/"+a.ID, map[string]any{"expected_revision": a.Revision, "color_id": nil, "icon_id": nil}, 200))
	if get(b.ID).EffectiveColorID != "blue" {
		t.Fatal("inheritance did not update")
	}
	found := false
	for _, e := range history(t, h, "task", a.ID) {
		if strings.Contains(string(e.Changes), `"color_id":{"after":null,"before":"rose"}`) {
			found = true
		}
	}
	if !found {
		t.Fatal("clear missing from history")
	}
	q := value[ProjectDTO](t, apiCall(t, h, "POST", "/projects", map[string]any{"title": "Q", "color_id": "orange"}, 201))
	apiCall(t, h, "PATCH", "/tasks/"+a.ID, map[string]any{"expected_revision": a.Revision, "project_id": q.ID, "related_revisions": map[string]int64{b.ID: b.Revision}}, 200)
	if get(b.ID).EffectiveColorID != "orange" {
		t.Fatal("move inheritance")
	}
	apiCall(t, h, "PATCH", "/tasks/"+a.ID, map[string]any{"expected_revision": 1, "color_id": "green"}, 409)
	free := task(t, h, map[string]any{"title": "plain"})
	if free.EffectiveColorID != "slate" || free.EffectiveIconID != "lucide:circle-check" {
		t.Fatal(free)
	}
	for _, bad := range []map[string]any{{"title": "bad", "color_id": "red; background:url(x)"}, {"title": "bad", "icon_id": "<svg>"}, {"title": "bad", "effective_color_id": "blue"}} {
		status := 422
		if bad["effective_color_id"] != nil {
			status = 400
		}
		apiCall(t, h, "POST", "/tasks", bad, status)
	}
}
func TestCustomIconsValidationRetirementAndBackup(t *testing.T) {
	db, _, h := seriesFixture(t)
	raw := testPNG(t)
	in := map[string]any{"name": "<img onerror=bad>", "data": append(append([]byte{}, raw...), []byte("<script>bad</script>")...)}
	icon := value[IconDTO](t, apiCall(t, h, "POST", "/icons", in, 201))
	same := value[IconDTO](t, apiCall(t, h, "POST", "/icons", in, 200))
	if same.ID != icon.ID {
		t.Fatal("upload duplicated")
	}
	imagePath := "/icons/" + icon.ID + "/image"
	w := apiCall(t, h, "GET", imagePath, nil, 200)
	if !bytes.Equal(w.Body.Bytes(), raw) {
		t.Fatal("not canonical PNG")
	}
	if w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(w.Header())
	}
	task(t, h, map[string]any{"title": "custom", "icon_id": icon.ID})
	apiCall(t, h, "PATCH", "/icons/"+icon.ID, map[string]any{"name": "renamed", "expected_revision": 1}, 200)
	apiCall(t, h, "PATCH", "/icons/"+icon.ID, map[string]any{"name": "stale", "expected_revision": 1}, 409)
	apiCall(t, h, "DELETE", "/icons/"+icon.ID, map[string]any{"expected_revision": 2}, 200)
	for _, v := range value[[]IconDTO](t, apiCall(t, h, "GET", "/icons", nil, 200)) {
		if v.ID == icon.ID {
			t.Fatal("retired still selectable")
		}
	}
	apiCall(t, h, "GET", imagePath, nil, 200)
	snapshot := filepath.Join(t.TempDir(), "icons.sqlite")
	if e := db.Export(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	other := testStore(t)
	if e := other.Restore(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	restored := apiCall(t, NewHandler(NewService(other)), "GET", imagePath, nil, 200)
	if !bytes.Equal(restored.Body.Bytes(), raw) {
		t.Fatal("backup image changed")
	}
	if e := other.Transaction(ctx, func(tx *sql.Tx) error {
		var n int
		if e := tx.QueryRow("SELECT count(*) FROM icon_events WHERE icon_id=?", icon.ID).Scan(&n); e != nil {
			return e
		}
		if n != 3 {
			t.Fatalf("history %d", n)
		}
		return checkIntegrity(ctx, tx)
	}); e != nil {
		t.Fatal(e)
	}
	for _, bad := range [][]byte{[]byte("<svg onload='bad'/>"), []byte("RIFFbrokenWEBP"), raw[:20], make([]byte, 1048577)} {
		apiCall(t, h, "POST", "/icons", map[string]any{"name": "bad.png", "data": bad}, 422)
	}
	var large bytes.Buffer
	png.Encode(&large, image.NewGray(image.Rect(0, 0, 1025, 1)))
	apiCall(t, h, "POST", "/icons", map[string]any{"name": "large", "data": large.Bytes()}, 422)
	apiCall(t, h, "PATCH", "/icons/lucide:house", map[string]any{"name": "bad", "expected_revision": 1}, 422)
	apiCall(t, h, "GET", "/icons/lucide:house/image", nil, 200)
	apiCall(t, h, "POST", "/icons", map[string]any{"name": "bad", "data": raw, "image": "untrusted"}, 400)
}
func TestAppearanceCalendarRecurrenceAndTemplates(t *testing.T) {
	db, s, h := seriesFixture(t)
	in := seriesInput("appearance", "2026-11-16", recurrence.Schedule, 7)
	in.Task.Appearance = Appearance{ColorID: ptr("teal"), IconID: ptr("lucide:coffee")}
	series := createSeries(t, h, in)
	first := series.Occurrences[0].Task
	apiCall(t, h, "PATCH", "/tasks/"+first.ID, map[string]any{"expected_revision": first.Revision, "color_id": "rose"}, 200)
	drainSeries(t, s, MaterializeInput{Through: "2027-02-01"})
	current := getSeriesTest(t, s, series.ID)
	for i, o := range current.Occurrences {
		want := "teal"
		if i == 0 {
			want = "rose"
		}
		if o.Task.ColorID == nil || *o.Task.ColorID != want || *o.Task.IconID != "lucide:coffee" {
			t.Fatal(o)
		}
	}
	apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": first.ID, "date": "2026-11-17"}, 201)
	cal, e := s.CalendarProjection(ctx, "2026-11-16", "2026-11-18")
	if e != nil {
		t.Fatal(e)
	}
	for _, item := range cal.Items {
		if item.TaskID == first.ID && item.EffectiveColorID != "rose" {
			t.Fatal(item)
		}
	}
	snapshot := cleaningSnapshot()
	snapshot.Appearance = Appearance{ColorID: ptr("green"), IconID: ptr("lucide:house")}
	snapshot.Tasks[0].Appearance = Appearance{ColorID: ptr("purple")}
	template := createTemplateTest(t, s, snapshot)
	projects := projectSeriesTest(t, s, template, "2026-11-16", 14)
	for _, o := range projects.Occurrences {
		p := o.Project
		if p == nil || p.EffectiveColorID != "green" {
			t.Fatal(o)
		}
		if taskNamed(t, *p, "Кухня").EffectiveColorID != "purple" {
			t.Fatal(p)
		}
	}
	// Legacy baselines without computed appearance still recognize untouched projects.
	p := projects.Occurrences[0].Project
	if e := db.Transaction(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec("UPDATE project_instances SET baseline_json=json_remove(baseline_json,'$.effective_color_id','$.effective_icon_id') WHERE project_id=?", p.ID)
		if e != nil {
			return e
		}
		ok, e := pristineProject(tx, *p)
		if !ok {
			t.Fatal("derived fields protected pristine project")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}
func TestAppearanceHistoryFailureRollsBack(t *testing.T) {
	db, _, h := seriesFixture(t)
	a := task(t, h, map[string]any{"title": "rollback"})
	exec(t, db, `CREATE TRIGGER fail_appearance_history BEFORE INSERT ON history_events BEGIN SELECT RAISE(ABORT,'test history failure'); END`)
	apiCall(t, h, "PATCH", "/tasks/"+a.ID, map[string]any{"expected_revision": 1, "color_id": "blue"}, 422)
	got := value[TaskDTO](t, apiCall(t, h, "GET", "/tasks/"+a.ID, nil, 200))
	if got.ColorID != nil || got.Revision != 1 {
		t.Fatal(got)
	}
	exec(t, db, `CREATE TRIGGER fail_icon_history BEFORE INSERT ON icon_events BEGIN SELECT RAISE(ABORT,'test icon history failure'); END`)
	apiCall(t, h, "POST", "/icons", map[string]any{"name": "rollback", "data": testPNG(t)}, 422)
	if len(value[[]IconDTO](t, apiCall(t, h, "GET", "/icons", nil, 200))) != len(iconCatalog) {
		t.Fatal("partial upload")
	}
}
func TestDeadlineMoscowEndOfDayAndIndependentPlanning(t *testing.T) {
	_, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 16, 20, 59, 0, 0, time.UTC) }
	a := task(t, h, map[string]any{"title": "plan", "planned_date": "2026-11-15", "due_date": "2026-11-16"})
	read := func() string { return apiCall(t, h, "GET", "/attention?tab=overdue", nil, 200).Body.String() }
	if !strings.Contains(read(), "Не выполнено по плану") || strings.Contains(read(), "Просрочен крайний срок") {
		t.Fatal(read())
	}
	s.now = func() time.Time { return time.Date(2026, 11, 16, 21, 0, 0, 0, time.UTC) }
	if !strings.Contains(read(), "Просрочен крайний срок") {
		t.Fatal(read())
	}
	moved := value[TaskDTO](t, apiCall(t, h, "POST", "/tasks/"+a.ID+"/reschedule", map[string]any{"expected_revision": 1, "planned_date": "2026-12-01"}, 200))
	if *moved.DueDate != "2026-11-16" {
		t.Fatal(moved)
	}
	done := value[TaskDTO](t, apiCall(t, h, "POST", "/tasks/"+a.ID+"/complete", map[string]any{"expected_revision": moved.Revision, "completed_date": "2026-11-17"}, 200))
	if *done.CompletedDate != "2026-11-17" {
		t.Fatal(done)
	}
}
func TestWebPIcon(t *testing.T) {
	raw, e := os.ReadFile("testdata/icon.webp")
	if e != nil {
		t.Fatal(e)
	}
	data, w, h, e := normalizeIcon(raw)
	if e != nil || w < 1 || h < 1 || !bytes.HasPrefix(data, []byte("\x89PNG")) {
		t.Fatal(w, h, e)
	}
}

func TestTemplateAppearanceVersionProtectsManualAndStartedInstances(t *testing.T) {
	_, s, h := seriesFixture(t)
	snapshot := cleaningSnapshot()
	snapshot.ColorID = ptr("green")
	snapshot.IconID = ptr("lucide:house")
	template := createTemplateTest(t, s, snapshot)
	series := projectSeriesTest(t, s, template, "2026-11-16", 14)
	first, second := series.Occurrences[0].Project, series.Occurrences[1].Project
	done := taskNamed(t, *first, "Купить средства")
	completeSeriesTask(t, h, done, "2026-11-15")
	apiCall(t, h, "PATCH", "/projects/"+second.ID, map[string]any{"expected_revision": second.Revision, "color_id": "rose"}, 200)
	snapshot.ColorID = ptr("blue")
	snapshot.Tasks[0].IconID = ptr("lucide:sofa")
	version, e := s.TemplateCommand(ctx, template.ID, "versions", TemplateInput{RequestID: "color-version", ExpectedRevision: template.Revision, Snapshot: &snapshot})
	if e != nil {
		t.Fatal(e)
	}
	command, preview := managedPreview(t, s, series.ID, SeriesCommand{Action: "change_template", FromCycle: ptr(int64(0)), TemplateVersionID: version.Template.VersionID})
	if preview.Rows[0].Disposition != "protected_started" || preview.Rows[1].Disposition != "protected_user_changes" {
		t.Fatal(preview.Rows)
	}
	if _, e = s.ApplySeriesCommand(ctx, series.ID, command); e != nil {
		t.Fatal(e)
	}
	current := getSeriesTest(t, s, series.ID)
	for i, o := range current.Occurrences {
		want := "blue"
		if i == 0 {
			want = "green"
		}
		if i == 1 {
			want = "rose"
		}
		if *o.Project.ColorID != want {
			t.Fatal(i, o.Project)
		}
	}
	if taskNamed(t, *current.Occurrences[0].Project, "Купить средства").Status != "completed" {
		t.Fatal("history changed")
	}
}
func TestIconCorruptionRefusesRestoreWithoutChangingLiveDatabase(t *testing.T) {
	db, _, h := seriesFixture(t)
	icon := value[IconDTO](t, apiCall(t, h, "POST", "/icons", map[string]any{"name": "backup", "data": testPNG(t)}, 201))
	path := filepath.Join(t.TempDir(), "snapshot.sqlite")
	if e := db.Export(ctx, path); e != nil {
		t.Fatal(e)
	}
	backup, e := openSQL(path, false)
	if e != nil {
		t.Fatal(e)
	}
	_, e = backup.Exec("UPDATE planner_icons SET image=? WHERE id=?", []byte("not an image"), icon.ID)
	backup.Close()
	if e != nil {
		t.Fatal(e)
	}
	before := recurrenceDBSnapshot(t, db)
	if e = db.Restore(ctx, path); e == nil {
		t.Fatal("corrupt image accepted")
	}
	if recurrenceDBSnapshot(t, db) != before {
		t.Fatal("failed restore changed database")
	}
}

func TestAppearanceHistoryDoesNotInventRemovalInPartialEvents(t *testing.T) {
	db, _, h := seriesFixture(t)
	a := task(t, h, map[string]any{"title": "history", "color_id": "blue"})
	if e := db.Transaction(ctx, func(tx *sql.Tx) error {
		return event(tx, "task", a.ID, "edited", "partial", stamp(time.Now()), a, map[string]any{"status": "in_progress"})
	}); e != nil {
		t.Fatal(e)
	}
	for _, event := range history(t, h, "task", a.ID) {
		if event.OperationID == "partial" {
			if strings.Contains(string(event.Changes), "color_id") || strings.Contains(string(event.Changes), "title") {
				t.Fatal(event)
			}
		}
	}
}

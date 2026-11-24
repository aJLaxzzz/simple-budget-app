package planner

import (
	"budget/internal/planner/recurrence"
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func cleaningSnapshot() ProjectTemplateSnapshot {
	return ProjectTemplateSnapshot{Title: "Уборка", Description: "Исходный шаблон", DueOffset: ptr(3), Tasks: []ProjectTemplateTask{{Key: "root", Title: "Комнаты", PlannedOffset: ptr(0), DueOffset: ptr(2)}, {Key: "child", ParentKey: ptr("root"), Title: "Кухня", PlannedOffset: ptr(1), DueOffset: ptr(3)}, {Key: "before", Title: "Купить средства", PlannedOffset: ptr(-1), DueOffset: ptr(0)}, {Key: "undated", Title: "Без даты"}}}
}
func createTemplateTest(t *testing.T, s *Service, snapshot ProjectTemplateSnapshot) ProjectTemplateDTO {
	t.Helper()
	key, _ := operation()
	result, e := s.TemplateCommand(ctx, "", "create", TemplateInput{RequestID: key, Snapshot: &snapshot})
	if e != nil {
		t.Fatal(e)
	}
	return *result.Template
}
func projectSeriesTest(t *testing.T, s *Service, template ProjectTemplateDTO, first string, interval int64) SeriesDTO {
	t.Helper()
	in := seriesInput("project-"+template.ID, first, recurrence.Schedule, interval)
	in.Task = nil
	in.SourceType = "project"
	in.TemplateID = template.ID
	in.ExpectedRevision = template.Revision
	v, e := s.CreateSeries(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func projectOccurrenceInput(t *testing.T, s *Service, id string) OccurrenceCommand {
	t.Helper()
	o, v := currentOccurrence(t, s, id)
	key, _ := operation()
	in := OccurrenceCommand{RequestID: key, ExpectedRevision: o.Revision, ExpectedSeriesRevision: v.Revision, RelatedRevisions: map[string]int64{}, SessionRevisions: map[string]int64{}}
	if o.Project != nil {
		in.ExpectedProjectRevision = o.Project.Revision
		for _, task := range o.Project.Tasks {
			in.RelatedRevisions[task.ID] = task.Revision
		}
	}
	return in
}
func taskNamed(t *testing.T, p ProjectDTO, title string) TaskDTO {
	t.Helper()
	for _, task := range p.Tasks {
		if task.Title == title {
			return task
		}
	}
	t.Fatal("missing task", title)
	return TaskDTO{}
}
func TestTemplateValidationIndependentTreesOffsetsAndHistory(t *testing.T) {
	db, s, _ := seriesFixture(t)
	snapshot := cleaningSnapshot()
	v := createTemplateTest(t, s, snapshot)
	if v.Version != 1 || len(v.Snapshot.Tasks) != 4 || v.Snapshot.Tasks[0].Priority != "normal" {
		t.Fatal(v)
	}
	before := recurrenceDBSnapshot(t, db)
	preview, e := s.TemplatePreview(ctx, v.ID, TemplateInput{StartDate: "2026-12-20"})
	if e != nil || *preview[0].Project.Tasks[2].PlannedDate != "2026-12-19" {
		t.Fatal(preview, e)
	}
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("preview wrote")
	}
	instantiate := func(key string) ProjectDTO {
		r, e := s.TemplateCommand(ctx, v.ID, "instantiate", TemplateInput{RequestID: key, ExpectedRevision: v.Revision, StartDate: "2026-12-20"})
		if e != nil {
			t.Fatal(e)
		}
		return *r.Project
	}
	a, b := instantiate("manual-one"), instantiate("manual-two")
	if a.ID == b.ID || a.TemplateVersionID == nil || len(a.Tasks) != 4 {
		t.Fatal(a, b)
	}
	root := taskNamed(t, a, "Комнаты")
	child := taskNamed(t, a, "Кухня")
	if child.ParentTaskID == nil || *child.ParentTaskID != root.ID || *child.PlannedDate != "2026-12-21" || *root.PlannedDate != "2026-12-20" || *root.DueDate != "2026-12-22" || taskNamed(t, a, "Без даты").PlannedDate != nil {
		t.Fatal(a)
	}
	if taskNamed(t, b, "Комнаты").ID == root.ID {
		t.Fatal("shared tree identity")
	}
	c := instantiate("manual-one")
	if c.ID != a.ID {
		t.Fatal("manual creation duplicated")
	}
	hist, e := s.History(ctx, "template", v.ID, 100, 0)
	if e != nil || len(hist) != 3 {
		t.Fatal(hist, e)
	}
	for _, bad := range []ProjectTemplateSnapshot{
		{Title: "bad", Tasks: []ProjectTemplateTask{{Key: "a", Title: "a", ParentKey: ptr("a")}}},
		{Title: "bad", Tasks: []ProjectTemplateTask{{Key: "a", Title: "a"}, {Key: "b", Title: "b", ParentKey: ptr("a")}, {Key: "c", Title: "c", ParentKey: ptr("b")}}},
		{Title: "bad", Tasks: []ProjectTemplateTask{{Key: "a", Title: "a", ParentKey: ptr("b")}, {Key: "b", Title: "b", ParentKey: ptr("a")}}},
		{Title: "bad", Tasks: []ProjectTemplateTask{{Key: "a", Title: "a", PlannedOffset: ptr(3651)}}},
	} {
		if _, e := normalizeTemplate(bad); e == nil {
			t.Fatal("invalid template accepted", bad)
		}
	}
	if e := db.Transaction(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec("UPDATE template_versions SET snapshot_json='{}' WHERE id=?", v.VersionID)
		return e
	}); e == nil {
		t.Fatal("mutable snapshot")
	}
}
func TestProjectSeriesAdjustedAnchorIdempotenceConcurrencyAndCalendar(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := createTemplateTest(t, s, cleaningSnapshot())
	in := seriesInput("adjusted-project", "2026-11-21", recurrence.Schedule, 14)
	in.Task = nil
	in.SourceType = "project"
	in.TemplateID = v.ID
	in.ExpectedRevision = v.Revision
	in.Rule.AllowedDays.Mode = recurrence.Weekdays
	in.Rule.Adjustment = recurrence.Next
	series, e := s.CreateSeries(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	if len(series.Occurrences) != 4 {
		t.Fatal(len(series.Occurrences))
	}
	first := series.Occurrences[0]
	if first.BaseDate != "2026-11-21" || first.AdjustedDate != "2026-11-23" || *taskNamed(t, *first.Project, "Кухня").PlannedDate != "2026-11-24" {
		t.Fatal(first)
	}
	if count(t, db, "projects") != 4 || count(t, db, "tasks") != 16 {
		t.Fatal("wrong tree count")
	}
	ids := map[string]bool{}
	for _, o := range series.Occurrences {
		if o.Project.Status != "planned" {
			t.Fatal(o)
		}
		for _, task := range o.Project.Tasks {
			if ids[task.ID] {
				t.Fatal("shared task")
			}
			ids[task.ID] = true
		}
	}
	before := recurrenceDBSnapshot(t, db)
	again, e := s.CreateSeries(ctx, in)
	if e != nil || again.ID != series.ID || before != recurrenceDBSnapshot(t, db) {
		t.Fatal("retry wrote", e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.Materialize(ctx, MaterializeInput{SeriesID: series.ID, Through: "2027-03-31"})
			if e == nil && r.Results[0].Error != nil {
				e = r.Results[0].Error
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	current := getSeriesTest(t, s, series.ID)
	if len(current.Occurrences) < 8 {
		t.Fatal(current)
	}
	if count(t, db, "projects") != len(current.Occurrences) {
		t.Fatal("duplicate projects")
	}
	cal := value[CalendarProjection](t, apiCall(t, h, "GET", "/calendar?from=2026-11-22&to=2026-11-27", nil, 200))
	found := false
	for _, x := range cal.Items {
		if x.TaskID == taskNamed(t, *first.Project, "Кухня").ID {
			found = true
			if x.ProjectID == nil || *x.ProjectID != first.Project.ID {
				t.Fatal(x)
			}
		}
	}
	if !found {
		t.Fatal("project tasks absent from calendar")
	}
	in.RequestID = "completion-project"
	in.Rule.Mode = recurrence.Completion
	if _, e = s.CreateSeries(ctx, in); e == nil {
		t.Fatal("completion project accepted")
	}
	if e = db.Transaction(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT INTO occurrences(id,series_id,rule_version_id,cycle_index,base_date,adjusted_date,project_state,outcome) VALUES('duplicate-project',?,?,0,'2026-11-21','2026-11-23','compact','skipped')`, series.ID, series.RuleVersionID)
		return e
	}); e == nil {
		t.Fatal("duplicate cycle allowed")
	}
}
func TestProjectPauseCompactRestoreAndPartialWork(t *testing.T) {
	db, s, h := seriesFixture(t)
	template := createTemplateTest(t, s, cleaningSnapshot())
	v := projectSeriesTest(t, s, template, "2026-11-16", 7)
	first := v.Occurrences[0]
	done := taskNamed(t, *first.Project, "Купить средства")
	completeSeriesTask(t, h, done, "2026-11-15")
	before := recurrenceDBSnapshot(t, db)
	in := projectOccurrenceInput(t, s, first.ID)
	if _, e := s.OccurrenceCommand(ctx, first.ID, "skip", in); e == nil {
		t.Fatal("implicit child skip")
	}
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("partial skip")
	}
	open := taskNamed(t, *first.Project, "Кухня")
	session := value[SessionDTO](t, apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": open.ID, "date": "2026-11-18"}, 201))
	in = projectOccurrenceInput(t, s, first.ID)
	in.TasksDecision = "skip_open"
	if _, e := s.OccurrenceCommand(ctx, first.ID, "skip", in); e == nil {
		t.Fatal("active session silently closed")
	}
	in.SessionsDecision = "cancel"
	in.SessionRevisions[session.ID] = session.Revision
	skipped := occurrenceApply(t, s, first.ID, "skip", in)
	if skipped.Project.Status != "skipped" || taskNamed(t, *skipped.Project, done.Title).Status != "completed" || taskNamed(t, *skipped.Project, open.Title).Status != "skipped" {
		t.Fatal(skipped)
	}
	in = projectOccurrenceInput(t, s, first.ID)
	in.TasksDecision = "restore_closed"
	restored := occurrenceApply(t, s, first.ID, "restore", in)
	if restored.Project.ID != first.Project.ID || taskNamed(t, *restored.Project, done.Title).Status != "completed" || taskNamed(t, *restored.Project, open.Title).ID != open.ID {
		t.Fatal(restored)
	}
	managedApply(t, s, v.ID, SeriesCommand{Action: "pause", StartDate: "2027-02-01", EndDate: "2027-03-01"})
	priorProjects := count(t, db, "projects")
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID, Through: "2027-03-10"})
	current := getSeriesTest(t, s, v.ID)
	compact := []TaskOccurrenceDTO{}
	for _, o := range current.Occurrences {
		if o.Compact {
			compact = append(compact, o)
		}
	}
	if len(compact) != 5 || count(t, db, "projects") != len(current.Occurrences)-5 || priorProjects >= count(t, db, "projects") {
		t.Fatal("compact pause did not avoid trees", len(compact))
	}
	facts := allIncidents(t, s, v.ID, "")
	if len(facts) != 6 {
		t.Fatal(facts)
	}
	id := compact[0].ID
	in = projectOccurrenceInput(t, s, id)
	r := occurrenceApply(t, s, id, "restore", in)
	if r.Compact || r.Project == nil || len(r.Project.Tasks) != 4 || r.ID != id {
		t.Fatal(r)
	}
	n := count(t, db, "projects")
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID, Through: "2027-03-10"})
	if count(t, db, "projects") != n {
		t.Fatal("restoration duplicated")
	}
}
func TestProjectTemplateVersionsPreviewPreserveManualAndRemoval(t *testing.T) {
	db, s, h := seriesFixture(t)
	template := createTemplateTest(t, s, cleaningSnapshot())
	v := projectSeriesTest(t, s, template, "2026-11-16", 14)
	first := v.Occurrences[0]
	task := taskNamed(t, *first.Project, "Купить средства")
	completeSeriesTask(t, h, task, "2026-11-15")
	second := v.Occurrences[1]
	moved := taskNamed(t, *second.Project, "Кухня")
	apiCall(t, h, "POST", "/tasks/"+moved.ID+"/reschedule", map[string]any{"expected_revision": moved.Revision, "planned_date": "2026-12-25"}, 200)
	next := cleaningSnapshot()
	next.Title = "Новая уборка"
	next.Tasks[0].Title = "Новые комнаты"
	next.Tasks = append(next.Tasks, ProjectTemplateTask{Key: "extra", Title: "Окна", PlannedOffset: ptr(2)})
	version, e := s.TemplateCommand(ctx, template.ID, "versions", TemplateInput{RequestID: "template-version-2", ExpectedRevision: template.Revision, Snapshot: &next})
	if e != nil {
		t.Fatal(e)
	}
	if version.Template.Version != 2 || count(t, db, "projects") != len(v.Occurrences) {
		t.Fatal("template edit mutated instances")
	}
	command, preview := managedPreview(t, s, v.ID, SeriesCommand{Action: "change_template", FromCycle: ptr(int64(0)), TemplateVersionID: version.Template.VersionID})
	if preview.Rows[0].Disposition != "protected_started" || preview.Rows[1].Disposition != "protected_user_changes" {
		t.Fatal(preview)
	}
	before := recurrenceDBSnapshot(t, db)
	if _, e = s.PreviewSeriesChange(ctx, v.ID, command); e != nil || before != recurrenceDBSnapshot(t, db) {
		t.Fatal("preview wrote", e)
	}
	if _, e = s.ApplySeriesCommand(ctx, v.ID, command); e != nil {
		t.Fatal(e)
	}
	current := getSeriesTest(t, s, v.ID)
	if current.Occurrences[0].Project.Title != "Уборка" || current.Occurrences[1].Project.Title != "Уборка" || current.Occurrences[2].Project.Title != "Новая уборка" {
		t.Fatal(current)
	}
	if taskNamed(t, *current.Occurrences[2].Project, "Новые комнаты").ID != taskNamed(t, *v.Occurrences[2].Project, "Комнаты").ID {
		t.Fatal("existing identity changed")
	}
	removed := next
	removed.Tasks = append([]ProjectTemplateTask(nil), next.Tasks[:4]...)
	v3, e := s.TemplateCommand(ctx, template.ID, "versions", TemplateInput{RequestID: "version3", ExpectedRevision: version.Template.Revision, Snapshot: &removed})
	if e != nil {
		t.Fatal(e)
	}
	command, preview = managedPreview(t, s, v.ID, SeriesCommand{Action: "change_template", FromCycle: ptr(int64(2)), TemplateVersionID: v3.Template.VersionID})
	if len(preview.Conflicts) == 0 {
		t.Fatal("removal not previewed")
	}
	before = recurrenceDBSnapshot(t, db)
	if _, e = s.ApplySeriesCommand(ctx, v.ID, command); e == nil || before != recurrenceDBSnapshot(t, db) {
		t.Fatal("unconfirmed removal applied", e)
	}
	managedApply(t, s, v.ID, SeriesCommand{Action: "change_template", FromCycle: ptr(int64(2)), TemplateVersionID: v3.Template.VersionID, RemovedTasksDecision: "cancel"})
	current = getSeriesTest(t, s, v.ID)
	if taskNamed(t, *current.Occurrences[2].Project, "Окна").Status != "cancelled" {
		t.Fatal("removed task disappeared")
	}
}
func TestProjectSeriesRuleMovePauseRestartBackupAndRollback(t *testing.T) {
	db, s, h := seriesFixture(t)
	template := createTemplateTest(t, s, cleaningSnapshot())
	v := projectSeriesTest(t, s, template, "2026-11-16", 14)
	first := v.Occurrences[1]
	in := projectOccurrenceInput(t, s, first.ID)
	in.PlannedDate = ptr("2026-12-25")
	in.TasksDateDecision = "keep"
	moved := occurrenceApply(t, s, first.ID, "reschedule", in)
	if *taskNamed(t, *moved.Project, "Кухня").PlannedDate != *taskNamed(t, *first.Project, "Кухня").PlannedDate {
		t.Fatal("implicit child shift")
	}
	rule := v.Rule.Rule
	rule.Interval = 7
	command, preview := managedPreview(t, s, v.ID, SeriesCommand{Action: "change_rule", Rule: &rule, FromCycle: ptr(int64(1)), FirstDate: "2026-11-25", TasksDateDecision: "shift"})
	if preview.Rows[0].Disposition != "manual_exception" {
		t.Fatal(preview)
	}
	apiCall(t, h, "PATCH", "/tasks/"+v.Occurrences[2].Project.Tasks[0].ID, map[string]any{"expected_revision": 1, "description": "changed after preview"}, 200)
	if _, e := s.ApplySeriesCommand(ctx, v.ID, command); e == nil {
		t.Fatal("stale preview accepted")
	}
	managedApply(t, s, v.ID, SeriesCommand{Action: "change_rule", Rule: &rule, FromCycle: ptr(int64(1)), FirstDate: "2026-11-25", TasksDateDecision: "shift"})
	current := getSeriesTest(t, s, v.ID)
	if *current.Occurrences[1].Project.StartDate != "2026-12-25" {
		t.Fatal("manual lost")
	}
	before := recurrenceDBSnapshot(t, db)
	savedSeries := getSeriesTest(t, s, v.ID)
	snapshot := filepath.Join(t.TempDir(), "projects.sqlite")
	if e := db.Export(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	managedApply(t, s, v.ID, SeriesCommand{Action: "pause", StartDate: "2026-12-01", EndDate: "2026-12-31"})
	if e := db.Restore(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(savedSeries, getSeriesTest(t, s, v.ID)) {
		t.Fatal("backup lost project/task identities or version linkage")
	}
	if e := checkIntegrity(ctx, db.db); e != nil {
		t.Fatal(e)
	}
	exec(t, db, `CREATE TRIGGER fail_project_tree BEFORE INSERT ON tasks WHEN NEW.title='Кухня' BEGIN SELECT RAISE(ABORT,'injected tree failure'); END`)
	before = recurrenceDBSnapshot(t, db)
	_, e := s.TemplateCommand(ctx, template.ID, "instantiate", TemplateInput{RequestID: "fail-tree", ExpectedRevision: template.Revision, StartDate: "2026-12-20"})
	if e == nil || before != recurrenceDBSnapshot(t, db) {
		t.Fatal("partial tree committed", e)
	}
	exec(t, db, "DROP TRIGGER fail_project_tree")
	exec(t, db, `CREATE TRIGGER fail_project_history BEFORE INSERT ON history_events WHEN NEW.project_id IS NOT NULL BEGIN SELECT RAISE(ABORT,'history failure'); END`)
	before = recurrenceDBSnapshot(t, db)
	_, e = s.TemplateCommand(ctx, template.ID, "instantiate", TemplateInput{RequestID: "fail-history", ExpectedRevision: template.Revision, StartDate: "2026-12-20"})
	if e == nil || before != recurrenceDBSnapshot(t, db) {
		t.Fatal("partial history", e)
	}
	exec(t, db, "DROP TRIGGER fail_project_history")
	missing := seriesInput("project-rf", "2026-11-16", recurrence.Schedule, 7)
	missing.Task = nil
	missing.SourceType = "project"
	missing.TemplateID = template.ID
	missing.ExpectedRevision = template.Revision
	missing.Rule.AllowedDays.Mode = recurrence.RussianWorkdays
	missing.Rule.Adjustment = recurrence.Next
	before = recurrenceDBSnapshot(t, db)
	if _, e = s.CreateSeries(ctx, missing); e == nil || before != recurrenceDBSnapshot(t, db) {
		t.Fatal("invented RF calendar", e)
	}
	// Existing task-series paths remain covered by all stage 9–11 tests.
}
func TestProjectSeriesRestartAndAPI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "planner.sqlite")
	db, e := Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	s := NewService(db)
	s.now = func() time.Time { return time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC) }
	template := createTemplateTest(t, s, cleaningSnapshot())
	v := projectSeriesTest(t, s, template, "2026-11-16", 7)
	managedApply(t, s, v.ID, SeriesCommand{Action: "pause", StartDate: "2026-12-20", EndDate: "2027-02-10"})
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	db, e = Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s = NewService(db)
	s.now = func() time.Time { return time.Date(2027, 2, 11, 12, 0, 0, 0, time.UTC) }
	drainSeries(t, s, MaterializeInput{})
	before := recurrenceDBSnapshot(t, db)
	drainSeries(t, s, MaterializeInput{})
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("restart duplicated")
	}
	h := NewHandler(s)
	apiCall(t, h, "GET", "/templates", nil, 200)
	apiCall(t, h, "GET", "/templates/"+template.ID, nil, 200)
	apiCall(t, h, "GET", "/history?object_type=template&object_id="+template.ID, nil, 200)
	apiCall(t, h, "POST", "/templates", map[string]any{"revision": 1}, 400)
	command, preview := managedPreview(t, s, v.ID, SeriesCommand{Action: "archive"})
	raw, _ := json.Marshal(command)
	one := apiCall(t, h, "POST", "/series/"+v.ID+"/archive", json.RawMessage(raw), 200)
	two := apiCall(t, h, "POST", "/series/"+v.ID+"/archive", json.RawMessage(raw), 200)
	if one.Body.String() != two.Body.String() || preview.SeriesID != v.ID {
		t.Fatal("non-idempotent API")
	}
	n := count(t, db, "projects")
	drainSeries(t, s, MaterializeInput{Through: "2027-12-01"})
	if n != count(t, db, "projects") {
		t.Fatal("archived generated projects")
	}
	// New commands participate in ordinary revision checking.
	copy := template.Snapshot
	_, e = s.TemplateCommand(ctx, template.ID, "versions", TemplateInput{RequestID: "new-v", ExpectedRevision: template.Revision, Snapshot: &copy})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.TemplateCommand(ctx, template.ID, "archive", TemplateInput{RequestID: "stale-v", ExpectedRevision: template.Revision})
	api, ok := e.(*APIError)
	if !ok || api.Status != http.StatusConflict {
		t.Fatal(e)
	}
	old, e := s.Template(ctx, template.ID, template.VersionID)
	if e != nil || !reflect.DeepEqual(old.Snapshot, template.Snapshot) {
		t.Fatal("historical template changed", e)
	}
}

func TestProjectCompactCommandsResumeAndExplicitTaskShift(t *testing.T) {
	db, s, _ := seriesFixture(t)
	template := createTemplateTest(t, s, cleaningSnapshot())
	v := projectSeriesTest(t, s, template, "2026-11-16", 14)
	// Pause not-yet-created cycles, then explicitly process the compact record.
	managedApply(t, s, v.ID, SeriesCommand{Action: "pause", StartDate: "2027-02-01", EndDate: "2027-03-31"})
	drainSeries(t, s, MaterializeInput{SeriesID: v.ID, Through: "2027-03-31"})
	current := getSeriesTest(t, s, v.ID)
	var compact []TaskOccurrenceDTO
	for _, o := range current.Occurrences {
		if o.Compact {
			compact = append(compact, o)
		}
	}
	if len(compact) < 3 {
		t.Fatal(current)
	}
	id := compact[0].ID
	in := projectOccurrenceInput(t, s, id)
	in.PlannedDate = ptr("2026-12-25")
	in.TasksDateDecision = "shift"
	r := occurrenceApply(t, s, id, "reschedule", in)
	if r.Project == nil || *taskNamed(t, *r.Project, "Кухня").PlannedDate != "2026-12-26" {
		t.Fatal(r)
	}
	in = projectOccurrenceInput(t, s, id)
	in.PlannedDate = ptr("2026-12-31")
	in.TasksDateDecision = "shift"
	r = occurrenceApply(t, s, id, "reschedule", in)
	if *taskNamed(t, *r.Project, "Кухня").PlannedDate != "2027-01-01" || *taskNamed(t, *r.Project, "Комнаты").DueDate != "2027-01-02" {
		t.Fatal(r)
	}
	id = compact[1].ID
	in = projectOccurrenceInput(t, s, id)
	in.CompletedDate = ptr("2026-11-15")
	in.TasksDecision = "complete_open"
	done := occurrenceApply(t, s, id, "complete", in)
	for _, task := range done.Project.Tasks {
		if task.Status != "completed" || *task.CompletedDate != "2026-11-15" {
			t.Fatal(task)
		}
	}
	id = compact[2].ID
	cancelled := occurrenceApply(t, s, id, "cancel", projectOccurrenceInput(t, s, id))
	if !cancelled.Compact || cancelled.ProjectStatus != "cancelled" {
		t.Fatal(cancelled)
	}
	restored := occurrenceApply(t, s, id, "restore", projectOccurrenceInput(t, s, id))
	if restored.Project == nil || restored.Project.Status != "planned" {
		t.Fatal(restored)
	}
	// Early resume preserves acknowledged history and restores future unattended cycles.
	current = getSeriesTest(t, s, v.ID)
	var remaining *TaskOccurrenceDTO
	for i := range current.Occurrences {
		if current.Occurrences[i].Compact && current.Occurrences[i].ProjectStatus == "skipped" {
			remaining = &current.Occurrences[i]
			break
		}
	}
	if remaining != nil {
		occurrenceApply(t, s, remaining.ID, "review", projectOccurrenceInput(t, s, remaining.ID))
	}
	pauseID := current.Pauses[0].ID
	managedApply(t, s, v.ID, SeriesCommand{Action: "resume", PauseID: pauseID})
	if remaining != nil {
		o, _ := currentOccurrence(t, s, remaining.ID)
		if !o.Compact || o.ProjectStatus != "skipped" {
			t.Fatal("reviewed skip revived", o)
		}
	}
	if e := checkIntegrity(ctx, db.db); e != nil {
		t.Fatal(e)
	}
}
func TestProjectAtomicCloseConflictAndConcurrentTemplateVersion(t *testing.T) {
	db, s, h := seriesFixture(t)
	template := createTemplateTest(t, s, cleaningSnapshot())
	v := projectSeriesTest(t, s, template, "2026-11-16", 14)
	o := v.Occurrences[0]
	before := recurrenceDBSnapshot(t, db)
	in := projectOccurrenceInput(t, s, o.ID)
	in.TasksDecision = "skip_open"
	one := o.Project.Tasks[0]
	in.RelatedRevisions[one.ID] = 99
	if _, e := s.OccurrenceCommand(ctx, o.ID, "skip", in); e == nil || before != recurrenceDBSnapshot(t, db) {
		t.Fatal("child revision conflict was not atomic", e)
	}
	exec(t, db, `CREATE TRIGGER fail_project_incident BEFORE INSERT ON occurrence_incidents BEGIN SELECT RAISE(ABORT,'incident failure'); END`)
	before = recurrenceDBSnapshot(t, db)
	in = projectOccurrenceInput(t, s, o.ID)
	in.TasksDecision = "skip_open"
	if _, e := s.OccurrenceCommand(ctx, o.ID, "skip", in); e == nil || before != recurrenceDBSnapshot(t, db) {
		t.Fatal("partial project skip committed", e)
	}
	exec(t, db, "DROP TRIGGER fail_project_incident")
	// A generic project closure cannot bypass the composite operation.
	apiCall(t, h, "POST", "/projects/"+o.Project.ID+"/cancel", map[string]any{"expected_revision": o.Project.Revision}, 409)
	for _, q := range []string{"UPDATE occurrences SET project_state=NULL WHERE id='" + o.ID + "'", "UPDATE occurrences SET project_link=NULL WHERE id='" + o.ID + "'", "DELETE FROM projects WHERE id='" + o.Project.ID + "'"} {
		if e := db.Transaction(ctx, func(tx *sql.Tx) error { _, e := tx.Exec(q); return e }); e == nil {
			t.Fatal("link constraint bypass", q)
		}
	}
	closeInput := projectOccurrenceInput(t, s, o.ID)
	closeInput.TasksDecision = "cancel_open"
	closed := occurrenceApply(t, s, o.ID, "cancel", closeInput)
	apiCall(t, h, "POST", "/tasks", map[string]any{"title": "Cannot create active task", "project_id": o.Project.ID}, 422)
	closedTask := closed.Project.Tasks[0]
	apiCall(t, h, "POST", "/tasks/"+closedTask.ID+"/restore", map[string]any{"expected_revision": closedTask.Revision}, 422)
	restoreInput := projectOccurrenceInput(t, s, o.ID)
	restoreInput.TasksDecision = "restore_closed"
	occurrenceApply(t, s, o.ID, "restore", restoreInput)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, _ := operation()
			snapshot := cleaningSnapshot()
			_, e := s.TemplateCommand(ctx, template.ID, "versions", TemplateInput{RequestID: key, ExpectedRevision: template.Revision, Snapshot: &snapshot})
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	ok, conflicts := 0, 0
	for e := range results {
		if e == nil {
			ok++
		} else if api, yes := e.(*APIError); yes && api.Status == 409 {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatal(ok, conflicts)
	}
}
func TestProjectMigrationPreservesV5HistoryAndLinks(t *testing.T) {
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "v5.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, e = db.Exec("PRAGMA foreign_keys=ON"); e != nil {
		t.Fatal(e)
	}
	if e = migrate(ctx, db, migrations[:5]); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO project_templates(id,title) VALUES('t','Legacy'); INSERT INTO template_versions(id,template_id,version,snapshot_json) VALUES('tv','t',1,'{}'); INSERT INTO series(id,source_type,anchor_mode) VALUES('s','project','schedule'); INSERT INTO series_versions(id,series_id,version,effective_from,rule_json,source_snapshot_json,template_version_id) VALUES('sv','s',1,'2026-01-01','{}','{}','tv'); INSERT INTO occurrences(id,series_id,rule_version_id,cycle_index,base_date,adjusted_date) VALUES('o','s','sv',0,'2026-01-01','2026-01-01'); INSERT INTO projects(id,title,occurrence_id,template_version_id) VALUES('p','Legacy','o','tv'); INSERT INTO history_events(id,project_id,actor,action,payload_json,operation_id) VALUES('h','p','owner','created','{"original":true}','op')`); e != nil {
		t.Fatal(e)
	}
	if e = migrate(ctx, db, migrations); e != nil {
		t.Fatal(e)
	}
	if e = migrate(ctx, db, migrations); e != nil {
		t.Fatal(e)
	}
	var link, state, payload, op string
	if e = db.QueryRow("SELECT project_link,project_state FROM occurrences WHERE id='o'").Scan(&link, &state); e != nil || link != "o" || state != "materialized" {
		t.Fatal(link, state, e)
	}
	if e = db.QueryRow("SELECT payload_json,operation_id FROM history_events WHERE id='h'").Scan(&payload, &op); e != nil || payload != `{"original":true}` || op != "op" {
		t.Fatal(payload, op, e)
	}
	if e = checkIntegrity(ctx, db); e != nil {
		t.Fatal(e)
	}
}

func TestProjectRuleChangeDoesNotImplicitlyChangeEarlierTreeVersion(t *testing.T) {
	_, s, _ := seriesFixture(t)
	template := createTemplateTest(t, s, cleaningSnapshot())
	v := projectSeriesTest(t, s, template, "2026-11-16", 14)
	newer := cleaningSnapshot()
	newer.Tasks = append(newer.Tasks, ProjectTemplateTask{Key: "new", Title: "Новая задача"})
	version, e := s.TemplateCommand(ctx, template.ID, "versions", TemplateInput{RequestID: "future-tree", ExpectedRevision: template.Revision, Snapshot: &newer})
	if e != nil {
		t.Fatal(e)
	}
	managedApply(t, s, v.ID, SeriesCommand{Action: "change_template", FromCycle: ptr(int64(3)), TemplateVersionID: version.Template.VersionID})
	rule := v.Rule.Rule
	rule.Interval = 7
	managedApply(t, s, v.ID, SeriesCommand{Action: "change_rule", FromCycle: ptr(int64(1)), FirstDate: "2026-11-24", Rule: &rule, TasksDateDecision: "shift"})
	current := getSeriesTest(t, s, v.ID)
	if len(current.Occurrences[1].Project.Tasks) != 4 || *current.Occurrences[1].Project.TemplateVersionID != template.VersionID || *current.Occurrences[1].TemplateVersionID != template.VersionID {
		t.Fatal("rule edit changed the existing tree", current.Occurrences[1])
	}
	if len(current.Occurrences[3].Project.Tasks) != 5 {
		t.Fatal(current.Occurrences[3])
	}
	for _, date := range []string{"2024-02-28", "2025-02-28", "2026-12-31"} {
		c, e := composeProject(cleaningSnapshot(), date)
		if e != nil {
			t.Fatal(e)
		}
		want := map[string]string{"2024-02-28": "2024-02-29", "2025-02-28": "2025-03-01", "2026-12-31": "2027-01-01"}[date]
		if *c.Tasks[1].PlannedDate != want {
			t.Fatal(c)
		}
	}
}

func TestProjectClosureProtectsIndependentTaskSeriesSourceAfterMovingInstances(t *testing.T) {
	db, s, h := seriesFixture(t)
	template := createTemplateTest(t, s, cleaningSnapshot())
	v := projectSeriesTest(t, s, template, "2026-11-16", 14)
	o := v.Occurrences[0]
	task := taskNamed(t, *o.Project, "Без даты")
	input := seriesInput("nested-independent", "2026-11-16", recurrence.Schedule, 45)
	input.Task = nil
	input.ExistingTaskID = task.ID
	input.ExpectedRevision = task.Revision
	// New nested schedules are blocked; legacy source references must still
	// protect project closure after upgrading an existing database.
	if _, e := s.CreateSeries(ctx, input); e == nil {
		t.Fatal("new double recurrence accepted")
	}
	legacyProject := project(t, h, "Legacy independent source")
	input.Task = &TaskTemplate{Title: "Legacy independent task", Priority: "normal", ProjectID: &legacyProject.ID}
	input.ExistingTaskID = ""
	input.ExpectedRevision = 0
	nested, e := s.CreateSeries(ctx, input)
	if e != nil {
		t.Fatal(e)
	}
	legacyVersion, _ := operation()
	exec(t, db, `INSERT INTO series_versions(id,series_id,version,effective_from,rule_json,source_snapshot_json,created_at)
      SELECT ?,series_id,version+1,effective_from,rule_json,json_set(source_snapshot_json,'$.project_id',?),created_at FROM series_versions WHERE id=?`, legacyVersion, o.Project.ID, nested.RuleVersionID)
	for _, cycle := range nested.Occurrences {
		apiCall(t, h, "PATCH", "/tasks/"+cycle.Task.ID, map[string]any{"expected_revision": cycle.Task.Revision, "project_id": nil}, 200)
	}
	request := projectOccurrenceInput(t, s, o.ID)
	request.TasksDecision = "skip_open"
	before := recurrenceDBSnapshot(t, db)
	if _, e = s.OccurrenceCommand(ctx, o.ID, "skip", request); e == nil || before != recurrenceDBSnapshot(t, db) {
		t.Fatal("independent source was silently blocked", e)
	}
	managedApply(t, s, nested.ID, SeriesCommand{Action: "archive"})
	request = projectOccurrenceInput(t, s, o.ID)
	request.TasksDecision = "skip_open"
	result := occurrenceApply(t, s, o.ID, "skip", request)
	if result.Project.Status != "skipped" {
		t.Fatal(result)
	}
}

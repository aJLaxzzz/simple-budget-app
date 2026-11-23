package planner

import (
	"budget/internal/planner/recurrence"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTaskBlueprintVersionsContextIdempotenceAndIsolation(t *testing.T) {
	db, s, h := seriesFixture(t)
	source := task(t, h, map[string]any{"title": "Заготовка", "description": "Описание", "priority": "high", "color_id": "rose", "icon_id": "lucide:house", "planned_date": "2026-11-20", "due_date": "2026-11-21"})
	source = value[TaskDTO](t, apiCall(t, h, "POST", "/tasks/"+source.ID+"/complete", map[string]any{"expected_revision": source.Revision, "completed_date": "2026-11-15"}, 200))
	n := len(history(t, h, "task", source.ID))
	in := TaskTemplateInput{RequestID: "save-task-template", SourceTaskID: source.ID, ExpectedRevision: source.Revision}
	r := value[CommandResult](t, apiCall(t, h, "POST", "/task-templates", in, 201))
	tpl := r.Template
	again := value[CommandResult](t, apiCall(t, h, "POST", "/task-templates", in, 201))
	if again.Template.ID != tpl.ID || count(t, db, "tasks") != 1 {
		t.Fatal("duplicate")
	}
	if tpl.Kind != "task" || tpl.Snapshot.Tasks[0].PlannedOffset != nil || tpl.Snapshot.Tasks[0].DueOffset != nil {
		t.Fatal(tpl)
	}
	current := value[TaskDTO](t, apiCall(t, h, "GET", "/tasks/"+source.ID, nil, 200))
	if current.Status != "completed" || *current.CompletedDate != "2026-11-15" || current.SavedTemplateID == nil || len(history(t, h, "task", source.ID)) != n+1 {
		t.Fatal(current)
	}
	apiCall(t, h, "GET", "/templates/"+tpl.ID, nil, 404)
	create := TaskTemplateInput{RequestID: "instantiate-task", ExpectedRevision: tpl.Revision}
	a := value[CommandResult](t, apiCall(t, h, "POST", "/task-templates/"+tpl.ID+"/instantiate", create, 201)).Task
	if a.ID == source.ID || a.Status != "planned" || a.PlannedDate != nil || a.DueDate != nil || a.CompletedDate != nil || a.SourceTemplateID == nil || a.SavedTemplateID != nil || *a.ColorID != "rose" {
		t.Fatal(a)
	}
	if b := value[CommandResult](t, apiCall(t, h, "POST", "/task-templates/"+tpl.ID+"/instantiate", create, 201)).Task; b.ID != a.ID {
		t.Fatal("retry duplicated")
	}
	p := project(t, h, "Контекст")
	create.RequestID = "instantiate-context"
	create.ProjectID = &p.ID
	create.PlannedDate = ptr("2026-12-01")
	b := value[CommandResult](t, apiCall(t, h, "POST", "/task-templates/"+tpl.ID+"/instantiate", create, 201)).Task
	if *b.ProjectID != p.ID || *b.PlannedDate != "2026-12-01" {
		t.Fatal(b)
	}
	v := TaskTemplateInput{RequestID: "new-version", ExpectedRevision: tpl.Revision, Snapshot: &TaskBlueprint{Title: "Новое название", Priority: "low", Appearance: Appearance{ColorID: ptr("teal")}}}
	updated := value[CommandResult](t, apiCall(t, h, "POST", "/task-templates/"+tpl.ID+"/versions", v, 201)).Template
	if updated.Version != 2 || updated.VersionID == tpl.VersionID {
		t.Fatal(updated)
	}
	apiCall(t, h, "POST", "/task-templates/"+tpl.ID+"/versions", TaskTemplateInput{RequestID: "stale", ExpectedRevision: 1, Snapshot: v.Snapshot}, 409)
	unchanged := value[TaskDTO](t, apiCall(t, h, "GET", "/tasks/"+a.ID, nil, 200))
	if unchanged.Title != a.Title {
		t.Fatal("version rewrote task")
	}
	apiCall(t, h, "POST", "/task-templates/"+tpl.ID+"/archive", TaskTemplateInput{RequestID: "archive", ExpectedRevision: 2}, 200)
	apiCall(t, h, "POST", "/task-templates/"+tpl.ID+"/instantiate", TaskTemplateInput{RequestID: "archived", ExpectedRevision: 3}, 422)
	apiCall(t, h, "POST", "/task-templates", map[string]any{"request_id": "bad", "snapshot": map[string]any{"title": "x", "status": "completed"}}, 400)
	apiCall(t, h, "POST", "/task-templates", map[string]any{"request_id": "bad", "snapshot": map[string]any{"title": "x", "priority": "normal", "icon_id": "<script>"}}, 422)
	if len(history(t, h, "template", tpl.ID)) != 5 {
		t.Fatal("task template history inaccessible")
	}
	// Both template kinds, immutable versions and provenance survive backup/restore.
	backup := filepath.Join(t.TempDir(), "backup.db")
	if e := db.Export(ctx, backup); e != nil {
		t.Fatal(e)
	}
	before := recurrenceDBSnapshot(t, db)
	if e := db.Restore(ctx, backup); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, recurrenceDBSnapshot(t, db)) {
		t.Fatal("backup links lost")
	}
	_ = s
}

func TestProjectSourceAtomicPreviewAndIndependentRepeats(t *testing.T) {
	db, _, h := seriesFixture(t)
	p := value[ProjectDTO](t, apiCall(t, h, "POST", "/projects", map[string]any{"title": "Уборка", "start_date": "2026-11-21", "color_id": "teal"}, 201))
	root := task(t, h, map[string]any{"title": "Комната", "project_id": p.ID, "planned_date": "2026-11-21", "due_date": "2026-11-23", "icon_id": "lucide:house"})
	child := task(t, h, map[string]any{"title": "Полка", "project_id": p.ID, "parent_task_id": root.ID, "planned_date": "2026-11-22"})
	apiCall(t, h, "POST", "/tasks/"+child.ID+"/complete", map[string]any{"expected_revision": child.Revision, "completed_date": "2026-11-15"}, 200)
	in := ProjectSourceInput{ExpectedRevision: p.Revision, AnchorDate: "2026-11-21", FirstDate: "2026-12-05", Rule: &recurrence.Rule{SchemaVersion: 1, Timezone: recurrence.Timezone, Unit: recurrence.Weeks, Interval: 2, Mode: recurrence.Schedule, AllowedDays: recurrence.AllowedDays{Mode: recurrence.AnyDay}, Adjustment: recurrence.NoAdjustment}}
	before := recurrenceDBSnapshot(t, db)
	preview := value[ProjectSourcePreview](t, apiCall(t, h, "POST", "/projects/"+p.ID+"/template-preview", in, 200))
	if !reflect.DeepEqual(before, recurrenceDBSnapshot(t, db)) {
		t.Fatal("preview wrote")
	}
	if len(preview.Items) != 5 || len(preview.Snapshot.Tasks) != 2 {
		t.Fatal(preview)
	}
	in.PreviewToken = preview.PreviewToken
	in.RequestID = "repeat-project"
	out := value[CommandResult](t, apiCall(t, h, "POST", "/projects/"+p.ID+"/save-template", in, 201))
	if out.Project.ID != p.ID || out.Project.OccurrenceID != nil || out.Project.RepeatSeriesID == nil || out.Project.StartDate == nil || *out.Project.StartDate != "2026-11-21" {
		t.Fatal(out.Project)
	}
	instance := out.Series.Occurrences[0].Project
	if instance.ID == p.ID || *instance.ColorID != "teal" || len(instance.Tasks) != 2 {
		t.Fatal(instance)
	}
	for _, task := range instance.Tasks {
		if task.ID == root.ID || task.ID == child.ID || task.Status != "planned" || task.CompletedDate != nil {
			t.Fatal(task)
		}
	}
	if *taskNamed(t, *instance, "Полка").PlannedDate != "2026-12-06" || *taskNamed(t, *instance, "Комната").DueDate != "2026-12-07" {
		t.Fatal(instance)
	}
	again := value[CommandResult](t, apiCall(t, h, "POST", "/projects/"+p.ID+"/save-template", in, 201))
	if again.Series.ID != out.Series.ID {
		t.Fatal("retry duplicated")
	}
	original := value[ProjectDTO](t, apiCall(t, h, "GET", "/projects/"+p.ID, nil, 200))
	if taskNamed(t, original, "Полка").Status != "completed" {
		t.Fatal("history lost")
	}
	if e := checkIntegrity(ctx, db.db); e != nil {
		t.Fatal(e)
	}
	in.RequestID = "again"
	in.ExpectedRevision = original.Revision
	apiCall(t, h, "POST", "/projects/"+p.ID+"/template-preview", in, 409)
}
func TestProjectSourceStaleTreeAndRollback(t *testing.T) {
	db, _, h := seriesFixture(t)
	p := project(t, h, "Оригинал")
	task(t, h, map[string]any{"title": "Одна", "project_id": p.ID})
	in := ProjectSourceInput{ExpectedRevision: p.Revision, AnchorDate: "2026-11-21"}
	review := value[ProjectSourcePreview](t, apiCall(t, h, "POST", "/projects/"+p.ID+"/template-preview", in, 200))
	in.PreviewToken = review.PreviewToken
	in.RequestID = "save"
	task(t, h, map[string]any{"title": "Вторая", "project_id": p.ID})
	apiCall(t, h, "POST", "/projects/"+p.ID+"/save-template", in, 409)
	review = value[ProjectSourcePreview](t, apiCall(t, h, "POST", "/projects/"+p.ID+"/template-preview", in, 200))
	in.PreviewToken = review.PreviewToken
	exec(t, db, `CREATE TRIGGER reject_source_history BEFORE INSERT ON history_events WHEN NEW.action='saved_as_template' BEGIN SELECT RAISE(ABORT,'test'); END`)
	before := recurrenceDBSnapshot(t, db)
	apiCall(t, h, "POST", "/projects/"+p.ID+"/save-template", in, 422)
	if !reflect.DeepEqual(before, recurrenceDBSnapshot(t, db)) {
		t.Fatal("partial template or history")
	}
	exec(t, db, "DROP TRIGGER reject_source_history")
	result := value[CommandResult](t, apiCall(t, h, "POST", "/projects/"+p.ID+"/save-template", in, 201))
	if len(result.Template.Snapshot.Tasks) != 2 {
		t.Fatal(result)
	}
	// SQL protects mismatched template/version provenance even outside the service.
	e := db.Transaction(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec("INSERT INTO entity_template_links(project_id,template_id,version_id,role,created_at) VALUES(?,?,?,?,?)", p.ID, result.Template.ID, "unknown", "saved", "now")
		return e
	})
	if e == nil {
		t.Fatal("invalid provenance accepted")
	}
}

func TestProjectSourceCreationRollbackAndConcurrentReplay(t *testing.T) {
	db, s, h := seriesFixture(t)
	p := project(t, h, "Повторять безопасно")
	task(t, h, map[string]any{"title": "Дерево", "project_id": p.ID, "planned_date": "2026-11-21"})
	rule := seriesInput("unused", "2026-12-01", recurrence.Schedule, 14).Rule
	in := ProjectSourceInput{RequestID: "atomic-project-repeat", ExpectedRevision: p.Revision, AnchorDate: "2026-11-21", FirstDate: "2026-12-01", Rule: &rule}
	preview, e := s.ProjectTemplatePreview(ctx, p.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	in.PreviewToken = preview.PreviewToken
	exec(t, db, `CREATE TRIGGER reject_new_tree BEFORE INSERT ON tasks BEGIN SELECT RAISE(ABORT,'test tree rollback'); END`)
	before := recurrenceDBSnapshot(t, db)
	if _, e = s.SaveProjectTemplate(ctx, p.ID, in); e == nil {
		t.Fatal("expected injected failure")
	}
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("template/series/project partially committed")
	}
	exec(t, db, "DROP TRIGGER reject_new_tree")
	results := make(chan CommandResult, 4)
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() { r, e := s.SaveProjectTemplate(ctx, p.ID, in); results <- r; errs <- e }()
	}
	var first string
	for i := 0; i < 4; i++ {
		r := <-results
		e := <-errs
		if e != nil {
			t.Fatal(e)
		}
		if first == "" {
			first = r.Series.ID
		}
		if first != r.Series.ID {
			t.Fatal("parallel duplicate")
		}
	}
	if count(t, db, "series") != 1 || count(t, db, "project_templates") != 1 {
		t.Fatal("parallel duplicate")
	}
	// The snapshots and source link are included in restored SQLite, too.
	file := filepath.Join(t.TempDir(), "repeating-project.db")
	if e = db.Export(ctx, file); e != nil {
		t.Fatal(e)
	}
	if e = db.Restore(ctx, file); e != nil {
		t.Fatal(e)
	}
	restored := value[ProjectDTO](t, apiCall(t, h, "GET", "/projects/"+p.ID, nil, 200))
	if restored.RepeatSeriesID == nil || *restored.RepeatSeriesID != first {
		t.Fatal("restore lost source association")
	}
	for _, o := range getSeriesTest(t, s, first).Occurrences {
		if o.Project == nil || len(o.Project.Tasks) != 1 {
			t.Fatal("restore lost tree")
		}
	}
}

func TestTaskTemplateSourceAndHistoryFailuresAreAtomic(t *testing.T) {
	db, s, h := seriesFixture(t)
	p := project(t, h, "Цвет")
	p = value[ProjectDTO](t, apiCall(t, h, "PATCH", "/projects/"+p.ID, map[string]any{"expected_revision": p.Revision, "color_id": "purple", "icon_id": "lucide:house"}, 200))
	source := task(t, h, map[string]any{"title": "Наследует", "project_id": p.ID})
	in := TaskTemplateInput{RequestID: "task-blueprint-source", SourceTaskID: source.ID, ExpectedRevision: source.Revision}
	exec(t, db, `CREATE TRIGGER reject_blueprint_history BEFORE INSERT ON history_events WHEN NEW.action='saved_as_template' BEGIN SELECT RAISE(ABORT,'test'); END`)
	before := recurrenceDBSnapshot(t, db)
	if _, e := s.TaskTemplateCommand(ctx, "", "create", in); e == nil {
		t.Fatal("injected failure")
	}
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("partial source save")
	}
	exec(t, db, "DROP TRIGGER reject_blueprint_history")
	r, e := s.TaskTemplateCommand(ctx, "", "create", in)
	if e != nil {
		t.Fatal(e)
	}
	if *r.Template.Snapshot.Tasks[0].ColorID != "purple" {
		t.Fatal("effective appearance not captured")
	}
	in.RequestID = "stale-source"
	if _, e = s.TaskTemplateCommand(ctx, "", "create", in); e == nil {
		t.Fatal("stale source accepted")
	}
	exec(t, db, `CREATE TRIGGER reject_instance_history BEFORE INSERT ON history_events WHEN NEW.action='created_from_template' BEGIN SELECT RAISE(ABORT,'test'); END`)
	before = recurrenceDBSnapshot(t, db)
	_, e = s.TaskTemplateCommand(ctx, r.Template.ID, "instantiate", TaskTemplateInput{RequestID: "rollback-instance", ExpectedRevision: r.Template.Revision})
	if e == nil || before != recurrenceDBSnapshot(t, db) {
		t.Fatal("partial instance")
	}
}

package planner

import (
	"budget/internal/storageguard"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func dateRepairTestPreview(t *testing.T, s *Service, h http.Handler, id string) (ProjectDateRepairInput, ProjectDateRepairPreview) {
	t.Helper()
	v := getSeriesTest(t, s, id)
	in := ProjectDateRepairInput{ExpectedRevision: v.Revision}
	p := value[ProjectDateRepairPreview](t, apiCall(t, h, "POST", "/series/"+id+"/dates-preview", in, 200))
	in.PreviewToken = p.PreviewToken
	return in, p
}
func safeDateChanges(p ProjectDateRepairPreview) int {
	n := 0
	for _, r := range p.Rows {
		for _, c := range r.Changes {
			if c.Safe {
				n++
			}
		}
	}
	return n
}
func TestBeta31RepairKeepDatesAndYear(t *testing.T) {
	db, s, h := seriesFixture(t)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	source := beta3Draft(t, h, "beta31-source")
	id := *source.RepeatSeriesID
	v := getSeriesTest(t, s, id)
	// Reproduce the confirmed user action: change project origin, keep task dates.
	rule := v.Rule.Rule
	managedApply(t, s, id, SeriesCommand{Action: "change_rule", Rule: &rule, FromCycle: ptr(int64(1)), FirstDate: "2026-12-15", TasksDateDecision: "keep"})
	in, preview := dateRepairTestPreview(t, s, h, id)
	if safeDateChanges(preview) == 0 {
		t.Fatal("confirmed defect not reproduced")
	}
	// Date-only repair must preserve unrelated edits and manual ordering.
	task := preview.Rows[0].Changes[0].Before
	if e := db.Transaction(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec(`UPDATE tasks SET description='personal note',sort_order=42,revision=revision+1 WHERE id=?`, task.ID)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	in.RequestID = "stale-beta31"
	apiCall(t, h, "POST", "/series/"+id+"/dates-apply", in, 409)
	in, preview = dateRepairTestPreview(t, s, h, id)
	in.RequestID = "repair-beta31"
	// Failure after at least one write must roll everything back.
	exec(t, db, `CREATE TRIGGER beta31_fail BEFORE UPDATE ON series BEGIN SELECT RAISE(ABORT,'forced rollback'); END`)
	apiCall(t, h, "POST", "/series/"+id+"/dates-apply", in, 422)
	_, afterFail := dateRepairTestPreview(t, s, h, id)
	if afterFail.PreviewToken != preview.PreviewToken {
		t.Fatal("partial transaction")
	}
	exec(t, db, `DROP TRIGGER beta31_fail`)
	apiCall(t, h, "POST", "/series/"+id+"/dates-apply", in, 200)
	apiCall(t, h, "POST", "/series/"+id+"/dates-apply", in, 200)
	fixed, e := s.Task(ctx, task.ID)
	if e != nil || fixed.Description != "personal note" || fixed.SortOrder != 42 {
		t.Fatal("unrelated edit lost", e)
	}
	in, preview = dateRepairTestPreview(t, s, h, id)
	if safeDateChanges(preview) != 0 {
		t.Fatal("not idempotent")
	}
	oldRevision := in.ExpectedRevision
	in.RequestID = "noop-beta31"
	apiCall(t, h, "POST", "/series/"+id+"/dates-apply", in, 200)
	if getSeriesTest(t, s, id).Revision != oldRevision {
		t.Fatal("correct rows changed")
	}
	drainSeries(t, s, MaterializeInput{SeriesID: id, Through: "2027-12-31"})
	_, preview = dateRepairTestPreview(t, s, h, id)
	if len(preview.Rows) < 52 || safeDateChanges(preview) != 0 {
		t.Fatal("year of instances", len(preview.Rows), safeDateChanges(preview))
	}
	for _, r := range preview.Rows {
		for _, task := range r.Project.Tasks {
			if task.PlannedDate == nil {
				t.Fatal("date missing")
			}
		}
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot.sqlite")
	if e = db.Export(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(ctx, snapshot)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	s2 := NewService(reopened)
	s2.now = s.now
	_, p2 := dateRepairTestPreview(t, s2, NewHandler(s2), id)
	if safeDateChanges(p2) != 0 {
		t.Fatal("restart dates")
	}
	if e = reopened.Restore(ctx, snapshot); e == nil {
		t.Fatal("same file must not restore")
	}
	if e = db.Restore(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
}
func TestBeta31ProtectIndividualDatesAndClosedTasks(t *testing.T) {
	db, s, h := seriesFixture(t)
	source := beta3Draft(t, h, "protected31")
	id := *source.RepeatSeriesID
	v := getSeriesTest(t, s, id)
	rule := v.Rule.Rule
	managedApply(t, s, id, SeriesCommand{Action: "change_rule", Rule: &rule, FromCycle: ptr(int64(1)), FirstDate: "2026-12-15", TasksDateDecision: "keep"})
	_, p := dateRepairTestPreview(t, s, h, id)
	a := p.Rows[0].Changes[0].Before
	b := p.Rows[0].Changes[1].Before
	exec(t, db, `UPDATE tasks SET planned_date='2027-02-01',revision=revision+1 WHERE id='`+a.ID+`';UPDATE tasks SET status='cancelled',revision=revision+1 WHERE id='`+b.ID+`'`)
	completed := p.Rows[1].Changes[0].Before
	withSession := p.Rows[1].Changes[1].Before
	if e := db.Transaction(ctx, func(tx *sql.Tx) error {
		if _, e := tx.Exec(`UPDATE tasks SET status='completed',completed_date='2026-11-24',completed_at='2026-11-24T12:00:00Z',revision=revision+1 WHERE id=?`, completed.ID); e != nil {
			return e
		}
		_, e := tx.Exec(`INSERT INTO sessions(id,task_id,date,start_time,end_time,status) VALUES('protected-session31',?,'2026-12-15','10:00','11:00','in_progress')`, withSession.ID)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	in, p := dateRepairTestPreview(t, s, h, id)
	for _, c := range p.Rows[0].Changes {
		if (c.Before.ID == a.ID || c.Before.ID == b.ID) && c.Safe {
			t.Fatal("protected date eligible")
		}
	}
	for _, c := range p.Rows[1].Changes {
		if (c.Before.ID == completed.ID || c.Before.ID == withSession.ID) && c.Safe {
			t.Fatal("completed/session task eligible")
		}
	}
	in.RequestID = "protected-apply31"
	apiCall(t, h, "POST", "/series/"+id+"/dates-apply", in, 200)
	after, _ := s.Task(ctx, a.ID)
	if *after.PlannedDate != "2027-02-01" {
		t.Fatal("manual move lost")
	}
	after, _ = s.Task(ctx, b.ID)
	if after.Status != "cancelled" || !reflect.DeepEqual(after.PlannedDate, b.PlannedDate) {
		t.Fatal("closed task changed")
	}
	for _, original := range []TaskDTO{completed, withSession} {
		now, e := s.Task(ctx, original.ID)
		if e != nil || !reflect.DeepEqual(now.PlannedDate, original.PlannedDate) || !reflect.DeepEqual(now.DueDate, original.DueDate) {
			t.Fatal("protected completed/session date changed", e)
		}
	}

}

// Opt-in private verification. Input MUST be a standalone snapshot; this test
// copies it to t.TempDir and never opens the supplied path through Store.Open.
func TestBeta31PrivateSnapshot(t *testing.T) {
	path := os.Getenv("BETA31_SNAPSHOT")
	if path == "" {
		t.Skip("private snapshot not supplied")
	}
	if _, e := storageguard.Check(filepath.Dir(path), "development", ""); e != nil {
		t.Fatal(e)
	}
	if e := storageguard.Regular(path, false); e != nil {
		t.Fatal(e)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, e := os.Lstat(path + suffix); !os.IsNotExist(e) {
			t.Fatal("standalone snapshot required")
		}
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	copyPath := filepath.Join(t.TempDir(), "planner.db")
	if e = os.WriteFile(copyPath, raw, 0600); e != nil {
		t.Fatal(e)
	}
	db, e := Open(ctx, copyPath)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := NewService(db)
	s.now = func() time.Time { return time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC) }
	h := NewHandler(s)
	var id string
	if e = db.Transaction(ctx, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT s.id FROM series s JOIN series_versions v ON v.series_id=s.id WHERE s.source_type='project' AND json_extract(v.rule_json,'$.first_date')='2026-12-08' LIMIT 1`).Scan(&id)
	}); e != nil {
		t.Fatal("diagnostic series not found")
	}
	in, p := dateRepairTestPreview(t, s, h, id)
	if safeDateChanges(p) != 70 || len(p.Duplicates) != 1 {
		t.Fatalf("expected 70 date corrections and 1 duplicate, got %d/%d", safeDateChanges(p), len(p.Duplicates))
	}
	before := map[string]TaskDTO{}
	for _, r := range p.Rows {
		for _, t := range r.Project.Tasks {
			before[t.ID] = t
		}
	}
	in.RequestID = "private-repair31"
	apiCall(t, h, "POST", "/series/"+id+"/dates-apply", in, 200)
	_, fixed := dateRepairTestPreview(t, s, h, id)
	if safeDateChanges(fixed) != 0 {
		t.Fatal("private repair incomplete")
	}
	for _, r := range fixed.Rows {
		for _, task := range r.Project.Tasks {
			old := before[task.ID]
			old.PlannedDate = task.PlannedDate
			old.DueDate = task.DueDate
			old.Revision = task.Revision
			old.UpdatedAt = task.UpdatedAt
			if !reflect.DeepEqual(old, task) {
				t.Fatal("unrelated field changed")
			}
		}
	}
	calendarDates := map[string]string{}
	for _, bounds := range [][2]string{{"2026-11-01", "2027-01-31"}, {"2027-02-01", "2027-03-01"}} {
		calendar, e := s.CalendarProjection(ctx, bounds[0], bounds[1])
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range calendar.Items {
			if item.Type == "task" {
				calendarDates[item.TaskID] = item.Date
			}
		}
	}
	for _, row := range fixed.Rows {
		for _, task := range row.Project.Tasks {
			if task.PlannedDate != nil && calendarDates[task.ID] != *task.PlannedDate {
				t.Fatal("private calendar differs from stored task")
			}
		}
	}
	// Explicit separate deletion of the duplicate, retaining the original source.
	dup := fixed.Duplicates[0]
	plan := value[DeletionPreview](t, apiCall(t, h, "GET", "/occurrences/"+*dup.Other.OccurrenceID+"/delete-preview", nil, 200))
	del := DeletionInput{ExpectedRevision: plan.Revision, PreviewToken: plan.PreviewToken, Confirm: true, RequestID: "private-duplicate31"}
	apiCall(t, h, "POST", "/occurrences/"+*dup.Other.OccurrenceID+"/delete", del, 200)
	drainSeries(t, s, MaterializeInput{SeriesID: id})
	_, fixed = dateRepairTestPreview(t, s, h, id)
	if len(fixed.Duplicates) != 0 {
		t.Fatal("duplicate reappeared")
	}
	if _, e = s.Project(ctx, dup.Primary.ID); e != nil {
		t.Fatal("source lost")
	}
	export := filepath.Join(t.TempDir(), "verified.sqlite")
	if e = db.Export(ctx, export); e != nil {
		t.Fatal(e)
	}
	if e = db.Restore(ctx, export); e != nil {
		t.Fatal(e)
	}
	if e = db.Transaction(ctx, func(tx *sql.Tx) error { return checkIntegrity(ctx, tx) }); e != nil {
		t.Fatal(e)
	}
	report, _ := json.Marshal(map[string]int{"repaired_tasks": 70, "retained_projects_before_duplicate_decision": len(p.Rows), "duplicates_removed_on_copy": 1})
	t.Log(string(report))
}

func TestBeta31FridayYearAndIndependentOffsets(t *testing.T) {
	_, s, h := seriesFixture(t)
	source := beta3Draft(t, h, "friday-year31")
	id := *source.RepeatSeriesID
	drainSeries(t, s, MaterializeInput{SeriesID: id, Through: "2027-12-31"})
	_, p := dateRepairTestPreview(t, s, h, id)
	if len(p.Rows) < 52 || safeDateChanges(p) != 0 {
		t.Fatal("year horizon", len(p.Rows))
	}
	for _, r := range p.Rows {
		start, e := time.Parse("2006-01-02", r.ExpectedStart)
		if e != nil || start.Weekday() != time.Friday {
			t.Fatal("weekly anchor shifted")
		}
		expected := map[string]int{"Посудомойка": 0, "Пылесос": 1, "Полы": 2}
		for _, task := range r.Project.Tasks {
			want := start.AddDate(0, 0, expected[task.Title]).Format("2006-01-02")
			if task.PlannedDate == nil || *task.PlannedDate != want {
				t.Fatal("relative planned date")
			}
			if task.Title == "Пылесос" && (task.DueDate == nil || *task.DueDate != start.AddDate(0, 0, 2).Format("2006-01-02")) {
				t.Fatal("deadline must be independent")
			}
		}
	}
}

func TestBeta31EqualAdjustedDatesAreNotDuplicateCycles(t *testing.T) {
	_, s, h := seriesFixture(t)
	source := beta3Draft(t, h, "holidays31")
	id := *source.RepeatSeriesID
	v := getSeriesTest(t, s, id)
	rule := v.Rule.Rule
	rule.Unit = "days"
	rule.AllowedDays.Mode = "weekdays"
	rule.Adjustment = "next"
	managedApply(t, s, id, SeriesCommand{Action: "change_rule", Rule: &rule, FromCycle: ptr(int64(1)), FirstDate: "2026-12-05", TasksDateDecision: "shift"})
	_, p := dateRepairTestPreview(t, s, h, id)
	if len(p.Duplicates) != 0 {
		t.Fatal("different cycles on one adjusted day are not duplicates")
	}
	if len(p.Rows) < 3 || p.Rows[0].ExpectedStart != p.Rows[1].ExpectedStart {
		t.Fatal("overlap fixture absent")
	}
	// A distinct Saturday cycle legitimately adjusted to the source Friday is
	// not the same base period and must not be treated as a source duplicate.
	rule.Adjustment = "previous"
	command, shifted := managedPreview(t, s, id, SeriesCommand{Action: "change_rule", Rule: &rule, FromCycle: ptr(int64(1)), FirstDate: "2026-11-28", TasksDateDecision: "shift"})
	if len(shifted.Conflicts) != 0 {
		t.Fatal("working-day adjustment falsely blocks distinct base cycle")
	}
	apiCall(t, h, "POST", "/series/"+id+"/change-rule", command, 200)
	_, shiftedDates := dateRepairTestPreview(t, s, h, id)
	if len(shiftedDates.Duplicates) != 0 {
		t.Fatal("adjusted overlap incorrectly offered for deletion")
	}

}

func TestBeta31RuleChangeCannotOverlapSource(t *testing.T) {
	_, s, h := seriesFixture(t)
	source := beta3Draft(t, h, "no-source-duplicate31")
	id := *source.RepeatSeriesID
	v := getSeriesTest(t, s, id)
	rule := v.Rule.Rule
	in, p := managedPreview(t, s, id, SeriesCommand{Action: "change_rule", Rule: &rule, FromCycle: ptr(int64(1)), FirstDate: "2026-11-27", TasksDateDecision: "shift"})
	if len(p.Conflicts) == 0 {
		t.Fatal("source period collision not detected")
	}
	apiCall(t, h, "POST", "/series/"+id+"/change-rule", in, 409)
	if getSeriesTest(t, s, id).Revision != v.Revision {
		t.Fatal("collision changed series")
	}
	// The same check is required before a not-yet-materialized cycle.
	future, p := managedPreview(t, s, id, SeriesCommand{Action: "change_rule", Rule: &rule, FromCycle: &v.NextCycle, FirstDate: "2026-11-27", TasksDateDecision: "shift"})
	if len(p.Conflicts) == 0 {
		t.Fatal("future source collision not detected")
	}
	apiCall(t, h, "POST", "/series/"+id+"/change-rule", future, 409)
}

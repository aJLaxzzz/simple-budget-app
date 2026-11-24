package planner

import (
	"budget/internal/planner/recurrence"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManagementUpgradePreservesClosedV4Cycles(t *testing.T) {
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "v4.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, e = db.Exec("PRAGMA foreign_keys=ON"); e != nil {
		t.Fatal(e)
	}
	if e = migrate(ctx, db, migrations[:4]); e != nil {
		t.Fatal(e)
	}
	e = transaction(ctx, db, func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT INTO series(id,source_type,anchor_mode) VALUES('legacy','task','schedule');
INSERT INTO series_versions(id,series_id,version,effective_from,rule_json,source_snapshot_json) VALUES('legacy-version','legacy',1,'2026-11-16','{"first_cycle":0}','{}');
INSERT INTO occurrences(id,series_id,rule_version_id,cycle_index,base_date,adjusted_date,task_link,manual_date,outcome) VALUES('legacy-cancel','legacy','legacy-version',0,'2026-11-16','2026-11-16','legacy-cancel','2026-12-01','cancelled'),('legacy-skip','legacy','legacy-version',1,'2026-11-23','2026-11-23','legacy-skip',NULL,'skipped');
INSERT INTO tasks(id,title,status,planned_date,occurrence_id) VALUES('legacy-task','Сохранённое название','cancelled','2026-12-01','legacy-cancel'),('legacy-skipped-task','Старый пропуск','skipped','2026-11-23','legacy-skip');
INSERT INTO history_events(id,task_id,actor,action,payload_json) VALUES('legacy-event','legacy-task','owner','cancelled','{"keep":"exactly"}');`)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = migrate(ctx, db, migrations); e != nil {
		t.Fatal(e)
	}
	if e = migrate(ctx, db, migrations); e != nil {
		t.Fatal(e)
	}
	e = transaction(ctx, db, func(tx *sql.Tx) error {
		items, e := incidentList(tx, "legacy", "", "", 100, 0)
		if e != nil {
			return e
		}
		if len(items) != 2 {
			t.Fatalf("closed history disappeared or duplicated: %+v", items)
		}
		for _, item := range items {
			want := "needs_decision"
			if item.Kind == "cancelled" {
				want = "processed"
				if item.PlannedDate != "2026-12-01" {
					t.Fatal(item)
				}
			}
			if item.Disposition != want {
				t.Fatal(item)
			}
		}
		var payload string
		if e = tx.QueryRow("SELECT payload_json FROM history_events WHERE id='legacy-event'").Scan(&payload); e != nil {
			return e
		}
		if !json.Valid([]byte(payload)) || payload != `{"keep":"exactly"}` {
			t.Fatal("old history changed", payload)
		}
		var manual, scopes, attention int
		if e = tx.QueryRow("SELECT manual_override FROM occurrences WHERE id='legacy-cancel'").Scan(&manual); e != nil {
			return e
		}
		if e = tx.QueryRow("SELECT count(*) FROM series_version_scopes WHERE series_id='legacy' AND from_cycle=0 AND until_cycle IS NULL").Scan(&scopes); e != nil {
			return e
		}
		if e = tx.QueryRow("SELECT count(*) FROM attention_state WHERE disposition='needs_decision'").Scan(&attention); e != nil {
			return e
		}
		if manual != 1 || scopes != 1 || attention != 1 {
			t.Fatal(manual, scopes, attention)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = checkIntegrity(ctx, db); e != nil {
		t.Fatal(e)
	}
}

func managedPreview(t *testing.T, s *Service, id string, in SeriesCommand) (SeriesCommand, SeriesChangePreview) {
	t.Helper()
	v := getSeriesTest(t, s, id)
	in.ExpectedRevision = v.Revision
	p, e := s.PreviewSeriesChange(ctx, id, in)
	if e != nil {
		t.Fatal(e)
	}
	in.PreviewToken = p.PreviewToken
	in.RequestID, _ = operation()
	return in, p
}
func managedApply(t *testing.T, s *Service, id string, in SeriesCommand) CommandResult {
	t.Helper()
	in, p := managedPreview(t, s, id, in)
	if len(p.CalendarErrors) > 0 {
		t.Fatal(p.CalendarErrors)
	}
	r, e := s.ApplySeriesCommand(ctx, id, in)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func currentOccurrence(t *testing.T, s *Service, id string) (TaskOccurrenceDTO, SeriesDTO) {
	t.Helper()
	var o TaskOccurrenceDTO
	var v SeriesDTO
	e := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		var e error
		o, e = getOccurrence(tx, id)
		if e != nil {
			return e
		}
		v, e = getSeries(tx, o.SeriesID)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	return o, v
}
func occurrenceInput(t *testing.T, s *Service, id string) OccurrenceCommand {
	t.Helper()
	o, v := currentOccurrence(t, s, id)
	key, _ := operation()
	return OccurrenceCommand{RequestID: key, ExpectedRevision: o.Revision, ExpectedTaskRevision: o.Task.Revision, ExpectedSeriesRevision: v.Revision}
}
func occurrenceApply(t *testing.T, s *Service, id, action string, in OccurrenceCommand) TaskOccurrenceDTO {
	t.Helper()
	r, e := s.OccurrenceCommand(ctx, id, action, in)
	if e != nil {
		t.Fatal(e)
	}
	return *r.Occurrence
}
func ptr[T any](x T) *T { return &x }
func allIncidents(t *testing.T, s *Service, id, disposition string) []IncidentDTO {
	t.Helper()
	items, e := s.Incidents(ctx, id, "", disposition, 100, 0)
	if e != nil {
		t.Fatal(e)
	}
	return items
}
func TestManagementSingleMoveAndCommandReplay(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("one-move", "2026-11-16", recurrence.Schedule, 45))
	o := v.Occurrences[0]
	in := occurrenceInput(t, s, o.ID)
	in.PlannedDate = ptr("2026-11-20")
	moved := occurrenceApply(t, s, o.ID, "reschedule", in)
	if moved.ID != o.ID || moved.BaseDate != o.BaseDate || moved.CycleIndex != 0 || !moved.ManualOverride || *moved.ManualDate != "2026-11-20" {
		t.Fatal(moved)
	}
	snapshot := recurrenceDBSnapshot(t, db)
	replay := occurrenceApply(t, s, o.ID, "reschedule", in)
	if !reflect.DeepEqual(moved, replay) || snapshot != recurrenceDBSnapshot(t, db) {
		t.Fatal("replayed command wrote data")
	}
	in.PlannedDate = ptr("2026-11-21")
	if _, e := s.OccurrenceCommand(ctx, o.ID, "reschedule", in); e == nil {
		t.Fatal("reused key accepted")
	}
	drainSeries(t, s, MaterializeInput{Through: "2027-03-01"})
	now := getSeriesTest(t, s, v.ID)
	if now.Occurrences[1].BaseDate != "2026-12-31" || now.Occurrences[2].BaseDate != "2027-02-14" {
		t.Fatal(now)
	}
}
func TestManagementRuleVersionsPreserveHistoryManualWorkAndSessions(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("versions", "2026-11-16", recurrence.Schedule, 7))
	original := v.Occurrences
	completeSeriesTask(t, h, original[0].Task, "2026-11-16")
	closedHistory := history(t, h, "task", original[0].Task.ID)
	apiCall(t, h, "POST", "/tasks/"+original[2].Task.ID+"/reschedule", map[string]any{"expected_revision": 1, "planned_date": "2026-12-01"}, 200)
	apiCall(t, h, "PATCH", "/tasks/"+original[3].Task.ID, map[string]any{"expected_revision": 1, "status": "in_progress"}, 200)
	apiCall(t, h, "PATCH", "/tasks/"+original[4].Task.ID, map[string]any{"expected_revision": 1, "title": "Personal title", "description": "Personal description", "priority": "high"}, 200)
	session := value[SessionDTO](t, apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": original[4].Task.ID, "date": "2026-12-14"}, 201))
	rule := v.Rule.Rule
	rule.Interval = 14
	in := SeriesCommand{Action: "change_rule", Rule: &rule, FromCycle: ptr(int64(0)), FirstDate: "2026-11-18"}
	command, preview := managedPreview(t, s, v.ID, in)
	before := recurrenceDBSnapshot(t, db)
	second, e := s.PreviewSeriesChange(ctx, v.ID, command)
	if e != nil || second.PreviewToken != preview.PreviewToken || before != recurrenceDBSnapshot(t, db) {
		t.Fatal("preview writes or nondeterminism", e)
	}
	dispositions := map[int64]string{}
	for _, r := range preview.Rows {
		dispositions[r.CycleIndex] = r.Disposition
	}
	if dispositions[0] != "protected_closed" || dispositions[2] != "manual_exception" || dispositions[3] != "protected_started" {
		t.Fatal(dispositions)
	}
	if _, e = s.ApplySeriesCommand(ctx, v.ID, command); e != nil {
		t.Fatal(e)
	}
	current := getSeriesTest(t, s, v.ID)
	if len(current.Versions) != 2 || current.Versions[0].UntilCycle == nil || *current.Versions[0].UntilCycle != 0 {
		t.Fatal(current.Versions)
	}
	for i, o := range original {
		if current.Occurrences[i].ID != o.ID || current.Occurrences[i].Task.ID != o.Task.ID {
			t.Fatal("identity replaced")
		}
	}
	if current.Occurrences[1].BaseDate != "2026-12-02" || current.Occurrences[1].OriginalBaseDate != original[1].BaseDate || *current.Occurrences[2].Task.PlannedDate != "2026-12-01" || current.Occurrences[3].Task.Status != "in_progress" {
		t.Fatal(current)
	}
	if current.Occurrences[4].Task.Title != "Personal title" || current.Occurrences[4].Task.Description != "Personal description" || current.Occurrences[4].Task.Priority != "high" {
		t.Fatal("fields overwritten")
	}
	if !reflect.DeepEqual(closedHistory, history(t, h, "task", original[0].Task.ID)) {
		t.Fatal("closed history changed")
	}
	actualSession := value[SessionDTO](t, apiCall(t, h, "GET", "/sessions/"+session.ID, nil, 200))
	if !reflect.DeepEqual(session, actualSession) {
		t.Fatal("session changed")
	}
	in.FirstDate = "2026-11-19"
	managedApply(t, s, v.ID, in)
	current = getSeriesTest(t, s, v.ID)
	if len(current.Versions) != 3 || *current.Versions[1].UntilCycle != 0 {
		t.Fatal(current.Versions)
	}
	drainSeries(t, s, MaterializeInput{Through: "2027-05-01"})
	var n int
	if e = db.db.QueryRow(`SELECT count(*) FROM (SELECT series_id,cycle_index,count(*) n FROM occurrences GROUP BY series_id,cycle_index HAVING n>1)`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}
func TestManagementPauseSkipReviewRestoreAndEarlyResume(t *testing.T) {
	_, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("pause-history", "2026-11-16", recurrence.Schedule, 7))
	managedApply(t, s, v.ID, SeriesCommand{Action: "pause", StartDate: "2026-11-23", EndDate: "2026-12-14", Reason: "Vacation"})
	items := allIncidents(t, s, v.ID, "needs_decision")
	if len(items) != 4 {
		t.Fatal(items)
	}
	for _, i := range items {
		if i.Source != "pause" || i.Kind != "skipped" || i.TaskStatus != "skipped" || i.Reason != "Vacation" {
			t.Fatal(i)
		}
	}
	projection, e := s.CalendarProjection(ctx, "2026-11-23", "2026-12-14")
	if e != nil || len(projection.Items) != 0 {
		t.Fatal(projection, e)
	}
	chosen := items[0].OccurrenceID
	occurrenceApply(t, s, chosen, "review", occurrenceInput(t, s, chosen))
	if len(allIncidents(t, s, v.ID, "needs_decision")) != 3 || len(allIncidents(t, s, v.ID, "")) != 4 {
		t.Fatal("review erased fact")
	}
	current := getSeriesTest(t, s, v.ID)
	managedApply(t, s, v.ID, SeriesCommand{Action: "resume", PauseID: current.Pauses[0].ID})
	items = allIncidents(t, s, v.ID, "")
	if len(items) != 4 || len(allIncidents(t, s, v.ID, "needs_decision")) != 0 {
		t.Fatal(items)
	}
	for _, i := range items {
		if i.OccurrenceID == chosen {
			if i.TaskStatus != "skipped" || i.Resolution != "acknowledged" {
				t.Fatal(i)
			}
		} else if i.TaskStatus != "planned" || i.Resolution != "restored" {
			t.Fatal(i)
		}
	}
	in := occurrenceInput(t, s, chosen)
	in.PlannedDate = ptr("2026-11-19")
	restored := occurrenceApply(t, s, chosen, "reschedule", in)
	if restored.Task.Status != "planned" || *restored.Task.PlannedDate != "2026-11-19" {
		t.Fatal(restored)
	}
	drainSeries(t, s, MaterializeInput{})
	if len(allIncidents(t, s, v.ID, "")) != 4 {
		t.Fatal("pause duplicated")
	}
}
func TestManagementPausePreservesManualStartedAndSessionTasks(t *testing.T) {
	_, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("pause-protection", "2026-11-16", recurrence.Schedule, 7))
	manual := v.Occurrences[1].Task
	apiCall(t, h, "POST", "/tasks/"+manual.ID+"/reschedule", map[string]any{"expected_revision": 1, "planned_date": "2026-12-25"}, 200)
	started := v.Occurrences[2].Task
	apiCall(t, h, "PATCH", "/tasks/"+started.ID, map[string]any{"expected_revision": 1, "status": "in_progress"}, 200)
	withSession := v.Occurrences[3].Task
	apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": withSession.ID, "date": "2026-12-07"}, 201)
	in, p := managedPreview(t, s, v.ID, SeriesCommand{Action: "pause", StartDate: "2026-11-23", EndDate: "2026-12-14"})
	if len(p.Conflicts) != 2 {
		t.Fatal(p.Conflicts)
	}
	if _, e := s.ApplySeriesCommand(ctx, v.ID, in); e != nil {
		t.Fatal(e)
	}
	current := getSeriesTest(t, s, v.ID)
	if *current.Occurrences[1].Task.PlannedDate != "2026-12-25" || current.Occurrences[2].Task.Status != "in_progress" || current.Occurrences[3].Task.Status != "planned" || current.Occurrences[4].Task.Status != "skipped" {
		t.Fatal(current)
	}
}
func TestManagementCompletionSkipReanchorBranchAndHistoricalCompletion(t *testing.T) {
	_, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("completion-management", "2026-11-16", recurrence.Completion, 7))
	initial := v.Occurrences[0]
	in, p := managedPreview(t, s, v.ID, SeriesCommand{Action: "pause", StartDate: "2026-11-16", EndDate: "2026-12-14"})
	if len(p.Rows) != 3 || p.Rows[0].Disposition != "completion_pending_preserved" {
		t.Fatal(p)
	}
	if _, e := s.ApplySeriesCommand(ctx, v.ID, in); e != nil {
		t.Fatal(e)
	}
	completeSeriesTask(t, h, initial.Task, "2026-11-16")
	current := getSeriesTest(t, s, v.ID)
	if current.State != "needs_anchor" || len(current.Occurrences) != 3 || current.Occurrences[1].Task.Status != "skipped" {
		t.Fatal(current)
	}
	old := current.Occurrences[1]
	drainSeries(t, s, MaterializeInput{})
	if len(getSeriesTest(t, s, v.ID).Occurrences) != 3 {
		t.Fatal("pause made phantom chain")
	}
	managedApply(t, s, v.ID, SeriesCommand{Action: "reanchor", AnchorDate: "2026-12-14", Reason: "Restart"})
	current = getSeriesTest(t, s, v.ID)
	if len(current.Occurrences) != 3 || current.Occurrences[2].BaseDate != "2026-12-21" {
		t.Fatal(current)
	}
	request := occurrenceInput(t, s, old.ID)
	if _, e := s.OccurrenceCommand(ctx, old.ID, "restore", request); e == nil {
		t.Fatal("second active branch allowed")
	}
	s.now = func() time.Time { return time.Date(2026, 12, 22, 12, 0, 0, 0, time.UTC) }
	request = occurrenceInput(t, s, old.ID)
	request.CompletedDate = ptr("2026-11-24")
	request.BranchDecision = "historical_only"
	completed := occurrenceApply(t, s, old.ID, "complete", request)
	if *completed.Task.CompletedDate != "2026-11-24" {
		t.Fatal(completed)
	}
	current = getSeriesTest(t, s, v.ID)
	if len(current.Occurrences) != 5 || *current.WaitingOccurrenceID != current.Occurrences[2].ID {
		t.Fatal("historical completion advanced chain")
	}
	last := current.Occurrences[2]
	occurrenceApply(t, s, last.ID, "cancel", occurrenceInput(t, s, last.ID))
	restored := occurrenceApply(t, s, last.ID, "restore", occurrenceInput(t, s, last.ID))
	if restored.ID != last.ID || restored.Task.Status != "planned" {
		t.Fatal(restored)
	}
	completeSeriesTask(t, h, restored.Task, "2026-12-22")
	if len(getSeriesTest(t, s, v.ID).Occurrences) != 6 {
		t.Fatal("restored cycle did not advance")
	}
	incidents := allIncidents(t, s, v.ID, "")
	if len(incidents) != 2 {
		t.Fatal(incidents)
	}
}
func TestManagementCompletionRuleUsesActualAnchorAndPreservesPendingInPause(t *testing.T) {
	_, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("completion-rule-pause", "2026-11-16", recurrence.Completion, 7))
	completeSeriesTask(t, h, v.Occurrences[0].Task, "2026-11-15")
	v = getSeriesTest(t, s, v.ID)
	pending := v.Occurrences[1]
	if pending.BaseDate != "2026-11-22" {
		t.Fatal(pending)
	}
	managedApply(t, s, v.ID, SeriesCommand{Action: "pause", StartDate: "2026-11-24", EndDate: "2026-12-20"})
	rule := v.Rule.Rule
	rule.Interval = 14
	in, preview := managedPreview(t, s, v.ID, SeriesCommand{Action: "change_rule", Rule: &rule, FromCycle: ptr(int64(1))})
	if len(preview.Rows) != 3 || preview.Rows[0].Disposition != "change_date_pause_pending_preserved" || *preview.Rows[0].AfterDate != "2026-11-29" {
		t.Fatal(preview)
	}
	if _, e := s.ApplySeriesCommand(ctx, v.ID, in); e != nil {
		t.Fatal(e)
	}
	current := getSeriesTest(t, s, v.ID)
	if len(current.Occurrences) != 4 || current.Occurrences[1].ID != pending.ID || current.Occurrences[1].Task.Status != "planned" || *current.Occurrences[1].Task.PlannedDate != "2026-11-29" || *current.WaitingOccurrenceID != pending.ID || *current.AnchorDate != "2026-11-15" {
		t.Fatal(current)
	}
	if len(allIncidents(t, s, v.ID, "")) != 0 {
		t.Fatal("rule edit silently skipped the active completion cycle")
	}
}

func TestManagementUserSkipSessionsAndIncidentActions(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("skip-actions", "2026-11-16", recurrence.Schedule, 7))
	o := v.Occurrences[0]
	session := value[SessionDTO](t, apiCall(t, h, "POST", "/sessions", map[string]any{"task_id": o.Task.ID, "date": "2026-11-17"}, 201))
	in := occurrenceInput(t, s, o.ID)
	in.Reason = "Not done"
	before := recurrenceDBSnapshot(t, db)
	if _, e := s.OccurrenceCommand(ctx, o.ID, "skip", in); e == nil {
		t.Fatal("unconfirmed sessions closed")
	}
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("partial skip")
	}
	in.SessionsDecision = "cancel"
	in.SessionRevisions = map[string]int64{session.ID: session.Revision}
	skipped := occurrenceApply(t, s, o.ID, "skip", in)
	if skipped.Task.Status != "skipped" {
		t.Fatal(skipped)
	}
	cancelled := occurrenceApply(t, s, o.ID, "cancel", occurrenceInput(t, s, o.ID))
	if cancelled.Task.Status != "cancelled" {
		t.Fatal(cancelled)
	}
	facts := allIncidents(t, s, v.ID, "")
	if len(facts) != 2 {
		t.Fatal(facts)
	}
	kinds := map[string]bool{}
	for _, f := range facts {
		kinds[f.Kind] = true
	}
	if !kinds["skipped"] || !kinds["cancelled"] {
		t.Fatal(facts)
	}
	occurrenceApply(t, s, o.ID, "restore", occurrenceInput(t, s, o.ID))
	occurrenceApply(t, s, o.ID, "skip", occurrenceInput(t, s, o.ID))
	in = occurrenceInput(t, s, o.ID)
	in.CompletedDate = ptr("2026-11-15")
	done := occurrenceApply(t, s, o.ID, "complete", in)
	if done.Task.Status != "completed" || *done.Task.CompletedDate != "2026-11-15" || len(allIncidents(t, s, v.ID, "")) != 3 || len(allIncidents(t, s, v.ID, "needs_decision")) != 0 {
		t.Fatal(done)
	}
}
func TestManagementStalePreviewConcurrencyRollbackAndMissingCalendar(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("stale-preview", "2026-11-16", recurrence.Schedule, 7))
	r := v.Rule.Rule
	r.Interval = 14
	c, p := managedPreview(t, s, v.ID, SeriesCommand{Action: "change_rule", Rule: &r, FromCycle: ptr(int64(1)), FirstDate: "2026-11-24"})
	apiCall(t, h, "PATCH", "/tasks/"+v.Occurrences[1].Task.ID, map[string]any{"expected_revision": 1, "title": "Concurrent edit"}, 200)
	before := recurrenceDBSnapshot(t, db)
	if _, e := s.ApplySeriesCommand(ctx, v.ID, c); e == nil {
		t.Fatal("stale token accepted")
	}
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("stale preview wrote")
	}
	c, p = managedPreview(t, s, v.ID, c)
	if len(p.CalendarErrors) > 0 {
		t.Fatal(p)
	}
	exec(t, db, `CREATE TRIGGER management_fail BEFORE INSERT ON history_events WHEN NEW.action='rule_changed' BEGIN SELECT RAISE(ABORT,'injected'); END`)
	if _, e := s.ApplySeriesCommand(ctx, v.ID, c); e == nil {
		t.Fatal("history failure ignored")
	}
	exec(t, db, "DROP TRIGGER management_fail")
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("partial rule apply")
	}
	var wg sync.WaitGroup
	ids := make(chan string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, e := s.ApplySeriesCommand(ctx, v.ID, c)
			if e != nil {
				t.Error(e)
				return
			}
			ids <- result.Series.RuleVersionID
		}()
	}
	wg.Wait()
	close(ids)
	same := ""
	for id := range ids {
		if same != "" && same != id {
			t.Fatal("duplicate retry")
		}
		same = id
	}
	current := getSeriesTest(t, s, v.ID)
	if len(current.Versions) != 2 {
		t.Fatal(current.Versions)
	}
	r.AllowedDays.Mode = recurrence.RussianWorkdays
	r.Adjustment = recurrence.Next
	c, p = managedPreview(t, s, v.ID, SeriesCommand{Action: "change_rule", Rule: &r, FromCycle: ptr(int64(1)), FirstDate: "2026-11-24"})
	if len(p.CalendarErrors) == 0 || p.CalendarErrors[0].Code != "production_calendar_unavailable" {
		t.Fatal(p)
	}
	before = recurrenceDBSnapshot(t, db)
	if _, e := s.ApplySeriesCommand(ctx, v.ID, c); e == nil {
		t.Fatal("missing calendar accepted")
	}
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("missing calendar wrote")
	}
	for _, year := range []int{2026, 2027} {
		if e := db.ImportCalendarYear(ctx, syntheticRecurrenceYear(year)); e != nil {
			t.Fatal(e)
		}
	}
	c, p = managedPreview(t, s, v.ID, c)
	if len(p.CalendarErrors) != 0 {
		t.Fatal(p.CalendarErrors)
	}
	// Even a calendar source/revision change invalidates the consent token.
	if e := db.ImportCalendarYear(ctx, syntheticRecurrenceYear(2026)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ApplySeriesCommand(ctx, v.ID, c); e == nil {
		t.Fatal("calendar revision ignored")
	}
}
func TestManagementPauseAcrossRestartAndArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "planner.db")
	db, e := Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	s := NewService(db)
	s.now = func() time.Time { return time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC) }
	h := NewHandler(s)
	v := createSeries(t, h, seriesInput("restart-pause", "2026-11-16", recurrence.Schedule, 7))
	managedApply(t, s, v.ID, SeriesCommand{Action: "pause", StartDate: "2027-02-01", EndDate: "2027-03-01"})
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	db, e = Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s = NewService(db)
	s.now = func() time.Time { return time.Date(2027, 3, 2, 12, 0, 0, 0, time.UTC) }
	drainSeries(t, s, MaterializeInput{})
	facts := allIncidents(t, s, v.ID, "")
	if len(facts) != 5 {
		t.Fatal(facts)
	}
	before := recurrenceDBSnapshot(t, db)
	drainSeries(t, s, MaterializeInput{})
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("repeated pause pass wrote")
	}
	current := getSeriesTest(t, s, v.ID)
	if current.State != "active" {
		t.Fatal(current.State)
	}
	managedApply(t, s, v.ID, SeriesCommand{Action: "archive"})
	n := count(t, db, "occurrences")
	drainSeries(t, s, MaterializeInput{Through: "2027-10-01"})
	if count(t, db, "occurrences") != n {
		t.Fatal("archive generated cycles")
	}
}
func TestManagementScopesConstraintsSnapshotAndHTTP(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("scopes", "2026-11-16", recurrence.Schedule, 7))
	r := v.Rule.Rule
	r.Interval = 14
	managedApply(t, s, v.ID, SeriesCommand{Action: "change_rule", Rule: &r, FromCycle: ptr(int64(1)), FirstDate: "2026-11-25"})
	current := getSeriesTest(t, s, v.ID)
	if _, e := db.db.Exec("UPDATE series_version_scopes SET until_cycle=NULL WHERE version_id=?", v.RuleVersionID); e == nil {
		t.Fatal("overlapping scopes allowed")
	}
	if _, e := db.db.Exec("UPDATE series_versions SET rule_json='{}' WHERE id=?", current.RuleVersionID); e == nil {
		t.Fatal("mutable rule")
	}
	managedApply(t, s, v.ID, SeriesCommand{Action: "pause", StartDate: "2026-11-25", EndDate: "2026-12-10"})
	before := recurrenceDBSnapshot(t, db)
	snapshot := filepath.Join(t.TempDir(), "management.sqlite")
	if e := db.Export(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	facts := allIncidents(t, s, v.ID, "needs_decision")
	occurrenceApply(t, s, facts[0].OccurrenceID, "review", occurrenceInput(t, s, facts[0].OccurrenceID))
	if e := db.Restore(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("management state not restored")
	}
	apiCall(t, h, "GET", "/incidents?kind=skipped&disposition=needs_decision&limit=1", nil, 200)
	apiCall(t, h, "GET", "/history?object_type=occurrence&object_id="+facts[0].OccurrenceID, nil, 200)
	apiCall(t, h, "POST", "/series/"+v.ID+"/preview", map[string]any{"expected_revision": getSeriesTest(t, s, v.ID).Revision, "action": "pause", "start_date": "2026-02-30", "end_date": "2026-03-01"}, 422)
	apiCall(t, h, "POST", "/occurrences/"+facts[0].OccurrenceID+"/review", map[string]any{"revision": 2}, 400)
	apiCall(t, h, "GET", "/series/"+v.ID+"/pause", nil, 405)
	apiCall(t, h, "GET", "/incidents?kind=overdue", nil, 422)
	// distinct commands racing on the same revisions cannot both apply.
	input := occurrenceInput(t, s, facts[0].OccurrenceID)
	a, b := input, input
	b.RequestID, _ = operation()
	statuses := make(chan bool, 2)
	var wg sync.WaitGroup
	for _, in := range []OccurrenceCommand{a, b} {
		wg.Add(1)
		go func(in OccurrenceCommand) {
			defer wg.Done()
			_, e := s.OccurrenceCommand(ctx, facts[0].OccurrenceID, "review", in)
			statuses <- e == nil
		}(in)
	}
	wg.Wait()
	close(statuses)
	success := 0
	for ok := range statuses {
		if ok {
			success++
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
}
func TestManagementSQLIncidentsStayImmutable(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("immutable-fact", "2026-11-16", recurrence.Schedule, 7))
	o := v.Occurrences[0]
	occurrenceApply(t, s, o.ID, "skip", occurrenceInput(t, s, o.ID))
	facts := allIncidents(t, s, v.ID, "")
	for _, column := range []string{"kind", "reason", "planned_date", "source"} {
		if _, e := db.db.Exec(fmt.Sprintf("UPDATE occurrence_incidents SET %s=%s WHERE id=?", column, column), facts[0].ID); e == nil {
			t.Fatal(column)
		}
	}
	var count int
	if e := db.db.QueryRow("SELECT count(*) FROM attention_state WHERE occurrence_id=? AND disposition='processed'", o.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	occurrenceApply(t, s, o.ID, "review", occurrenceInput(t, s, o.ID))
	if e := db.db.QueryRow("SELECT count(*) FROM attention_state WHERE occurrence_id=? AND disposition='processed'", o.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	if len(history(t, h, "occurrence", o.ID)) < 2 {
		t.Fatal("no incident history")
	}
}

// Ensure the real HTTP adapter accepts only the documented command contract.
func TestManagementHTTPPreviewApplyReplay(t *testing.T) {
	_, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("http-manage", "2026-11-16", recurrence.Schedule, 7))
	command := SeriesCommand{Action: "pause", ExpectedRevision: v.Revision, StartDate: "2026-11-23", EndDate: "2026-11-30"}
	preview := value[SeriesChangePreview](t, apiCall(t, h, http.MethodPost, "/series/"+v.ID+"/preview", command, 200))
	command.PreviewToken = preview.PreviewToken
	command.RequestID = "http-pause-command"
	first := apiCall(t, h, "POST", "/series/"+v.ID+"/pause", command, 200)
	second := apiCall(t, h, "POST", "/series/"+v.ID+"/pause", command, 200)
	if first.Body.String() != second.Body.String() {
		t.Fatal("response changed on retry")
	}
	response := apiCall(t, h, "GET", "/incidents?series_id="+v.ID+"&kind=skipped", nil, 200)
	if !strings.Contains(response.Body.String(), "pause") {
		t.Fatal(response.Body.String())
	}
	facts := allIncidents(t, s, v.ID, "")
	in := occurrenceInput(t, s, facts[0].OccurrenceID)
	apiCall(t, h, "POST", "/occurrences/"+facts[0].OccurrenceID+"/review", in, 200)
	apiCall(t, h, "POST", "/occurrences/"+facts[0].OccurrenceID+"/review", in, 200)
}

func TestManagementPauseUsesAdjustedDateAndKeepsCollidingCycles(t *testing.T) {
	_, s, h := seriesFixture(t)
	in := seriesInput("adjusted-pause", "2026-11-20", recurrence.Schedule, 1)
	in.Rule.AllowedDays.Mode = recurrence.Weekdays
	in.Rule.Adjustment = recurrence.Next
	v := createSeries(t, h, in)
	managedApply(t, s, v.ID, SeriesCommand{Action: "pause", StartDate: "2026-11-21", EndDate: "2026-11-22"})
	if len(allIncidents(t, s, v.ID, "")) != 0 {
		t.Fatal("pause used base date")
	}
	managedApply(t, s, v.ID, SeriesCommand{Action: "pause", StartDate: "2026-11-23", EndDate: "2026-11-23"})
	facts := allIncidents(t, s, v.ID, "")
	if len(facts) != 3 {
		t.Fatal(facts)
	}
	seen := map[int64]bool{}
	for _, f := range facts {
		seen[f.CycleIndex] = true
		if f.PlannedDate != "2026-11-23" {
			t.Fatal(f)
		}
	}
	if len(seen) != 3 {
		t.Fatal("colliding cycles merged")
	}
}
func TestManagementReplaceUnusedAnchorAndArchiveStaysArchived(t *testing.T) {
	_, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("unused-anchor", "2026-11-16", recurrence.Completion, 365))
	o := v.Occurrences[0]
	occurrenceApply(t, s, o.ID, "skip", occurrenceInput(t, s, o.ID))
	managedApply(t, s, v.ID, SeriesCommand{Action: "reanchor", AnchorDate: "2027-01-01"})
	in := occurrenceInput(t, s, o.ID)
	if _, e := s.OccurrenceCommand(ctx, o.ID, "restore", in); e == nil {
		t.Fatal("new anchor silently replaced")
	}
	in.BranchDecision = "replace_anchor"
	if _, e := s.OccurrenceCommand(ctx, o.ID, "restore", in); e == nil {
		t.Fatal("replaced an existing dynamic continuation")
	}
	currentBefore := getSeriesTest(t, s, v.ID)
	managedApply(t, s, v.ID, SeriesCommand{Action: "archive"})
	occurrenceApply(t, s, *currentBefore.WaitingOccurrenceID, "cancel", occurrenceInput(t, s, *currentBefore.WaitingOccurrenceID))
	current := getSeriesTest(t, s, v.ID)
	if current.State != "archived" {
		t.Fatal("cancel unarchived series")
	}
}
func TestManagementPauseFailureRollsBackEveryIncidentAndReceipt(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("pause-rollback", "2026-11-16", recurrence.Schedule, 7))
	c, _ := managedPreview(t, s, v.ID, SeriesCommand{Action: "pause", StartDate: "2026-11-23", EndDate: "2026-12-14"})
	before := recurrenceDBSnapshot(t, db)
	exec(t, db, `CREATE TRIGGER fail_second_incident BEFORE INSERT ON occurrence_incidents WHEN (SELECT count(*) FROM occurrence_incidents)>0 BEGIN SELECT RAISE(ABORT,'injected'); END`)
	if _, e := s.ApplySeriesCommand(ctx, v.ID, c); e == nil {
		t.Fatal("partial pause accepted")
	}
	exec(t, db, "DROP TRIGGER fail_second_incident")
	if before != recurrenceDBSnapshot(t, db) {
		t.Fatal("pause partially persisted")
	}
	if _, e := s.ApplySeriesCommand(ctx, v.ID, c); e != nil {
		t.Fatal("failed command cannot retry", e)
	}
}

func TestOccurrenceRescheduleTimeAtomicAndReplay(t *testing.T) {
	db, s, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("ux-time", "2026-11-16", recurrence.Schedule, 45))
	o := v.Occurrences[0]
	in := occurrenceInput(t, s, o.ID)
	in.PlannedDate = ptr("2026-11-20")
	in.ScheduledTime = Field[string]{Set: true, Value: ptr("10:30")}
	moved := occurrenceApply(t, s, o.ID, "reschedule", in)
	if moved.Task.ScheduledTime == nil || *moved.Task.ScheduledTime != "10:30" || moved.BaseDate != o.BaseDate || !moved.ManualOverride {
		t.Fatal(moved)
	}
	snapshot := recurrenceDBSnapshot(t, db)
	occurrenceApply(t, s, o.ID, "reschedule", in)
	if snapshot != recurrenceDBSnapshot(t, db) {
		t.Fatal("replay changed state")
	}
	invalid := occurrenceInput(t, s, o.ID)
	invalid.PlannedDate = ptr("2026-11-22")
	invalid.ScheduledTime = Field[string]{Set: true, Value: ptr("25:00")}
	if _, e := s.OccurrenceCommand(ctx, o.ID, "reschedule", invalid); e == nil {
		t.Fatal("invalid time accepted")
	}
	if snapshot != recurrenceDBSnapshot(t, db) {
		t.Fatal("invalid time partially changed occurrence")
	}
	clear := occurrenceInput(t, s, o.ID)
	clear.PlannedDate = ptr("2026-11-23")
	clear.ScheduledTime = Field[string]{Set: true}
	if got := occurrenceApply(t, s, o.ID, "reschedule", clear); got.Task.ScheduledTime != nil {
		t.Fatal(got)
	}
}

func TestOccurrenceTimeHTTPContract(t *testing.T) {
	_, _, h := seriesFixture(t)
	v := createSeries(t, h, seriesInput("time-api", "2026-11-16", recurrence.Schedule, 45))
	o := v.Occurrences[0]
	body := map[string]any{"request_id": "ux-time-http", "expected_revision": o.Revision, "expected_task_revision": o.Task.Revision, "expected_series_revision": v.Revision, "planned_date": "2026-11-20", "scheduled_time": "09:30"}
	apiCall(t, h, "POST", "/occurrences/"+o.ID+"/reschedule", body, 200)
	task := value[TaskDTO](t, apiCall(t, h, "GET", "/tasks/"+o.Task.ID, nil, 200))
	if task.ScheduledTime == nil || *task.ScheduledTime != "09:30" {
		t.Fatal(task)
	}
}

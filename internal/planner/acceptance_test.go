package planner

import (
	"budget/internal/planner/recurrence"
	"budget/internal/storageguard"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func importBundled(t *testing.T, s *Service, raw []byte, key string) {
	t.Helper()
	var doc CalendarDocument
	if e := json.Unmarshal(raw, &doc); e != nil {
		t.Fatal(e)
	}
	_, checksum, e := validateCalendarDocument(doc)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ImportProductionCalendar(ctx, CalendarImportInput{Calendar: doc, SourceConfirmed: true, PreviewToken: calendarImportToken(checksum, 0), RequestID: key}); e != nil {
		t.Fatal(e)
	}
}
func TestAcceptanceCalendarsEveryDayAndYearBoundary(t *testing.T) {
	st, s, h := seriesFixture(t)
	for _, year := range []int{2026, 2027} {
		raw := verified2026
		if year == 2027 {
			raw = verified2027
		}
		importBundled(t, s, raw, fmt.Sprint("calendar", year))
		var doc CalendarDocument
		json.Unmarshal(raw, &doc)
		holidays := map[string]bool{"01-01": true, "01-02": true, "01-03": true, "01-04": true, "01-05": true, "01-06": true, "01-07": true, "01-08": true, "02-23": true, "03-08": true, "05-01": true, "05-09": true, "06-12": true, "11-04": true}
		extra := map[string]bool{"01-09": true, "03-09": true, "05-11": true, "12-31": true}
		short := map[string]bool{"04-30": true, "05-08": true, "06-11": true, "11-03": true}
		if year == 2027 {
			extra = map[string]bool{"02-22": true, "05-03": true, "05-10": true, "06-14": true, "11-05": true, "12-31": true}
			short = map[string]bool{"02-20": true, "04-30": true, "06-11": true, "11-03": true}
		}
		working, shortened := 0, 0
		for i, d := range doc.Days {
			date := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i)
			if string(d.Date) != date.Format("2006-01-02") {
				t.Fatal("missing/duplicate day", d)
			}
			k := date.Format("01-02")
			want := "working"
			if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday || holidays[k] || extra[k] {
				want = "nonworking"
			}
			if short[k] {
				want = "shortened"
			}
			if d.Kind != want {
				t.Fatalf("%s: %s != %s", d.Date, d.Kind, want)
			}
			if want != "nonworking" {
				working++
			}
			if want == "shortened" {
				shortened++
			}
		}
		if len(doc.Days) != 365 || working != 247 || shortened != 4 {
			t.Fatal(year, len(doc.Days), working, shortened)
		}
	}
	in := seriesInput("boundary", "2026-11-16", recurrence.Schedule, 7)
	in.Rule.AllowedDays.Mode = recurrence.RussianWorkdays
	in.Rule.Adjustment = recurrence.Next
	v := createSeries(t, h, in)
	if len(v.Occurrences) != 9 || v.Occurrences[8].AdjustedDate != "2027-01-11" {
		t.Fatal("60-day boundary", v.Occurrences)
	}
	in = seriesInput("haircut-ru", "2026-11-01", recurrence.Completion, 45)
	in.Rule.AllowedDays.Mode = recurrence.RussianWorkdays
	in.Rule.Adjustment = recurrence.Next
	v = createSeries(t, h, in)
	first := v.Occurrences[0]
	if first.AdjustedDate != "2026-11-02" {
		t.Fatal(first)
	}
	s.now = func() time.Time { return time.Date(2026, 12, 21, 12, 0, 0, 0, time.UTC) }
	completeSeriesTask(t, h, first.Task, "2026-12-21")
	drainSeries(t, s, MaterializeInput{})
	v = getSeriesTest(t, s, v.ID)
	if len(v.Occurrences) != 4 || v.Occurrences[1].BaseDate != "2027-02-04" || v.Occurrences[1].AdjustedDate != "2027-02-04" {
		t.Fatal(v.Occurrences)
	}
	if e := checkIntegrity(ctx, st.db); e != nil {
		t.Fatal(e)
	}
}
func TestAcceptanceOverdueProjectAndDate(t *testing.T) {
	_, s, h := seriesFixture(t)
	p := value[ProjectDTO](t, apiCall(t, h, "POST", "/projects", map[string]any{"title": "Late project", "due_date": "2026-11-15"}, 201))
	task(t, h, map[string]any{"title": "Late plan, future deadline", "planned_date": "2026-11-14", "due_date": "2026-12-01"})
	r := value[struct {
		Count int
		Items []map[string]any
	}](t, apiCall(t, h, "GET", "/attention?tab=overdue", nil, 200))
	if r.Count != 2 || len(r.Items) != 2 || r.Items[0]["planned_date"] != "2026-11-14" {
		t.Fatal(r)
	}
	in := SnoozeInput{Type: "project", Until: "2026-11-18", RequestID: "snooze-project"}
	if _, e := s.SnoozeAttention(ctx, p.ID, in); e != nil {
		t.Fatal(e)
	}
	r = value[struct {
		Count int
		Items []map[string]any
	}](t, apiCall(t, h, "GET", "/attention", nil, 200))
	if r.Count != 1 {
		t.Fatal(r)
	}
	apiCall(t, h, "POST", "/projects/"+p.ID+"/complete", map[string]any{"expected_revision": p.Revision}, 200)
	if len(history(t, h, "project", p.ID)) != 3 {
		t.Fatal("history lost")
	}
}
func TestAcceptanceUpgradeEveryPreviousVersion(t *testing.T) {
	for version := 1; version < len(migrations); version++ {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "upgrade.db")
			if e := os.WriteFile(path, nil, 0600); e != nil {
				t.Fatal(e)
			}
			db, e := openSQL(path, false)
			if e != nil {
				t.Fatal(e)
			}
			if e = migrate(ctx, db, migrations[:version]); e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec("INSERT INTO projects(id,title) VALUES('preserved','Сохранить при обновлении')"); e != nil {
				t.Fatal(e)
			}
			db.Close()
			st, e := Open(ctx, path)
			if e != nil {
				t.Fatal(e)
			}
			defer st.Close()
			if count(t, st, "projects") != 1 || count(t, st, "schema_migrations") != len(migrations) {
				t.Fatal("upgrade lost data")
			}
			if e = checkIntegrity(ctx, st.db); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestAcceptanceLoad(t *testing.T) {
	st, s, h := seriesFixture(t)
	if e := st.Transaction(ctx, func(tx *sql.Tx) error {
		for p := 0; p < 100; p++ {
			id := fmt.Sprintf("load-p%d", p)
			if _, e := tx.Exec("INSERT INTO projects(id,title) VALUES(?,?)", id, id); e != nil {
				return e
			}
			for n := 0; n < 10; n++ {
				tid := fmt.Sprintf("load-t%d-%d", p, n)
				var parent any
				if n >= 5 {
					parent = fmt.Sprintf("load-t%d-%d", p, n-5)
				}
				if _, e := tx.Exec("INSERT INTO tasks(id,title,project_id,parent_task_id,planned_date) VALUES(?,?,?,?,'2026-11-16')", tid, tid, id, parent); e != nil {
					return e
				}
			}
		}
		stmt, e := tx.Prepare("INSERT INTO history_events(id,task_id,actor,action,operation_id) VALUES(?,'load-t0-0','owner','edited',?)")
		if e != nil {
			return e
		}
		defer stmt.Close()
		for i := 0; i < 10000; i++ {
			id := fmt.Sprint("history-", i)
			if _, e = stmt.Exec(id, id); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	series := createSeries(t, h, seriesInput("load-series", "2026-01-01", recurrence.Schedule, 1))
	drainSeries(t, s, MaterializeInput{SeriesID: series.ID})
	results := map[string]float64{}
	// Same production SQL projection, measured separately from HTTP/JSON/DOM.
	// Includes driver decoding and row scanning; it is not a pure SQLite VM timer.
	if e := st.Transaction(ctx, func(tx *sql.Tx) error {
		start := time.Now()
		rows, err := listTasks(tx, "WHERE status IN ('planned','in_progress')")
		results["sql_tasks_query_and_scan"] = float64(time.Since(start).Microseconds()) / 1000
		if err != nil {
			return err
		}
		start = time.Now()
		for i := range rows {
			if err := decorateTask(tx, &rows[i]); err != nil {
				return err
			}
		}
		results["sql_task_children_decoration"] = float64(time.Since(start).Microseconds()) / 1000
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	for _, route := range []string{"/projects", "/tasks", "/calendar?from=2026-11-01&to=2026-11-30", "/history?object_type=task&object_id=load-t0-0&limit=50", "/attention?limit=50", "/series"} {
		start := time.Now()
		apiCall(t, h, "GET", route, nil, 200)
		results[route] = float64(time.Since(start).Microseconds()) / 1000
	}
	t.Logf("100 projects, %d tasks, %d occurrences, %d history: latency ms %#v", count(t, st, "tasks"), count(t, st, "occurrences"), count(t, st, "history_events"), results)
	if file := os.Getenv("PLANNER_PERF_REPORT"); file != "" {
		if e := storageguard.TestPath(filepath.Dir(file)); e != nil {
			t.Fatal(e)
		}
		if _, e := os.Lstat(file); e == nil {
			if e = storageguard.Regular(file, false); e != nil {
				t.Fatal(e)
			}
		}
		raw, _ := json.MarshalIndent(results, "", "  ")
		if e := os.WriteFile(file, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := checkIntegrity(ctx, st.db); e != nil {
		t.Fatal(e)
	}
	backup := filepath.Join(t.TempDir(), "large.db")
	if e := st.Export(ctx, backup); e != nil {
		t.Fatal(e)
	}
	if e := st.Restore(ctx, backup); e != nil {
		t.Fatal(e)
	}
	if count(t, st, "tasks") < 1300 {
		t.Fatal("data missing")
	}
}

func TestAcceptanceCreateReceiptReplayConflictAndRollback(t *testing.T) {
	st := testStore(t)
	h := NewHandler(NewService(st))
	p := value[ProjectDTO](t, apiCall(t, h, "POST", "/projects", map[string]any{"title": "Once", "request_id": "create-project"}, 201))
	again := value[ProjectDTO](t, apiCall(t, h, "POST", "/projects", map[string]any{"title": "Once", "request_id": "create-project"}, 201))
	if p.ID != again.ID || count(t, st, "projects") != 1 {
		t.Fatal("duplicate project")
	}
	input := map[string]any{"title": "Once", "request_id": "create-task", "project_id": p.ID}
	first := task(t, h, input)
	if task(t, h, input).ID != first.ID || count(t, st, "tasks") != 1 || len(history(t, h, "task", first.ID)) != 1 {
		t.Fatal("duplicate task/history")
	}
	input["title"] = "Other"
	apiCall(t, h, "POST", "/tasks", input, 409)
	session := map[string]any{"request_id": "create-session", "task_id": first.ID, "date": "2026-11-19"}
	a := value[SessionDTO](t, apiCall(t, h, "POST", "/sessions", session, 201))
	b := value[SessionDTO](t, apiCall(t, h, "POST", "/sessions", session, 201))
	if a.ID != b.ID || count(t, st, "sessions") != 1 {
		t.Fatal("duplicate session")
	}
	before := count(t, st, "planner_commands")
	exec(t, st, `CREATE TRIGGER reject_create_history BEFORE INSERT ON history_events BEGIN SELECT RAISE(ABORT,'injected history failure'); END`)
	apiCall(t, h, "POST", "/tasks", map[string]any{"title": "Rollback", "request_id": "rollback"}, 422)
	if count(t, st, "planner_commands") != before || count(t, st, "tasks") != 1 {
		t.Fatal("partial receipt or task")
	}
	exec(t, st, "DROP TRIGGER reject_create_history")
	apiCall(t, h, "POST", "/tasks", map[string]any{"title": "Rollback", "request_id": "rollback"}, 201)
}

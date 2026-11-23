package planner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var ctx = context.Background()

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(ctx, filepath.Join(t.TempDir(), "planner.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func exec(t *testing.T, s *Store, q string, args ...any) {
	t.Helper()
	if e := s.Transaction(ctx, func(tx *sql.Tx) error { _, e := tx.Exec(q, args...); return e }); e != nil {
		t.Fatal(e)
	}
}
func count(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if e := s.Transaction(ctx, func(tx *sql.Tx) error { return tx.QueryRow("SELECT count(*) FROM " + table).Scan(&n) }); e != nil {
		t.Fatal(e)
	}
	return n
}
func TestCreateReopenAndLock(t *testing.T) {
	s := testStore(t)
	path := s.path
	if _, e := Open(ctx, path); e == nil {
		t.Fatal("second application opened database")
	}
	alias := filepath.Join(filepath.Dir(path), "alias.db")
	if e := os.Link(path, alias); e != nil {
		t.Fatal(e)
	}
	if _, e := Open(ctx, alias); e == nil {
		t.Fatal("hardlink bypassed lock")
	}
	if e := os.Remove(alias); e != nil {
		t.Fatal(e)
	}
	if count(t, s, "schema_migrations") != len(migrations) {
		t.Fatal("migrations")
	}
	exec(t, s, "INSERT INTO projects(id,title) VALUES('p','Project')")
	var fk, timeout, synchronous int
	var journal string
	s.db.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	s.db.QueryRow("PRAGMA busy_timeout").Scan(&timeout)
	s.db.QueryRow("PRAGMA synchronous").Scan(&synchronous)
	s.db.QueryRow("PRAGMA journal_mode").Scan(&journal)
	if fk != 1 || timeout != 5000 || synchronous != 2 || journal != "wal" {
		t.Fatalf("pragmas: %d %d %d %s", fk, timeout, synchronous, journal)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e := Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if count(t, s, "projects") != 1 || count(t, s, "schema_migrations") != len(migrations) {
		t.Fatal("reopen changed data")
	}
}
func TestMigrationRollbackAndUnknownVersion(t *testing.T) {
	s := testStore(t)
	bad := append(append([]migration{}, migrations...), migration{len(migrations) + 1, "CREATE TABLE partial(id TEXT); INVALID SQL;"})
	if e := migrate(ctx, s.db, bad); e == nil {
		t.Fatal("bad migration accepted")
	}
	var n int
	s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='partial'").Scan(&n)
	if n != 0 || count(t, s, "schema_migrations") != len(migrations) {
		t.Fatal("partial migration committed")
	}
	exec(t, s, "PRAGMA user_version=99")
	path := s.path
	s.Close()
	if reopened, e := Open(ctx, path); e == nil {
		reopened.Close()
		t.Fatal("future schema accepted")
	}
}
func TestConstraintsAndTransactions(t *testing.T) {
	s := testStore(t)
	exec(t, s, "INSERT INTO projects(id,title) VALUES('p','P'),('q','Q')")
	exec(t, s, "INSERT INTO tasks(id,title,project_id) VALUES('parent','Parent','p'),('free','Inbox',NULL)")
	exec(t, s, "INSERT INTO tasks(id,title,project_id,parent_task_id) VALUES('child','Child','p','parent')")
	for _, q := range []string{
		"INSERT INTO tasks(id,title,project_id) VALUES('bad','Bad','missing')",
		"INSERT INTO tasks(id,title,project_id,parent_task_id) VALUES('bad','Bad','p','child')",
		"INSERT INTO tasks(id,title,project_id,parent_task_id) VALUES('bad','Bad','q','parent')",
		"UPDATE tasks SET parent_task_id='child' WHERE id='parent'",
		"UPDATE tasks SET project_id='q' WHERE id='child'",
		"DELETE FROM projects WHERE id='p'",
		"INSERT INTO sessions(id,task_id,date,start_time,end_time) VALUES('s','parent','2026-01-01','12:00','11:00')",
	} {
		if e := s.Transaction(ctx, func(tx *sql.Tx) error { _, e := tx.Exec(q); return e }); e == nil {
			t.Fatalf("constraint accepted: %s", q)
		}
	}
	exec(t, s, "UPDATE tasks SET project_id='q' WHERE id='parent'")
	exec(t, s, "INSERT INTO sessions(id,task_id,date) VALUES('s1','child','2026-01-01'),('s2','child','2026-01-02')")
	sentinel := errors.New("rollback")
	if e := s.Transaction(ctx, func(tx *sql.Tx) error {
		tx.Exec("INSERT INTO projects(id,title) VALUES('rollback','R')")
		return sentinel
	}); !errors.Is(e, sentinel) {
		t.Fatal(e)
	}
	if count(t, s, "projects") != 2 || count(t, s, "sessions") != 2 {
		t.Fatal("atomicity")
	}
}
func TestParallelAccess(t *testing.T) {
	s := testStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := s.Transaction(ctx, func(tx *sql.Tx) error {
				_, e := tx.Exec("INSERT INTO projects(id,title) VALUES(?,?)", fmt.Sprint(i), "Parallel")
				return e
			})
			if e != nil {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	if count(t, s, "projects") != 30 {
		t.Fatal("lost writes")
	}
}
func TestExportRestoreAndCorruption(t *testing.T) {
	s := testStore(t)
	exec(t, s, "INSERT INTO projects(id,title) VALUES('p','Original')")
	exec(t, s, "INSERT INTO tasks(id,title,project_id) VALUES('parent','Root','p')")
	exec(t, s, "INSERT INTO tasks(id,title,project_id,parent_task_id) VALUES('child','Child','p','parent')")
	snapshot := filepath.Join(t.TempDir(), "backup.sqlite")
	if e := s.Export(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	if e := s.Export(ctx, snapshot); e == nil {
		t.Fatal("export overwrote existing file")
	}
	exec(t, s, "INSERT INTO projects(id,title) VALUES('extra','Extra')")
	if e := s.Restore(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	if count(t, s, "projects") != 1 || count(t, s, "tasks") != 2 {
		t.Fatal("restore failed")
	}
	bad := filepath.Join(t.TempDir(), "bad.sqlite")
	os.WriteFile(bad, []byte("corrupt"), 0600)
	if e := s.Restore(ctx, bad); e == nil {
		t.Fatal("corrupt snapshot accepted")
	}
	if count(t, s, "projects") != 1 {
		t.Fatal("restore error changed data")
	}
	if e := s.Restore(ctx, s.path); e == nil {
		t.Fatal("live source accepted")
	}
}

func TestFreshMigrationRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	if e := os.WriteFile(path, nil, 0600); e != nil {
		t.Fatal(e)
	}
	db, e := openSQL(path, false)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	bad := append(append([]migration{}, migrations...), migration{len(migrations) + 1, "CREATE TABLE partial(id); THIS IS INVALID;"})
	if e = migrate(ctx, db, bad); e == nil {
		t.Fatal("invalid migration succeeded")
	}
	var n int
	if e = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&n); e != nil {
		t.Fatal(e)
	}
	if n != 0 {
		t.Fatal("fresh migration left schema", n)
	}
	if e = migrate(ctx, db, migrations); e != nil {
		t.Fatal(e)
	}
}
func TestUnknownMigrationChecksum(t *testing.T) {
	s := testStore(t)
	exec(t, s, "UPDATE schema_migrations SET checksum='unknown' WHERE version=1")
	path := s.path
	s.Close()
	if s, e := Open(ctx, path); e == nil {
		s.Close()
		t.Fatal("unknown migration accepted")
	}
}
func TestRestoreRollsBackConstraintFailure(t *testing.T) {
	s := testStore(t)
	exec(t, s, "INSERT INTO projects(id,title) VALUES('p','Project')")
	exec(t, s, "INSERT INTO tasks(id,title,project_id) VALUES('parent','Parent','p')")
	exec(t, s, "INSERT INTO tasks(id,title,project_id,parent_task_id) VALUES('child','Child','p','parent')")
	snapshot := filepath.Join(t.TempDir(), "backup.sqlite")
	if e := s.Export(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	db, e := openSQL(snapshot, false)
	if e != nil {
		t.Fatal(e)
	}
	var trigger string
	if e = db.QueryRow("SELECT sql FROM sqlite_master WHERE name='tasks_parent_insert'").Scan(&trigger); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("DROP TRIGGER tasks_parent_insert"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO tasks(id,title,project_id,parent_task_id) VALUES('grandchild','Invalid','p','child')"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(trigger); e != nil {
		t.Fatal(e)
	}
	db.Close()
	exec(t, s, "INSERT INTO projects(id,title) VALUES('keep','Keep live data')")
	if e = s.Restore(ctx, snapshot); e == nil {
		t.Fatal("invalid nested snapshot restored")
	}
	if count(t, s, "projects") != 2 || count(t, s, "tasks") != 2 {
		t.Fatal("failed restoration partially committed")
	}
}
func TestExportDuringParallelWrites(t *testing.T) {
	s := testStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := s.Transaction(ctx, func(tx *sql.Tx) error {
				if _, e := tx.Exec("INSERT INTO projects(id,title) VALUES(?,?)", fmt.Sprint(i), "P"); e != nil {
					return e
				}
				_, e := tx.Exec("INSERT INTO tasks(id,title,project_id) VALUES(?,?,?)", fmt.Sprint(i), "T", fmt.Sprint(i))
				return e
			})
			if e != nil {
				t.Error(e)
			}
		}(i)
	}
	snapshot := filepath.Join(t.TempDir(), "parallel.sqlite")
	if e := s.Export(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	wg.Wait()
	db, e := openSQL(snapshot, true)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = validateSnapshot(ctx, db); e != nil {
		t.Fatal(e)
	}
	var projects, tasks int
	db.QueryRow("SELECT count(*) FROM projects").Scan(&projects)
	db.QueryRow("SELECT count(*) FROM tasks").Scan(&tasks)
	if projects != tasks {
		t.Fatal("inconsistent snapshot")
	}
}
func TestPlannerNeverChangesFinancialFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"data.json", "categories.json"} {
		if e := os.WriteFile(filepath.Join(dir, name), []byte("financial sentinel: "+name), 0600); e != nil {
			t.Fatal(e)
		}
	}
	s, e := Open(ctx, filepath.Join(dir, "planner.db"))
	if e != nil {
		t.Fatal(e)
	}
	snapshot := filepath.Join(dir, "snapshot.sqlite")
	if e = s.Export(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	if e = s.Restore(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	if e = s.Export(ctx, filepath.Join(dir, "data.json")); e == nil {
		t.Fatal("financial export path accepted")
	}
	s.Close()
	for _, name := range []string{"data.json", "categories.json"} {
		b, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil || string(b) != "financial sentinel: "+name {
			t.Fatal("financial data modified")
		}
	}
}

func TestVersionedOccurrencesAndIndependentProjects(t *testing.T) {
	s := testStore(t)
	exec(t, s, "INSERT INTO project_templates(id,title) VALUES('template','Template')")
	exec(t, s, `INSERT INTO template_versions(id,template_id,version,snapshot_json) VALUES('template-v1','template',1,'{"tasks":[]}')`)
	exec(t, s, "INSERT INTO series(id,source_type,anchor_mode) VALUES('series','project','schedule')")
	exec(t, s, `INSERT INTO series_versions(id,series_id,version,effective_from,rule_json,template_version_id,source_snapshot_json) VALUES('rule','series',1,'2026-01-01','{}','template-v1','{}')`)
	exec(t, s, `INSERT INTO occurrences(id,series_id,rule_version_id,cycle_index,base_date,adjusted_date,project_state,project_link) VALUES('one','series','rule',0,'2026-01-01','2026-01-01','materialized','one'),('two','series','rule',1,'2026-01-08','2026-01-08','materialized','two');
 INSERT INTO projects(id,title,occurrence_id,template_version_id) VALUES('p1','First','one','template-v1'),('p2','Second','two','template-v1')`)
	exec(t, s, "UPDATE projects SET title='Changed',revision=revision+1 WHERE id='p1'")
	var title string
	if e := s.Transaction(ctx, func(tx *sql.Tx) error { return tx.QueryRow("SELECT title FROM projects WHERE id='p2'").Scan(&title) }); e != nil || title != "Second" {
		t.Fatal("instances coupled", e)
	}
	for _, q := range []string{
		"UPDATE template_versions SET snapshot_json='{}' WHERE id='template-v1'",
		"UPDATE series_versions SET rule_json='{}' WHERE id='rule'",
		"INSERT INTO series(id,source_type,anchor_mode) VALUES('bad','project','completion')",
		"INSERT INTO tasks(id,title,occurrence_id) VALUES('bad','Wrong source','one')",
		"INSERT INTO occurrences(id,series_id,rule_version_id,cycle_index,base_date,adjusted_date) VALUES('duplicate','series','rule',0,'2026-01-01','2026-01-01')",
		"INSERT INTO tasks(id,title,planned_date) VALUES('bad','Invalid date','2026-02-30')",
	} {
		if e := s.Transaction(ctx, func(tx *sql.Tx) error { _, e := tx.Exec(q); return e }); e == nil {
			t.Fatalf("constraint accepted %s", q)
		}
	}
}

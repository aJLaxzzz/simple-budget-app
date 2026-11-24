package planner

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
)

// Parent-first order also satisfies the task hierarchy insert triggers.
var dataTables = []string{"planner_icons", "icon_events", "planner_settings", "project_templates", "template_versions", "retired_series_requests", "series", "removed_cycles", "series_appearances", "series_versions", "series_version_scopes", "detached_occurrences", "occurrences", "occurrence_schedule", "projects", "series_assignments", "project_repeat_state", "tasks", "entity_template_links", "project_instances", "project_closed_tasks", "series_runtime", "sessions", "series_pauses", "occurrence_incidents", "planner_commands", "history_events", "attention_state", "production_calendar_years", "production_calendar_days", "calendar_imports"}

func schema(ctx context.Context, q queryer) ([]string, error) {
	rows, e := q.QueryContext(ctx, "SELECT type||':'||name||':'||sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var text string
		if e = rows.Scan(&text); e != nil {
			return nil, e
		}
		result = append(result, text)
	}
	return result, rows.Err()
}
func validateSnapshot(ctx context.Context, db *sql.DB) error {
	var id, v int
	if e := db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&id); e != nil {
		return e
	}
	if e := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); e != nil {
		return e
	}
	if id != applicationID || v != len(migrations) {
		return fmt.Errorf("snapshot has unsupported planner schema %d", v)
	}
	rows, e := db.QueryContext(ctx, "SELECT version,checksum FROM schema_migrations ORDER BY version")
	if e != nil {
		return e
	}
	n := 0
	for rows.Next() {
		var v int
		var hash string
		if e = rows.Scan(&v, &hash); e != nil {
			rows.Close()
			return e
		}
		n++
		if v != n || v > len(migrations) || hash != fmt.Sprintf("%x", sha256.Sum256([]byte(migrations[v-1].sql))) {
			rows.Close()
			return errors.New("snapshot migration checksum mismatch")
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if n != len(migrations) {
		return errors.New("snapshot migration ledger incomplete")
	}
	if e := checkIntegrity(ctx, db); e != nil {
		return e
	}
	return checkIconIntegrity(ctx, db)
}

// Export produces a verified, standalone SQLite snapshot, never a raw copy of a
// live WAL database. Destination must be new; existing files are never replaced.
func (s *Store) Export(ctx context.Context, destination string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return ErrClosed
	}
	if ext := filepath.Ext(destination); ext != ".sqlite" && ext != ".db" {
		return errors.New("snapshot destination must end in .sqlite or .db")
	}
	f, e := os.CreateTemp(filepath.Dir(destination), ".planner-export-*.sqlite")
	if e != nil {
		return e
	}
	temp := f.Name()
	f.Close()
	defer os.Remove(temp)
	if _, e = s.db.ExecContext(ctx, "VACUUM INTO ?", temp); e != nil {
		return e
	}
	backup, e := openSQL(temp, true)
	if e != nil {
		return e
	}
	e = validateSnapshot(ctx, backup)
	closeErr := backup.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e = os.Link(temp, destination); e != nil {
		return e
	} // atomic, no overwrite
	return syncDirectory(filepath.Dir(destination))
}
func syncDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

// Restore only accepts standalone snapshots. Validation happens on a private
// staging copy; all live row replacements and FK checks form ONE transaction.
// Schema and migration ledger are not replaced by input-controlled SQL.
func (s *Store) Restore(ctx context.Context, snapshot string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return ErrClosed
	}
	source, e := os.Open(snapshot)
	if e != nil {
		return e
	}
	defer source.Close()
	info, e := source.Stat()
	if e != nil {
		return e
	}
	live, e := os.Stat(s.path)
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || os.SameFile(info, live) {
		return errors.New("restore requires a separate standalone snapshot")
	}
	temp, e := os.CreateTemp(filepath.Dir(s.path), ".planner-restore-*.sqlite")
	if e != nil {
		return e
	}
	name := temp.Name()
	defer os.Remove(name)
	_, e = io.Copy(temp, source)
	if e == nil {
		e = temp.Sync()
	}
	ce := temp.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	backup, e := openSQL(name, true)
	if e != nil {
		return e
	}
	e = validateSnapshot(ctx, backup)
	var sourceSchema []string
	if e == nil {
		sourceSchema, e = schema(ctx, backup)
	}
	ce = backup.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	currentSchema, e := schema(ctx, s.db)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(sourceSchema, currentSchema) {
		return errors.New("snapshot schema differs from application schema")
	}
	conn, e := s.db.Conn(ctx)
	if e != nil {
		return e
	}
	defer conn.Close()
	if _, e = conn.ExecContext(ctx, "ATTACH DATABASE ? AS restore_source", name); e != nil {
		return e
	}
	defer conn.ExecContext(context.Background(), "DETACH DATABASE restore_source")
	tx, e := conn.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "PRAGMA defer_foreign_keys=ON"); e != nil {
		return e
	}
	for i := len(dataTables) - 1; i >= 0; i-- {
		if _, e = tx.ExecContext(ctx, "DELETE FROM "+dataTables[i]); e != nil {
			return e
		}
	}
	for _, table := range dataTables {
		order := ""
		if table == "tasks" {
			order = " ORDER BY parent_task_id IS NOT NULL"
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO main."+table+" SELECT * FROM restore_source."+table+order); e != nil {
			return e
		}
	}
	if e = seedBuiltinIcons(ctx, tx); e != nil {
		return e
	}
	if e = checkIntegrity(ctx, tx); e != nil {
		return e
	}
	return tx.Commit()
}
